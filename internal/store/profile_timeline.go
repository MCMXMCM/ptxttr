package store

import (
	"context"

	"ptxt-nstr/internal/nostrx"
)

type ProfileTimelineCounts struct {
	Posts   int
	Replies int
	Media   int
}

func (s *Store) ProfileTimelineCounts(ctx context.Context, pubkey string) (ProfileTimelineCounts, error) {
	if s == nil || s.db == nil || pubkey == "" {
		return ProfileTimelineCounts{}, nil
	}
	var counts ProfileTimelineCounts
	if err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM events e
		WHERE e.pubkey = ? AND e.kind = ?
		AND NOT EXISTS (SELECT 1 FROM tags t WHERE t.event_id = e.id AND t.name = 'e')`,
		pubkey, nostrx.KindTextNote,
	).Scan(&counts.Posts); err != nil {
		return counts, err
	}

	if err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM events e
		WHERE e.pubkey = ? AND e.kind = ?
		AND EXISTS (SELECT 1 FROM tags t WHERE t.event_id = e.id AND t.name = 'e')`,
		pubkey, nostrx.KindTextNote,
	).Scan(&counts.Replies); err != nil {
		return counts, err
	}

	if err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM events e
		WHERE e.pubkey = ? AND e.kind IN (?, ?)
		AND (
			LOWER(e.content) LIKE '%.jpg%' OR
			LOWER(e.content) LIKE '%.jpeg%' OR
			LOWER(e.content) LIKE '%.png%' OR
			LOWER(e.content) LIKE '%.gif%' OR
			LOWER(e.content) LIKE '%.webp%' OR
			LOWER(e.content) LIKE '%.avif%' OR
			LOWER(e.content) LIKE '%.svg%' OR
			LOWER(e.content) LIKE '%.mp4%' OR
			LOWER(e.content) LIKE '%.webm%' OR
			LOWER(e.content) LIKE '%.mov%' OR
			LOWER(e.content) LIKE '%.m4v%' OR
			LOWER(e.content) LIKE '%.mkv%' OR
			LOWER(e.content) LIKE '%.mp3%' OR
			LOWER(e.content) LIKE '%.wav%' OR
			LOWER(e.content) LIKE '%.ogg%' OR
			LOWER(e.content) LIKE '%youtube.com/%' OR
			LOWER(e.content) LIKE '%youtu.be/%' OR
			LOWER(e.content) LIKE '%vimeo.com/%' OR
			LOWER(e.content) LIKE '%tenor.com/%' OR
			LOWER(e.content) LIKE '%giphy.com/%' OR
			EXISTS (SELECT 1 FROM tags t WHERE t.event_id = e.id AND t.name = 'imeta')
		)`,
		pubkey, nostrx.KindTextNote, nostrx.KindRepost,
	).Scan(&counts.Media); err != nil {
		return counts, err
	}

	return counts, nil
}
