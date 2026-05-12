package nostrx

import (
	"context"
	"errors"
	"iter"
	"os"
	"strings"
	"sync"

	fnostr "fiatjaf.com/nostr"
	"fiatjaf.com/nostr/nip77"
)

// MaxNegentropyLocalRows caps how many SQLite rows may participate in a single
// NIP-77 reconcile (COUNT guard + eligibility).
const MaxNegentropyLocalRows = 50_000

// NegentropySupportedFilter reports whether we can express the filter as a
// bounded SQL predicate over the events table. When true, membership matches
// nostr.Filter.Matches for: IDs, Authors, Kinds, Since, Until. False for Search,
// non-empty Tags, or when none of IDs/Authors/Kinds constrain rows (empty filter).
func NegentropySupportedFilter(f fnostr.Filter) bool {
	if f.Search != "" {
		return false
	}
	if len(f.Tags) > 0 {
		return false
	}
	if len(f.IDs) == 0 && len(f.Authors) == 0 && len(f.Kinds) == 0 {
		return false
	}
	return true
}

// NegentropyCache is the SQLite surface required for NIP-77 download sync.
type NegentropyCache interface {
	SaveEvent(ctx context.Context, event Event) error
	NegentropyLocalMatchCount(ctx context.Context, f fnostr.Filter) (int64, error)
	NegentropyQueryEvents(ctx context.Context, f fnostr.Filter) iter.Seq[fnostr.Event]
}

func negentropyEnvEnabled() bool {
	v := strings.TrimSpace(strings.ToLower(os.Getenv("PTXT_NEGENTROPY")))
	switch v {
	case "1", "true", "yes", "on":
		return true
	case "", "0", "false", "no", "off":
		return false
	default:
		return false
	}
}

// negentropyDownloadTarget implements nostr.Querier + nostr.Publisher for
// download-only NIP-77 (local ids in QueryEvents; relay events via Publish).
type negentropyDownloadTarget struct {
	cache    NegentropyCache
	ctx      context.Context
	relayURL string
	out      *[]Event
	mu       *sync.Mutex
}

func (t *negentropyDownloadTarget) QueryEvents(filter fnostr.Filter) iter.Seq[fnostr.Event] {
	return t.cache.NegentropyQueryEvents(t.ctx, filter)
}

func (t *negentropyDownloadTarget) Publish(ctx context.Context, ev fnostr.Event) error {
	e, saved, err := NegentropyPublisherPersist(ctx, t.cache, t.relayURL, ev)
	if err != nil || !saved {
		return err
	}
	t.mu.Lock()
	*t.out = append(*t.out, e)
	t.mu.Unlock()
	return nil
}

// negentropyPrefetch is the gate before per-relay NIP-77 (local COUNT + cap).
func (c *Client) negentropyPrefetch(ctx context.Context, filter fnostr.Filter) bool {
	if c.negentropy == nil || !negentropyEnvEnabled() {
		return false
	}
	n, err := CheckNegentropyLocalBudget(ctx, c.negentropy, filter)
	return err == nil && n > 0
}

func (c *Client) fetchViaNegentropy(ctx context.Context, relayURL string, filter fnostr.Filter) ([]Event, error) {
	if err := c.acquireOutbound(ctx); err != nil {
		return nil, err
	}
	defer c.releaseOutbound()
	var got []Event
	var mu sync.Mutex
	syncCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	var syncErr error
	var syncErrOnce sync.Once
	target := &negentropyDownloadTarget{
		cache:    c.negentropy,
		ctx:      syncCtx,
		relayURL: relayURL,
		out:      &got,
		mu:       &mu,
	}
	setSyncErr := func(err error) {
		if err == nil {
			return
		}
		syncErrOnce.Do(func() {
			syncErr = err
			cancel()
		})
	}
	err := nip77.NegentropySync(syncCtx, relayURL, filter, nil, target, func(handleCtx context.Context, dir nip77.Direction) {
		syncEventsFromIDsStrict(handleCtx, dir, setSyncErr)
	})
	if syncErr != nil && (err == nil || errors.Is(err, context.Canceled)) {
		return got, syncErr
	}
	return got, err
}

func syncEventsFromIDsStrict(ctx context.Context, dir nip77.Direction, onError func(error)) {
	batch := make([]fnostr.ID, 0, 50)
	seen := make(map[fnostr.ID]struct{})

	flush := func() bool {
		if len(batch) == 0 {
			return ctx.Err() == nil
		}
		for evt := range dir.From.QueryEvents(fnostr.Filter{IDs: batch}) {
			if err := dir.To.Publish(ctx, evt); err != nil {
				onError(err)
				return false
			}
			if ctx.Err() != nil {
				return false
			}
		}
		batch = batch[:0]
		return ctx.Err() == nil
	}

	for {
		select {
		case <-ctx.Done():
			return
		case item, ok := <-dir.Items:
			if !ok {
				flush()
				return
			}
			if _, exists := seen[item]; exists {
				continue
			}
			seen[item] = struct{}{}
			batch = append(batch, item)
			if len(batch) == 50 && !flush() {
				return
			}
		}
	}
}

func negentropyFailReason(err error) string {
	if err == nil {
		return ""
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "timeout"
	}
	if errors.Is(err, context.Canceled) {
		return "other"
	}
	s := strings.ToLower(err.Error())
	switch {
	case strings.Contains(s, "neg-err"), strings.Contains(s, "error_envelope"), strings.Contains(s, "relay returned"):
		return "relay"
	case strings.Contains(s, "reconcile"):
		return "reconcile"
	default:
		return "other"
	}
}

func (c *Client) recordNegentropyError(reason string) {
	switch reason {
	case "relay":
		c.metrics.negentropyErrorRelay.Add(1)
	case "reconcile":
		c.metrics.negentropyErrorReconcile.Add(1)
	case "timeout":
		c.metrics.negentropyErrorTimeout.Add(1)
	default:
		c.metrics.negentropyErrorOther.Add(1)
	}
}
