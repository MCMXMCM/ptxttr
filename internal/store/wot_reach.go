package store

import (
	"context"
	"database/sql"
	"strings"
)

// reachablePubkeysWithinSQL must stay a single SELECT so SQLite evaluates the
// recursive CTE under one implicit snapshot. Numbered params: ?1 owner hex,
// ?2 clamped max depth (hop count).
const reachablePubkeysWithinSQL = `
WITH RECURSIVE reach(pubkey, depth) AS (
  SELECT target_pubkey, 1
    FROM follow_edges
   WHERE owner_pubkey = ?1 AND target_pubkey != ?1
  UNION
  SELECT fe.target_pubkey, r.depth + 1
    FROM follow_edges fe
    JOIN reach r ON r.pubkey = fe.owner_pubkey
   WHERE r.depth < ?2 AND fe.target_pubkey != ?1
)
SELECT pubkey
  FROM reach
 GROUP BY pubkey
 ORDER BY MIN(depth) ASC, pubkey ASC`

// dbQuerier is the read-only surface shared by *sql.DB and *sql.Tx that
// scanReachablePubkeysWithin needs. Keeping the helper polymorphic lets the
// snapshot-consistency test pin the read inside its own *sql.Tx without the
// production path paying for an extra BEGIN/COMMIT round-trip.
type dbQuerier interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
}

// ReachablePubkeysWithin returns distinct pubkeys reachable from owner within
// depth hops over follow_edges after ClampDepth. Results are ordered by minimum
// hop distance, then pubkey ascending (not parent-discovery order like a
// hand-rolled BFS). The CTE may revisit nodes across paths; work stays bounded by
// max depth and the outer GROUP BY keeps MIN(depth) per pubkey.
//
// Empty owner, nil store/db, or non-positive depth after clamp yields (nil, nil).
//
// SQLite gives single-statement snapshot isolation for any one SELECT, so this
// runs as a bare QueryContext rather than wrapping the read in BeginTx; that
// avoids Begin/Commit round-trips on a hot path while preserving the same
// single-query snapshot as the old read-only transaction wrapper.
func (s *Store) ReachablePubkeysWithin(ctx context.Context, owner string, depth int) ([]string, error) {
	owner = strings.TrimSpace(owner)
	if owner == "" || s == nil || s.db == nil {
		return nil, nil
	}
	depth = ClampDepth(depth)
	if depth <= 0 {
		return nil, nil
	}
	return scanReachablePubkeysWithin(ctx, s.db, owner, depth)
}

func scanReachablePubkeysWithin(ctx context.Context, q dbQuerier, owner string, depth int) ([]string, error) {
	rows, err := q.QueryContext(ctx, reachablePubkeysWithinSQL, owner, depth)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []string
	for rows.Next() {
		var pk string
		if err := rows.Scan(&pk); err != nil {
			return nil, err
		}
		out = append(out, pk)
	}
	return out, rows.Err()
}
