package store

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// HistoryRow is one track start.
type HistoryRow struct {
	ID        int64     `json:"id"`
	StartedAt time.Time `json:"started_at"`
	File      string    `json:"file"`
	Title     string    `json:"title,omitempty"`
	Artist    string    `json:"artist,omitempty"`
	Album     string    `json:"album,omitempty"`
	Source    string    `json:"source,omitempty" doc:"The program or the person that put the track on"`
}

// AddHistory records a track start.
func (s *Store) AddHistory(ctx context.Context, r HistoryRow) error {
	_, err := s.w.ExecContext(ctx, `INSERT INTO history (started_at, file, title, artist, album, source) VALUES (?, ?, ?, ?, ?, ?)`,
		format(r.StartedAt), r.File, r.Title, r.Artist, r.Album, r.Source)
	return err
}

// ListHistory returns up to limit rows that started before the given
// time, newest first. A zero time means now.
func (s *Store) ListHistory(ctx context.Context, before time.Time, limit int) ([]HistoryRow, error) {
	if before.IsZero() {
		before = time.Now().Add(time.Hour)
	}
	rows, err := s.r.QueryContext(ctx, `SELECT id, started_at, file, title, artist, album, source FROM history WHERE started_at < ? ORDER BY started_at DESC, id DESC LIMIT ?`, format(before), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []HistoryRow{}
	for rows.Next() {
		var r HistoryRow
		var started string
		if err := rows.Scan(&r.ID, &started, &r.File, &r.Title, &r.Artist, &r.Album, &r.Source); err != nil {
			return nil, err
		}
		r.StartedAt = parse(started)
		out = append(out, r)
	}
	return out, rows.Err()
}

// LastHistoryAt returns the start of the newest row.
func (s *Store) LastHistoryAt(ctx context.Context) (time.Time, bool, error) {
	var started string
	err := s.r.QueryRowContext(ctx, `SELECT started_at FROM history ORDER BY started_at DESC, id DESC LIMIT 1`).Scan(&started)
	if errors.Is(err, sql.ErrNoRows) {
		return time.Time{}, false, nil
	}
	if err != nil {
		return time.Time{}, false, err
	}
	return parse(started), true, nil
}

// TrimHistory keeps the newest rows within both limits: days of age and
// a row count.
func (s *Store) TrimHistory(ctx context.Context, days, rows int) error {
	cutoff := format(time.Now().AddDate(0, 0, -days))
	if _, err := s.w.ExecContext(ctx, `DELETE FROM history WHERE started_at < ?`, cutoff); err != nil {
		return err
	}
	_, err := s.w.ExecContext(ctx, `DELETE FROM history WHERE id NOT IN (SELECT id FROM history ORDER BY started_at DESC, id DESC LIMIT ?)`, rows)
	return err
}
