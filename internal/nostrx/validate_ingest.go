package nostrx

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"

	fnostr "fiatjaf.com/nostr"
)

// IngestSource identifies which validation rules apply before an event is
// accepted into the local cache or API publish path.
type IngestSource string

const (
	// IngestFromRelay is for events received from a Nostr relay (REQ / negentropy).
	// Only cryptographic validity is required; kinds are not restricted.
	IngestFromRelay IngestSource = "relay"
	// IngestFromHTTPAPI is for the browser/API publish endpoint; kinds and
	// content limits match handlers_api policy.
	IngestFromHTTPAPI IngestSource = "http_api"
	// IngestPersisted is for events saved locally after publish or other
	// trusted paths without RelayURL set; same crypto checks as relay ingest.
	IngestPersisted IngestSource = "persisted"
)

const publishMaxContentBytes = 64000

// maxRelayHintsForPublish caps relay preference tags when validating kind 10002.
const maxRelayHintsForPublish = MaxRelays * 3

// ValidateIngestEvent runs source-specific checks then ValidateSignedEvent.
func ValidateIngestEvent(source IngestSource, event Event) error {
	switch source {
	case IngestFromHTTPAPI:
		if err := validateHTTPAPIIngestShape(event); err != nil {
			return err
		}
	case IngestFromRelay, IngestPersisted:
		// Shape rules only; crypto is always ValidateSignedEvent below.
	default:
		return fmt.Errorf("nostrx: unknown ingest source %q", source)
	}
	return ValidateSignedEvent(event)
}

// IngestSourceForStoreSave picks relay vs persisted rules using RelayURL.
func IngestSourceForStoreSave(event Event) IngestSource {
	if strings.TrimSpace(event.RelayURL) != "" {
		return IngestFromRelay
	}
	return IngestPersisted
}

func validateHTTPAPIIngestShape(event Event) error {
	if strings.TrimSpace(event.ID) == "" || strings.TrimSpace(event.PubKey) == "" || strings.TrimSpace(event.Sig) == "" {
		return errors.New("signed event payload is required")
	}
	switch event.Kind {
	case KindTextNote, KindComment, KindRepost, KindProfileMetadata, KindFollowList, KindMuteList, KindBookmarkList, KindRelayListMetadata:
	case KindReaction:
		if err := ValidateReactionHTTPAPIShape(event); err != nil {
			return err
		}
	default:
		return fmt.Errorf("kind %d is not accepted for API publish", event.Kind)
	}
	if len(event.Content) > publishMaxContentBytes {
		return errors.New("event content is too large")
	}
	if event.Kind == KindProfileMetadata {
		var asObject map[string]any
		if err := json.Unmarshal([]byte(event.Content), &asObject); err != nil {
			return errors.New("kind 0 content must be valid JSON")
		}
	}
	if event.Kind == KindRelayListMetadata {
		return validateRelayListMetadataForPublish(event)
	}
	if event.Kind == KindMuteList {
		return validateMuteListForPublish(event)
	}
	return nil
}

func validateMuteListForPublish(event Event) error {
	if len(event.Tags) > MaxMuteListTagRows {
		return fmt.Errorf("kind %d tag list is too large (max %d)", KindMuteList, MaxMuteListTagRows)
	}
	for _, tag := range event.Tags {
		if len(tag) < 2 {
			continue
		}
		if tag[0] != "p" {
			continue
		}
		if _, err := NormalizePubKey(tag[1]); err != nil {
			return fmt.Errorf("kind %d p tag has invalid pubkey", KindMuteList)
		}
	}
	return nil
}

func validateRelayListMetadataForPublish(event Event) error {
	if strings.TrimSpace(event.Content) != "" {
		return errors.New("kind 10002 content must be empty")
	}
	if len(event.Tags) == 0 {
		return nil
	}
	ev := event
	hints := RelayHints(&ev, maxRelayHintsForPublish)
	if len(hints) == 0 {
		return errors.New("kind 10002 tags must contain valid relay entries")
	}
	return nil
}

// ValidateRelayIngestBatch converts relay wire events, applies IngestFromRelay
// validation, and returns only valid events in original order.
func ValidateRelayIngestBatch(relayURL string, rawEvents []fnostr.Event, parallel int) []Event {
	if len(rawEvents) == 0 {
		return nil
	}
	if parallel <= 1 || len(rawEvents) == 1 {
		out := make([]Event, 0, len(rawEvents))
		for _, raw := range rawEvents {
			event, ok := validatedRelayEvent(relayURL, raw)
			if ok {
				out = append(out, event)
			}
		}
		return out
	}

	workers := parallel
	if workers > len(rawEvents) {
		workers = len(rawEvents)
	}
	results := make([]Event, len(rawEvents))
	valid := make([]bool, len(rawEvents))
	jobs := make(chan int, len(rawEvents))
	for idx := range rawEvents {
		jobs <- idx
	}
	close(jobs)

	var wg sync.WaitGroup
	for worker := 0; worker < workers; worker++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for idx := range jobs {
				event, ok := validatedRelayEvent(relayURL, rawEvents[idx])
				if ok {
					results[idx] = event
					valid[idx] = true
				}
			}
		}()
	}
	wg.Wait()

	out := make([]Event, 0, len(rawEvents))
	for idx, ok := range valid {
		if ok {
			out = append(out, results[idx])
		}
	}
	return out
}

// VerifyIngestEventsBeforeSave validates events before persistence. The
// sequential branch (parallel <= 1) preserves submission order for errors.
// When parallel > 1, up to min(parallel, len(events)) workers run concurrently
// (order of completion is undefined); the first validation error wins.
func VerifyIngestEventsBeforeSave(parallel int, events []Event) error {
	if len(events) == 0 {
		return nil
	}
	if parallel <= 1 {
		for _, ev := range events {
			if err := ValidateIngestEvent(IngestSourceForStoreSave(ev), ev); err != nil {
				return err
			}
		}
		return nil
	}

	n := parallel
	if n > len(events) {
		n = len(events)
	}
	jobs := make(chan int, len(events))
	for i := range events {
		jobs <- i
	}
	close(jobs)

	var wg sync.WaitGroup
	var mu sync.Mutex
	var firstErr error
	for w := 0; w < n; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for idx := range jobs {
				ev := events[idx]
				if err := ValidateIngestEvent(IngestSourceForStoreSave(ev), ev); err != nil {
					mu.Lock()
					if firstErr == nil {
						firstErr = err
					}
					mu.Unlock()
				}
			}
		}()
	}
	wg.Wait()
	return firstErr
}
