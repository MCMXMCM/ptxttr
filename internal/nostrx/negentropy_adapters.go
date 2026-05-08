package nostrx

import (
	"context"
	"errors"
	"fmt"

	fnostr "fiatjaf.com/nostr"
)

var (
	// ErrNegentropyUnsupportedFilter is returned when the filter uses search/tags, lacks ids/authors/kinds, or cannot be mirrored in local SQL.
	ErrNegentropyUnsupportedFilter = errors.New("nostrx: negentropy filter not supported for local SQL")

	// ErrNegentropyLocalSetTooLarge is returned when the local COUNT exceeds MaxNegentropyLocalRows.
	ErrNegentropyLocalSetTooLarge = errors.New("nostrx: negentropy local match set exceeds cap")
)

// ValidateNegentropyFilter returns ErrNegentropyUnsupportedFilter when
// NegentropySupportedFilter is false.
func ValidateNegentropyFilter(f fnostr.Filter) error {
	if NegentropySupportedFilter(f) {
		return nil
	}
	return ErrNegentropyUnsupportedFilter
}

// CheckNegentropyLocalBudget validates the filter, COUNTs matches, and errors
// if the set exceeds MaxNegentropyLocalRows.
func CheckNegentropyLocalBudget(ctx context.Context, c NegentropyCache, f fnostr.Filter) (n int64, err error) {
	if err := ValidateNegentropyFilter(f); err != nil {
		return 0, err
	}
	n, err = c.NegentropyLocalMatchCount(ctx, f)
	if err != nil {
		return 0, err
	}
	if n > MaxNegentropyLocalRows {
		return n, fmt.Errorf("%w (%d > %d)", ErrNegentropyLocalSetTooLarge, n, MaxNegentropyLocalRows)
	}
	return n, nil
}

// nostrFilterFromQueryCore maps Query → nostr.Filter (IDs, authors, kinds,
// since, until, limit). Same validation as FetchFrom for hex fields. Does not
// set Tags or enforce unconstrained-query rules.
func nostrFilterFromQueryCore(q Query) (fnostr.Filter, error) {
	filter := fnostr.Filter{
		IDs:     idsFromHex(q.IDs),
		Authors: pubkeysFromHex(q.Authors),
		Kinds:   kindsFromInts(q.Kinds),
		Limit:   q.Limit,
	}
	if len(q.IDs) > 0 && len(filter.IDs) == 0 {
		return fnostr.Filter{}, errors.New("no valid event ids in query")
	}
	if len(q.Authors) > 0 && len(filter.Authors) == 0 {
		return fnostr.Filter{}, errors.New("no valid authors in query")
	}
	if q.Since > 0 {
		filter.Since = fnostr.Timestamp(q.Since)
	}
	if q.Until > 0 {
		filter.Until = fnostr.Timestamp(q.Until)
	}
	return filter, nil
}

// NegentropyFilterFromQuery maps Query → nostr.Filter for the negentropy SQL
// subset (no tags). Limit is clamped like FetchFrom so the wire filter matches.
func NegentropyFilterFromQuery(q Query) (fnostr.Filter, error) {
	if len(q.Tags) > 0 {
		return fnostr.Filter{}, ErrNegentropyUnsupportedFilter
	}
	qn := q
	qn.Limit = ClampRelayQueryLimit(qn.Limit)
	filter, err := nostrFilterFromQueryCore(qn)
	if err != nil {
		return fnostr.Filter{}, err
	}
	if !NegentropySupportedFilter(filter) {
		return fnostr.Filter{}, ErrNegentropyUnsupportedFilter
	}
	return filter, nil
}

// NegentropyPublisherPersist mirrors fetchRelay validation, sets RelayURL, and
// SaveEvent. Invalid events → (ok=false, err=nil); save errors → (ok=false, err!=nil).
func NegentropyPublisherPersist(ctx context.Context, cache NegentropyCache, relayURL string, ev fnostr.Event) (e Event, ok bool, err error) {
	e, ok = validatedRelayEvent(relayURL, ev)
	if !ok {
		return Event{}, false, nil
	}
	if err := cache.SaveEvent(ctx, e); err != nil {
		return Event{}, false, err
	}
	return e, true, nil
}
