package store

import (
	"context"
	"database/sql"
	"errors"
)

type ShareArtifactRecord struct {
	Token          string
	ViewerPubkey   string
	NoteID         string
	RootID         string
	ParentID       string
	Surface        string
	HasImage       bool
	ImageURL       string
	LiveThreadURL  string
	PayloadJSON    string
	CreatedAt      int64
	LastAccessedAt int64
}

func (s *Store) CreateShareArtifact(ctx context.Context, rec ShareArtifactRecord) error {
	if s == nil || s.db == nil || rec.Token == "" || rec.NoteID == "" || rec.Surface == "" || rec.LiveThreadURL == "" || rec.PayloadJSON == "" {
		return nil
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	_, err := s.db.ExecContext(ctx, `INSERT INTO share_artifacts(
			token, viewer_pubkey, note_id, root_id, parent_id, surface, has_image, image_url, live_thread_url, payload_json, created_at, last_accessed_at
		) VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		rec.Token,
		rec.ViewerPubkey,
		rec.NoteID,
		rec.RootID,
		rec.ParentID,
		rec.Surface,
		boolToInt(rec.HasImage),
		rec.ImageURL,
		rec.LiveThreadURL,
		rec.PayloadJSON,
		rec.CreatedAt,
		rec.LastAccessedAt,
	)
	return err
}

func (s *Store) ShareArtifactByToken(ctx context.Context, token string) (*ShareArtifactRecord, bool, error) {
	if s == nil || s.db == nil || token == "" {
		return nil, false, nil
	}
	var rec ShareArtifactRecord
	var hasImage int
	err := s.db.QueryRowContext(ctx, `SELECT
			token, viewer_pubkey, note_id, root_id, parent_id, surface, has_image, image_url, live_thread_url, payload_json, created_at, last_accessed_at
		FROM share_artifacts WHERE token = ?`, token).Scan(
		&rec.Token,
		&rec.ViewerPubkey,
		&rec.NoteID,
		&rec.RootID,
		&rec.ParentID,
		&rec.Surface,
		&hasImage,
		&rec.ImageURL,
		&rec.LiveThreadURL,
		&rec.PayloadJSON,
		&rec.CreatedAt,
		&rec.LastAccessedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	rec.HasImage = hasImage != 0
	return &rec, true, nil
}

func (s *Store) TouchShareArtifact(ctx context.Context, token string, lastAccessedAt int64) error {
	if s == nil || s.db == nil || token == "" || lastAccessedAt <= 0 {
		return nil
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	_, err := s.db.ExecContext(ctx, `UPDATE share_artifacts
		SET last_accessed_at = ?
		WHERE token = ? AND last_accessed_at < ?`, lastAccessedAt, token, lastAccessedAt)
	return err
}

func boolToInt(v bool) int {
	if v {
		return 1
	}
	return 0
}
