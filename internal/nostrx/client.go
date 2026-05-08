package nostrx

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	fnostr "fiatjaf.com/nostr"
	"fiatjaf.com/nostr/nip19"
	"github.com/btcsuite/btcd/btcutil/bech32"
	"github.com/coder/websocket"
)

type Client struct {
	timeout              time.Duration
	ingestVerifyParallel int
	metrics              relayMetrics
	metricsMu            sync.Mutex
	relaysByURL          map[string]*relayMetrics
	penaltyMu            sync.Mutex
	relayPolicy          map[string]relayPolicyState
	negentropy           NegentropyCache

	outboundInitOnce sync.Once
	outboundMu       sync.Mutex
	outboundSlots    chan struct{} // buffered chan: send acquires, recv releases; nil = unlimited
	outboundWait   atomic.Uint64 // count of acquires that had to wait (blocked)
	outboundWaitNs atomic.Uint64 // total wait time while blocked
	outboundCancel atomic.Uint64 // ctx cancelled during acquire wait
}

type Metrics struct {
	Queries        uint64                  `json:"queries"`
	RelayAttempts  uint64                  `json:"relay_attempts"`
	RelayFailures  uint64                  `json:"relay_failures"`
	EventsSeen     uint64                  `json:"events_seen"`
	EventsReturned uint64                  `json:"events_returned"`
	Relays         map[string]RelayMetrics `json:"relays,omitempty"`
	Negentropy     NegentropyMetrics       `json:"negentropy"`
	Outbound       OutboundMetrics         `json:"outbound"`
}

// OutboundMetrics counts global relay connection slot contention (websocket dials).
type OutboundMetrics struct {
	MaxSlots        int    `json:"max_slots"`
	AcquireWaited   uint64 `json:"acquire_waited"`
	AcquireWaitNs   uint64 `json:"acquire_wait_ns"`
	AcquireCanceled uint64 `json:"acquire_canceled"`
}

// NegentropyMetrics counts NIP-77 negentropy attempts in FetchFrom (when enabled).
type NegentropyMetrics struct {
	Attempt        uint64 `json:"attempt"`
	Ok             uint64 `json:"ok"`
	Fallback       uint64 `json:"fallback"`
	ErrorRelay     uint64 `json:"error_relay"`
	ErrorReconcile uint64 `json:"error_reconcile"`
	ErrorTimeout   uint64 `json:"error_timeout"`
	ErrorOther     uint64 `json:"error_other"`
	ErrorTotal     uint64 `json:"error_total"`
}

type RelayMetrics struct {
	RelayAttempts uint64 `json:"relay_attempts"`
	RelayFailures uint64 `json:"relay_failures"`
	EventsSeen    uint64 `json:"events_seen"`
}

type relayMetrics struct {
	queries        atomic.Uint64
	relayAttempts  atomic.Uint64
	relayFailures  atomic.Uint64
	eventsSeen     atomic.Uint64
	eventsReturned atomic.Uint64

	negentropyAttempt        atomic.Uint64
	negentropyOk             atomic.Uint64
	negentropyFallback       atomic.Uint64
	negentropyErrorRelay     atomic.Uint64
	negentropyErrorReconcile atomic.Uint64
	negentropyErrorTimeout   atomic.Uint64
	negentropyErrorOther     atomic.Uint64
}

type relayPolicyError struct {
	kind   string
	reason string
}

type relayPolicyState struct {
	failCount int
	until     time.Time
	reason    string
}

func (e *relayPolicyError) Error() string {
	if e == nil {
		return ""
	}
	if e.reason == "" {
		return "relay policy rejection: " + e.kind
	}
	return "relay policy rejection (" + e.kind + "): " + e.reason
}

type Query struct {
	IDs     []string
	Authors []string
	Kinds   []int
	Tags    map[string][]string
	Since   int64
	Until   int64
	Limit   int
}

type PublishRelayResult struct {
	RelayURL string `json:"relay_url"`
	Accepted bool   `json:"accepted"`
	Message  string `json:"message,omitempty"`
	Error    string `json:"error,omitempty"`
}

type PublishResult struct {
	Results []PublishRelayResult `json:"results"`
}

func (result PublishResult) AcceptedCount() int {
	count := 0
	for _, relay := range result.Results {
		if relay.Accepted {
			count++
		}
	}
	return count
}

func NewClient(relays []string, timeout time.Duration) *Client {
	_ = relays
	return &Client{
		timeout:     timeout,
		relaysByURL: make(map[string]*relayMetrics),
		relayPolicy: make(map[string]relayPolicyState),
	}
}

// SetNegentropyCache sets the local cache for NIP-77 download sync; nil disables it.
func (c *Client) SetNegentropyCache(nc NegentropyCache) {
	if c == nil {
		return
	}
	c.negentropy = nc
}

// SetIngestVerifyParallel caps workers for staged relay batch validation.
// Values 0 or 1 keep sequential validation.
func (c *Client) SetIngestVerifyParallel(parallel int) {
	if c == nil {
		return
	}
	if parallel < 0 {
		parallel = 0
	}
	c.ingestVerifyParallel = parallel
}

// SetRelayMaxOutboundConns limits concurrent outbound relay WebSocket operations
// process-wide (each fetchRelay / publishRelay / negentropy sync holds one slot).
// n <= 0 disables the limit (tests / special deployments).
//
// Only the first call per Client has any effect; later calls are ignored. Call
// this before any outbound relay I/O. Replacing the underlying channel after
// slots have been acquired would deadlock release paths; sync.Once prevents
// that. If you need a different cap, construct a new Client.
func (c *Client) SetRelayMaxOutboundConns(n int) {
	if c == nil {
		return
	}
	c.outboundInitOnce.Do(func() {
		c.outboundMu.Lock()
		defer c.outboundMu.Unlock()
		if n <= 0 {
			c.outboundSlots = nil
			return
		}
		c.outboundSlots = make(chan struct{}, n)
	})
}

func (c *Client) relayMaxOutboundSlots() int {
	if c == nil {
		return 0
	}
	c.outboundMu.Lock()
	ch := c.outboundSlots
	c.outboundMu.Unlock()
	if ch == nil {
		return 0
	}
	return cap(ch)
}

func (c *Client) acquireOutbound(ctx context.Context) error {
	if c == nil {
		return nil
	}
	c.outboundMu.Lock()
	ch := c.outboundSlots
	c.outboundMu.Unlock()
	if ch == nil {
		return nil
	}
	start := time.Now()
	select {
	case <-ctx.Done():
		c.outboundCancel.Add(1)
		return ctx.Err()
	case ch <- struct{}{}:
		if waited := time.Since(start); waited > 0 {
			c.outboundWait.Add(1)
			c.outboundWaitNs.Add(uint64(waited))
		}
		return nil
	}
}

func (c *Client) releaseOutbound() {
	if c == nil {
		return
	}
	c.outboundMu.Lock()
	ch := c.outboundSlots
	c.outboundMu.Unlock()
	if ch == nil {
		return
	}
	<-ch
}

func (c *Client) OutboundMetrics() OutboundMetrics {
	if c == nil {
		return OutboundMetrics{}
	}
	return OutboundMetrics{
		MaxSlots:        c.relayMaxOutboundSlots(),
		AcquireWaited:   c.outboundWait.Load(),
		AcquireWaitNs:   c.outboundWaitNs.Load(),
		AcquireCanceled: c.outboundCancel.Load(),
	}
}

func (c *Client) Metrics() Metrics {
	relays := make(map[string]RelayMetrics)
	c.metricsMu.Lock()
	for relayURL, metrics := range c.relaysByURL {
		relays[relayURL] = RelayMetrics{
			RelayAttempts: metrics.relayAttempts.Load(),
			RelayFailures: metrics.relayFailures.Load(),
			EventsSeen:    metrics.eventsSeen.Load(),
		}
	}
	c.metricsMu.Unlock()
	relayErr := c.metrics.negentropyErrorRelay.Load()
	reconcileErr := c.metrics.negentropyErrorReconcile.Load()
	timeoutErr := c.metrics.negentropyErrorTimeout.Load()
	otherErr := c.metrics.negentropyErrorOther.Load()
	return Metrics{
		Queries:        c.metrics.queries.Load(),
		RelayAttempts:  c.metrics.relayAttempts.Load(),
		RelayFailures:  c.metrics.relayFailures.Load(),
		EventsSeen:     c.metrics.eventsSeen.Load(),
		EventsReturned: c.metrics.eventsReturned.Load(),
		Relays:         relays,
		Negentropy: NegentropyMetrics{
			Attempt:        c.metrics.negentropyAttempt.Load(),
			Ok:             c.metrics.negentropyOk.Load(),
			Fallback:       c.metrics.negentropyFallback.Load(),
			ErrorRelay:     relayErr,
			ErrorReconcile: reconcileErr,
			ErrorTimeout:   timeoutErr,
			ErrorOther:     otherErr,
			ErrorTotal:     relayErr + reconcileErr + timeoutErr + otherErr,
		},
		Outbound: c.OutboundMetrics(),
	}
}

func (c *Client) FetchFrom(ctx context.Context, relays []string, query Query) ([]Event, error) {
	relays = NormalizeRelayList(relays, MaxRelays)
	if len(relays) == 0 {
		return nil, errors.New("no relays configured")
	}
	query.Limit = ClampRelayQueryLimit(query.Limit)
	maxAccumulated := query.Limit * 4
	if maxAccumulated < query.Limit {
		maxAccumulated = query.Limit
	}
	c.metrics.queries.Add(1)
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()

	filter, err := nostrFilterFromQueryCore(query)
	if err != nil {
		return nil, err
	}
	filter.Tags = fnostr.TagMap(query.Tags)
	if len(query.IDs) == 0 && len(query.Authors) == 0 && len(query.Kinds) == 0 && len(query.Tags) == 0 {
		return nil, errors.New("refusing unconstrained relay query")
	}
	var events []Event
	var eventsMu sync.Mutex
	var wg sync.WaitGroup
	negFilter, negErr := NegentropyFilterFromQuery(query)
	negTry := negErr == nil && c.negentropyPrefetch(ctx, negFilter)

	for _, relayURL := range relays {
		if c.relayPolicyBlocked(relayURL, time.Now()) {
			continue
		}
		wg.Add(1)
		go func(relayURL string) {
			defer wg.Done()
			c.metrics.relayAttempts.Add(1)
			c.recordRelayAttempt(relayURL)
			var relayEvents []Event
			var err error
			if negTry {
				negBudget := c.timeout / 3
				if negBudget <= 0 {
					negBudget = c.timeout
				}
				negCtx, negCancel := context.WithTimeout(ctx, negBudget)
				c.metrics.negentropyAttempt.Add(1)
				relayEvents, err = c.fetchViaNegentropy(negCtx, relayURL, negFilter)
				negCancel()
				if err != nil {
					c.recordNegentropyError(negentropyFailReason(err))
					c.metrics.negentropyFallback.Add(1)
					relayEvents, err = c.fetchRelay(ctx, relayURL, filter, query.Limit)
				} else {
					c.metrics.negentropyOk.Add(1)
				}
			} else {
				relayEvents, err = c.fetchRelay(ctx, relayURL, filter, query.Limit)
			}
			if err != nil {
				c.metrics.relayFailures.Add(1)
				c.recordRelayFailure(relayURL)
				var policyErr *relayPolicyError
				if errors.As(err, &policyErr) {
					c.recordRelayPolicyRejection(relayURL, policyErr.kind, policyErr.reason, time.Now())
				} else {
					// Plain connection/timeout failures also accumulate so
					// chronically unreachable relays get short-circuited
					// after a few consecutive failures instead of being
					// retried on every request.
					c.recordRelayConnectionFailure(relayURL, err, time.Now())
				}
				return
			}
			c.clearRelayPolicyRejection(relayURL)
			c.metrics.eventsSeen.Add(uint64(len(relayEvents)))
			c.recordRelayEvents(relayURL, len(relayEvents))
			if len(relayEvents) == 0 {
				return
			}
			eventsMu.Lock()
			events = append(events, relayEvents...)
			if len(events) > maxAccumulated {
				events = events[:maxAccumulated]
			}
			eventsMu.Unlock()
		}(relayURL)
	}
	wg.Wait()

	events = DedupeEvents(events, query.Limit)
	c.metrics.eventsReturned.Add(uint64(len(events)))
	if len(events) > 0 {
		return events, nil
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return events, nil
}

func (c *Client) PublishTo(ctx context.Context, relays []string, event Event) (PublishResult, error) {
	relays = NormalizeRelayList(relays, MaxRelays)
	if len(relays) == 0 {
		return PublishResult{}, errors.New("no relays configured")
	}
	externalEvent, err := toExternalEvent(event)
	if err != nil {
		return PublishResult{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	results := make([]PublishRelayResult, len(relays))
	var wg sync.WaitGroup
	for index, relayURL := range relays {
		index, relayURL := index, relayURL
		if c.relayPolicyBlocked(relayURL, time.Now()) {
			results[index] = PublishRelayResult{
				RelayURL: relayURL,
				Accepted: false,
				Error:    "relay temporarily blocked by policy backoff",
			}
			continue
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			c.metrics.relayAttempts.Add(1)
			c.recordRelayAttempt(relayURL)
			relayResult := c.publishRelay(ctx, relayURL, externalEvent)
			results[index] = relayResult
			if relayResult.Accepted {
				c.clearRelayPolicyRejection(relayURL)
				return
			}
			c.metrics.relayFailures.Add(1)
			c.recordRelayFailure(relayURL)
			lowerReason := strings.ToLower(relayResult.Message + " " + relayResult.Error)
			switch {
			case strings.Contains(lowerReason, "auth"), strings.Contains(lowerReason, "challenge"):
				c.recordRelayPolicyRejection(relayURL, "auth", relayResult.Message+" "+relayResult.Error, time.Now())
			case strings.Contains(lowerReason, "blocked"), strings.Contains(lowerReason, "reject"), strings.Contains(lowerReason, "forbidden"), strings.Contains(lowerReason, "denied"):
				c.recordRelayPolicyRejection(relayURL, "blocked", relayResult.Message+" "+relayResult.Error, time.Now())
			case relayResult.Error != "":
				c.recordRelayConnectionFailure(relayURL, errors.New(relayResult.Error), time.Now())
			}
		}()
	}
	wg.Wait()
	return PublishResult{Results: results}, nil
}

func (c *Client) relayMetrics(relayURL string) *relayMetrics {
	c.metricsMu.Lock()
	defer c.metricsMu.Unlock()
	if c.relaysByURL == nil {
		c.relaysByURL = make(map[string]*relayMetrics)
	}
	metrics := c.relaysByURL[relayURL]
	if metrics == nil {
		metrics = &relayMetrics{}
		c.relaysByURL[relayURL] = metrics
	}
	return metrics
}

func (c *Client) recordRelayAttempt(relayURL string) {
	c.relayMetrics(relayURL).relayAttempts.Add(1)
}

func (c *Client) recordRelayFailure(relayURL string) {
	c.relayMetrics(relayURL).relayFailures.Add(1)
}

func (c *Client) recordRelayEvents(relayURL string, count int) {
	c.relayMetrics(relayURL).eventsSeen.Add(uint64(count))
}

func NormalizeRelayList(relays []string, max int) []string {
	if max <= 0 {
		max = MaxRelays
	}
	seen := make(map[string]bool)
	out := make([]string, 0, len(relays))
	for _, relay := range relays {
		normalized, err := NormalizeRelayURL(relay)
		if err != nil || seen[normalized] {
			continue
		}
		seen[normalized] = true
		out = append(out, normalized)
		if len(out) >= max {
			break
		}
	}
	return out
}

func (c *Client) fetchRelay(ctx context.Context, relayURL string, filter fnostr.Filter, limit int) ([]Event, error) {
	if err := c.acquireOutbound(ctx); err != nil {
		return nil, err
	}
	defer c.releaseOutbound()
	conn, _, err := websocket.Dial(ctx, relayURL, defaultRelayDialOptions())
	if err != nil {
		return nil, err
	}
	defer func() { _ = conn.Close(websocket.StatusNormalClosure, "query complete") }()
	conn.SetReadLimit(4 << 20)

	subID := "ptxt-" + randomHex(8)
	req, err := json.Marshal([]any{"REQ", subID, filter})
	if err != nil {
		return nil, err
	}
	if err := conn.Write(ctx, websocket.MessageText, req); err != nil {
		return nil, err
	}

	var rawEvents []fnostr.Event
	for len(rawEvents) < limit {
		msgType, msg, err := conn.Read(ctx)
		if err != nil {
			if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) || errors.Is(err, io.EOF) {
				break
			}
			return c.validateRelayBatch(relayURL, rawEvents), err
		}
		if msgType != websocket.MessageText {
			continue
		}
		var envelope []json.RawMessage
		if err := json.Unmarshal(msg, &envelope); err != nil || len(envelope) < 2 {
			continue
		}
		var typ string
		if err := json.Unmarshal(envelope[0], &typ); err != nil {
			continue
		}
		switch typ {
		case "EOSE":
			return c.validateRelayBatch(relayURL, rawEvents), nil
		case "CLOSED":
			reason := ""
			if len(envelope) >= 3 {
				_ = json.Unmarshal(envelope[2], &reason)
			}
			if reason != "" {
				slog.Info("relay CLOSED", "relay", relayURL, "events", len(rawEvents), "reason", reason)
				if kind, ok := classifyClosedPolicyReason(reason); ok {
					return c.validateRelayBatch(relayURL, rawEvents), &relayPolicyError{kind: kind, reason: reason}
				}
			}
			return c.validateRelayBatch(relayURL, rawEvents), nil
		case "NOTICE":
			var notice string
			_ = json.Unmarshal(envelope[1], &notice)
			slog.Debug("relay NOTICE", "relay", relayURL, "notice", notice)
			continue
		case "AUTH":
			// NIP-42 challenge. We can't authenticate as a server, so abandon this relay.
			slog.Info("relay AUTH challenge - skipping (anonymous client)", "relay", relayURL)
			return c.validateRelayBatch(relayURL, rawEvents), &relayPolicyError{kind: "auth", reason: "nip42 challenge"}
		case "EVENT":
			if len(envelope) < 3 {
				continue
			}
			var gotSubID string
			if err := json.Unmarshal(envelope[1], &gotSubID); err != nil || gotSubID != subID {
				continue
			}
			var ev fnostr.Event
			if err := json.Unmarshal(envelope[2], &ev); err != nil {
				continue
			}
			rawEvents = append(rawEvents, ev)
		}
	}
	_ = conn.Write(ctx, websocket.MessageText, []byte(fmt.Sprintf(`["CLOSE","%s"]`, subID)))
	return c.validateRelayBatch(relayURL, rawEvents), nil
}

func (c *Client) validateRelayBatch(relayURL string, rawEvents []fnostr.Event) []Event {
	return ValidateRelayIngestBatch(relayURL, rawEvents, c.ingestVerifyParallel)
}

func (c *Client) publishRelay(ctx context.Context, relayURL string, event fnostr.Event) PublishRelayResult {
	result := PublishRelayResult{RelayURL: relayURL}
	if err := c.acquireOutbound(ctx); err != nil {
		result.Error = err.Error()
		return result
	}
	defer c.releaseOutbound()
	conn, _, err := websocket.Dial(ctx, relayURL, defaultRelayDialOptions())
	if err != nil {
		result.Error = err.Error()
		return result
	}
	defer func() { _ = conn.Close(websocket.StatusNormalClosure, "publish complete") }()
	conn.SetReadLimit(4 << 20)
	envelope, err := json.Marshal([]any{"EVENT", event})
	if err != nil {
		result.Error = err.Error()
		return result
	}
	if err := conn.Write(ctx, websocket.MessageText, envelope); err != nil {
		result.Error = err.Error()
		return result
	}
	lastNotice := ""
	for {
		msgType, msg, err := conn.Read(ctx)
		if err != nil {
			if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) || errors.Is(err, io.EOF) {
				if lastNotice != "" {
					result.Message = lastNotice
					return result
				}
			}
			result.Error = err.Error()
			return result
		}
		if msgType != websocket.MessageText {
			continue
		}
		var parsed []json.RawMessage
		if err := json.Unmarshal(msg, &parsed); err != nil || len(parsed) < 1 {
			continue
		}
		var typ string
		if err := json.Unmarshal(parsed[0], &typ); err != nil {
			continue
		}
		switch typ {
		case "NOTICE":
			if len(parsed) < 2 {
				continue
			}
			_ = json.Unmarshal(parsed[1], &lastNotice)
		case "AUTH":
			result.Message = "relay requested NIP-42 auth challenge"
			return result
		case "CLOSED":
			if len(parsed) >= 3 {
				_ = json.Unmarshal(parsed[2], &result.Message)
			}
			return result
		case "OK":
			if len(parsed) < 4 {
				continue
			}
			var eventID string
			if err := json.Unmarshal(parsed[1], &eventID); err != nil || eventID != event.ID.Hex() {
				continue
			}
			_ = json.Unmarshal(parsed[2], &result.Accepted)
			_ = json.Unmarshal(parsed[3], &result.Message)
			return result
		}
	}
}

func classifyClosedPolicyReason(reason string) (string, bool) {
	lower := strings.ToLower(strings.TrimSpace(reason))
	if lower == "" {
		return "", false
	}
	switch {
	case strings.Contains(lower, "blocked"), strings.Contains(lower, "reject"), strings.Contains(lower, "forbidden"), strings.Contains(lower, "denied"):
		return "blocked", true
	case strings.Contains(lower, "auth"), strings.Contains(lower, "login"), strings.Contains(lower, "credential"):
		return "auth", true
	default:
		return "", false
	}
}

func relayPolicyBackoff(failures int) time.Duration {
	if failures <= 0 {
		return 0
	}
	backoff := 2 * time.Minute
	for i := 1; i < failures; i++ {
		backoff *= 2
		if backoff >= 30*time.Minute {
			return 30 * time.Minute
		}
	}
	if backoff > 30*time.Minute {
		backoff = 30 * time.Minute
	}
	return backoff
}

func (c *Client) relayPolicyBlocked(relayURL string, now time.Time) bool {
	c.penaltyMu.Lock()
	defer c.penaltyMu.Unlock()
	policy, ok := c.relayPolicy[relayURL]
	if !ok {
		return false
	}
	if now.Before(policy.until) {
		return true
	}
	delete(c.relayPolicy, relayURL)
	return false
}

func (c *Client) recordRelayPolicyRejection(relayURL, kind, reason string, now time.Time) {
	c.penaltyMu.Lock()
	defer c.penaltyMu.Unlock()
	state := c.relayPolicy[relayURL]
	state.failCount++
	state.reason = kind
	state.until = now.Add(relayPolicyBackoff(state.failCount))
	c.relayPolicy[relayURL] = state
	slog.Info("relay policy backoff", "relay", relayURL, "kind", kind, "retry_in", time.Until(state.until).Round(time.Second), "detail", reason)
}

// connFailureBlockThreshold is the number of consecutive plain (non-policy)
// connection failures we tolerate before blocking the relay for a backoff
// window. Single transient hiccups don't count against the relay; a relay
// that's been unreachable for multiple requests in a row does.
const connFailureBlockThreshold = 3

func connFailureBackoff(failures int) time.Duration {
	if failures < connFailureBlockThreshold {
		return 0
	}
	excess := failures - connFailureBlockThreshold
	backoff := 30 * time.Second
	for i := 0; i < excess; i++ {
		backoff *= 2
		if backoff >= 10*time.Minute {
			return 10 * time.Minute
		}
	}
	return backoff
}

// recordRelayConnectionFailure tracks consecutive plain failures (DNS error,
// timeout, EOF, refused, etc.) and applies a temporary block once the
// threshold is exceeded. Recovery is automatic: a successful query clears
// the counter via clearRelayPolicyRejection.
func (c *Client) recordRelayConnectionFailure(relayURL string, err error, now time.Time) {
	c.penaltyMu.Lock()
	defer c.penaltyMu.Unlock()
	state := c.relayPolicy[relayURL]
	state.failCount++
	state.reason = "connection"
	backoff := connFailureBackoff(state.failCount)
	if backoff > 0 {
		state.until = now.Add(backoff)
		slog.Info("relay connection backoff",
			"relay", relayURL,
			"failures", state.failCount,
			"retry_in", backoff.Round(time.Second),
			"err", err)
	}
	c.relayPolicy[relayURL] = state
}

func (c *Client) clearRelayPolicyRejection(relayURL string) {
	c.penaltyMu.Lock()
	defer c.penaltyMu.Unlock()
	delete(c.relayPolicy, relayURL)
}

func (c *Client) FetchRelayInfo(ctx context.Context, relayURL string) RelayInfo {
	normalized, err := NormalizeRelayURL(relayURL)
	if err != nil {
		return RelayInfo{URL: relayURL, Error: err.Error()}
	}
	infoURL := strings.Replace(normalized, "wss://", "https://", 1)
	infoURL = strings.Replace(infoURL, "ws://", "http://", 1)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, infoURL, nil)
	if err != nil {
		return RelayInfo{URL: normalized, Error: err.Error()}
	}
	req.Header.Set("Accept", "application/nostr+json")
	client := http.Client{Timeout: c.timeout}
	res, err := client.Do(req)
	if err != nil {
		return RelayInfo{URL: normalized, Error: err.Error()}
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return RelayInfo{URL: normalized, Error: fmt.Sprintf("NIP-11 returned %s", res.Status)}
	}
	var info RelayInfo
	if err := json.NewDecoder(res.Body).Decode(&info); err != nil {
		return RelayInfo{URL: normalized, Error: err.Error()}
	}
	info.URL = normalized
	return info
}

func DecodeIdentifier(value string) (string, error) {
	value = strings.TrimSpace(value)
	lower := strings.ToLower(value)
	if strings.HasPrefix(lower, "nostr:") || strings.HasPrefix(lower, "npub") || strings.HasPrefix(lower, "nprofile") {
		ref, err := DecodeNIP27Reference(value)
		if err != nil {
			return "", err
		}
		if ref.PubKey == "" {
			return "", errors.New("nostr identifier does not contain a public key")
		}
		return NormalizePubKey(ref.PubKey)
	}
	return NormalizePubKey(value)
}

func EncodeNPub(pubkey string) string {
	pk, err := fnostr.PubKeyFromHex(pubkey)
	if err != nil {
		return ""
	}
	return nip19.EncodeNpub(pk)
}

func EncodeNEvent(id string, pubkey string) string {
	eventID, err := fnostr.IDFromHex(id)
	if err != nil {
		return id
	}
	author, err := fnostr.PubKeyFromHex(pubkey)
	if err != nil {
		return nip19.EncodeNevent(eventID, nil, fnostr.ZeroPK)
	}
	return nip19.EncodeNevent(eventID, nil, author)
}

// EncodeNEventWithKind encodes an nevent NIP-19 string, including a kind TLV
// when kind is positive (e.g. KindLongForm for NIP-23).
func EncodeNEventWithKind(id string, pubkey string, kind int) string {
	eventID, err := fnostr.IDFromHex(id)
	if err != nil {
		return ""
	}
	const (
		tlvID     = 0
		tlvAuthor = 2
		tlvKind   = 3
	)
	var buf bytes.Buffer
	writeTLV := func(typ uint8, value []byte) {
		buf.WriteByte(typ)
		buf.WriteByte(byte(len(value)))
		buf.Write(value)
	}
	writeTLV(tlvID, eventID[:])
	if pubkey != "" {
		if author, err := fnostr.PubKeyFromHex(pubkey); err == nil && author != fnostr.ZeroPK {
			writeTLV(tlvAuthor, author[:])
		}
	}
	if kind > 0 {
		var kindBytes [4]byte
		binary.BigEndian.PutUint32(kindBytes[:], uint32(kind))
		writeTLV(tlvKind, kindBytes[:])
	}
	bits5, err := bech32.ConvertBits(buf.Bytes(), 8, 5, true)
	if err != nil {
		return ""
	}
	out, err := bech32.Encode("nevent", bits5)
	if err != nil {
		return ""
	}
	return out
}

// validatedRelayEvent converts a wire event, sets relayURL, and checks
// IngestFromRelay rules. ok is false when the event is dropped as invalid.
func validatedRelayEvent(relayURL string, ev fnostr.Event) (Event, bool) {
	e := fromExternalEvent(ev)
	e.RelayURL = relayURL
	if err := ValidateIngestEvent(IngestFromRelay, e); err != nil {
		return Event{}, false
	}
	return e, true
}

func fromExternalEvent(ev fnostr.Event) Event {
	event := Event{
		ID:        ev.ID.Hex(),
		PubKey:    ev.PubKey.Hex(),
		CreatedAt: int64(ev.CreatedAt),
		Kind:      int(ev.Kind),
		Content:   ev.Content,
		Sig:       hex.EncodeToString(ev.Sig[:]),
	}
	for _, tag := range ev.Tags {
		event.Tags = append(event.Tags, []string(tag))
	}
	return event
}

func toExternalEvent(event Event) (fnostr.Event, error) {
	id, err := fnostr.IDFromHex(event.ID)
	if err != nil {
		return fnostr.Event{}, errors.New("invalid event id")
	}
	pubkey, err := fnostr.PubKeyFromHex(event.PubKey)
	if err != nil {
		return fnostr.Event{}, errors.New("invalid event pubkey")
	}
	sigBytes, err := hex.DecodeString(event.Sig)
	if err != nil || len(sigBytes) != 64 {
		return fnostr.Event{}, errors.New("invalid event signature")
	}
	var sig [64]byte
	copy(sig[:], sigBytes)
	tags := make(fnostr.Tags, 0, len(event.Tags))
	for _, tag := range event.Tags {
		if len(tag) == 0 {
			continue
		}
		tags = append(tags, fnostr.Tag(tag))
	}
	return fnostr.Event{
		ID:        id,
		PubKey:    pubkey,
		CreatedAt: fnostr.Timestamp(event.CreatedAt),
		Kind:      fnostr.Kind(event.Kind),
		Tags:      tags,
		Content:   event.Content,
		Sig:       sig,
	}, nil
}

func idsFromHex(values []string) []fnostr.ID {
	var ids []fnostr.ID
	for _, value := range values {
		id, err := fnostr.IDFromHex(value)
		if err == nil {
			ids = append(ids, id)
		}
	}
	return ids
}

func pubkeysFromHex(values []string) []fnostr.PubKey {
	var pubkeys []fnostr.PubKey
	for _, value := range values {
		pubkey, err := fnostr.PubKeyFromHex(value)
		if err == nil {
			pubkeys = append(pubkeys, pubkey)
		}
	}
	return pubkeys
}

func kindsFromInts(values []int) []fnostr.Kind {
	var kinds []fnostr.Kind
	for _, value := range values {
		kinds = append(kinds, fnostr.Kind(value))
	}
	return kinds
}

func randomHex(n int) string {
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		panic("crypto/rand failed for relay subscription id")
	}
	return fmt.Sprintf("%x", buf)
}
