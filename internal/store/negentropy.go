package store

import (
	"context"
	"fmt"
	"iter"
	"strings"

	fnostr "fiatjaf.com/nostr"

	"ptxt-nstr/internal/nostrx"
)

var _ nostrx.NegentropyCache = (*Store)(nil)

func negentropyWhereClause(f fnostr.Filter) (string, []any, bool) {
	if !nostrx.NegentropySupportedFilter(f) {
		return "", nil, false
	}
	var parts []string
	var args []any
	if len(f.IDs) > 0 {
		parts = append(parts, "id IN ("+placeholders(len(f.IDs))+")")
		for _, id := range f.IDs {
			args = append(args, id.Hex())
		}
	}
	if len(f.Authors) > 0 {
		parts = append(parts, "pubkey IN ("+placeholders(len(f.Authors))+")")
		for _, pk := range f.Authors {
			args = append(args, pk.Hex())
		}
	}
	if len(f.Kinds) > 0 {
		parts = append(parts, "kind IN ("+placeholders(len(f.Kinds))+")")
		for _, k := range f.Kinds {
			args = append(args, int(k))
		}
	}
	if f.Since != 0 {
		parts = append(parts, "created_at >= ?")
		args = append(args, int64(f.Since))
	}
	if f.Until != 0 {
		parts = append(parts, "created_at <= ?")
		args = append(args, int64(f.Until))
	}
	return strings.Join(parts, " AND "), args, true
}

func negentropyLimitSQL(limit int) (string, []any) {
	if limit <= 0 {
		return "", nil
	}
	return " ORDER BY created_at DESC, id DESC LIMIT ?", []any{limit}
}

// NegentropyLocalMatchCount returns how many cached events match the filter.
func (s *Store) NegentropyLocalMatchCount(ctx context.Context, f fnostr.Filter) (int64, error) {
	wh, args, ok := negentropyWhereClause(f)
	if !ok {
		return 0, fmt.Errorf("%w", nostrx.ErrNegentropyUnsupportedFilter)
	}
	limitSQL, limitArgs := negentropyLimitSQL(f.Limit)
	q := `SELECT COUNT(*) FROM (SELECT 1 FROM events WHERE ` + wh + limitSQL + `)`
	row := s.db.QueryRowContext(ctx, q, append(args, limitArgs...)...)
	var n int64
	if err := row.Scan(&n); err != nil {
		return 0, err
	}
	return n, nil
}

// NegentropyQueryEvents yields minimal nostr.Event values (ID + CreatedAt) for
// rows matching the filter, ordered for stable scans. For unsupported filters
// it yields nothing; call nostrx.ValidateNegentropyFilter first if you need
// nostrx.ErrNegentropyUnsupportedFilter.
func (s *Store) NegentropyQueryEvents(ctx context.Context, f fnostr.Filter) iter.Seq[fnostr.Event] {
	return func(yield func(fnostr.Event) bool) {
		wh, args, ok := negentropyWhereClause(f)
		if !ok {
			return
		}
		limitSQL, limitArgs := negentropyLimitSQL(f.Limit)
		q := `SELECT id, created_at FROM (` +
			`SELECT id, created_at FROM events WHERE ` + wh + limitSQL +
			`) ORDER BY created_at ASC, id ASC`
		rows, err := s.db.QueryContext(ctx, q, append(args, limitArgs...)...)
		if err != nil {
			return
		}
		defer func() { _ = rows.Close() }()
		for rows.Next() {
			var idHex string
			var created int64
			if err := rows.Scan(&idHex, &created); err != nil {
				return
			}
			id, err := fnostr.IDFromHex(idHex)
			if err != nil {
				continue
			}
			ev := fnostr.Event{
				ID:        id,
				CreatedAt: fnostr.Timestamp(created),
			}
			if !yield(ev) {
				return
			}
		}
		_ = rows.Err()
	}
}
