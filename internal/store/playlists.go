package store

import (
	"context"
	"database/sql"
	"errors"
	"strconv"
	"strings"
	"time"
)

// Playlist is the database row behind an .m3u file. Schedules point at the
// id, so a rename does not break them.
type Playlist struct {
	ID        int64     `json:"id"`
	Name      string    `json:"name"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// ErrExists reports a name that is already in use.
var ErrExists = errors.New("name already exists")

// CreatePlaylist inserts a row and returns it.
func (s *Store) CreatePlaylist(ctx context.Context, name string) (Playlist, error) {
	now := time.Now().UTC()
	res, err := s.w.ExecContext(ctx, `INSERT INTO playlists (name, created_at, updated_at) VALUES (?, ?, ?)`, name, format(now), format(now))
	if err != nil {
		if isUnique(err) {
			return Playlist{}, ErrExists
		}
		return Playlist{}, err
	}
	id, _ := res.LastInsertId()
	return Playlist{ID: id, Name: name, CreatedAt: now, UpdatedAt: now}, nil
}

// ListPlaylists returns every playlist by name.
func (s *Store) ListPlaylists(ctx context.Context) ([]Playlist, error) {
	rows, err := s.r.QueryContext(ctx, `SELECT id, name, created_at, updated_at FROM playlists ORDER BY name COLLATE NOCASE`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Playlist{}
	for rows.Next() {
		var p Playlist
		var c, u string
		if err := rows.Scan(&p.ID, &p.Name, &c, &u); err != nil {
			return nil, err
		}
		p.CreatedAt, p.UpdatedAt = parse(c), parse(u)
		out = append(out, p)
	}
	return out, rows.Err()
}

// GetPlaylist returns one playlist.
func (s *Store) GetPlaylist(ctx context.Context, id int64) (Playlist, bool, error) {
	var p Playlist
	var c, u string
	err := s.r.QueryRowContext(ctx, `SELECT id, name, created_at, updated_at FROM playlists WHERE id = ?`, id).Scan(&p.ID, &p.Name, &c, &u)
	if errors.Is(err, sql.ErrNoRows) {
		return p, false, nil
	}
	if err != nil {
		return p, false, err
	}
	p.CreatedAt, p.UpdatedAt = parse(c), parse(u)
	return p, true, nil
}

// RenamePlaylist changes the name.
func (s *Store) RenamePlaylist(ctx context.Context, id int64, name string) error {
	_, err := s.w.ExecContext(ctx, `UPDATE playlists SET name = ?, updated_at = ? WHERE id = ?`, name, now(), id)
	if isUnique(err) {
		return ErrExists
	}
	return err
}

// TouchPlaylist records a change to the entries.
func (s *Store) TouchPlaylist(ctx context.Context, id int64) error {
	_, err := s.w.ExecContext(ctx, `UPDATE playlists SET updated_at = ? WHERE id = ?`, now(), id)
	return err
}

// DeletePlaylist removes the row. Schedules that point at it are deleted
// with it, because a rule without a source cannot play.
func (s *Store) DeletePlaylist(ctx context.Context, id int64) error {
	return s.Tx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `DELETE FROM schedules WHERE source_type = 'playlist' AND source_ref = ?`, itoa(id)); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM schedule_exceptions WHERE source_type = 'playlist' AND source_ref = ?`, itoa(id)); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `DELETE FROM playlists WHERE id = ?`, id)
		return err
	})
}

// isUnique reports a UNIQUE constraint failure.
func isUnique(err error) bool {
	return err != nil && strings.Contains(err.Error(), "UNIQUE constraint failed")
}

func itoa(n int64) string { return strconv.FormatInt(n, 10) }
