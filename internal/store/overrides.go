package store

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// Override is a person's decision that outranks the schedule until it
// ends. Mode says how it ends; EndsAt only applies to timed overrides.
type Override struct {
	Mode       string     `json:"mode" enum:"until_next,timed,play_now"`
	Intent     string     `json:"intent" enum:"play,pause,stop" doc:"What the person asked for"`
	EndsAt     *time.Time `json:"ends_at,omitempty" doc:"For timed overrides, the latest end"`
	Source     string     `json:"source" doc:"Who created it: the web UI or an API key's name"`
	Generation int64      `json:"-"`
	CreatedAt  time.Time  `json:"created_at"`
}

// GetOverride returns the active override, if any.
func (s *Store) GetOverride(ctx context.Context) (Override, bool, error) {
	var o Override
	var ends sql.NullString
	var created string
	err := s.r.QueryRowContext(ctx, `SELECT mode, intent, ends_at, source, queue_generation, created_at FROM overrides WHERE id = 1`).
		Scan(&o.Mode, &o.Intent, &ends, &o.Source, &o.Generation, &created)
	if errors.Is(err, sql.ErrNoRows) {
		return o, false, nil
	}
	if err != nil {
		return o, false, err
	}
	if ends.Valid {
		t := parse(ends.String)
		o.EndsAt = &t
	}
	o.CreatedAt = parse(created)
	return o, true, nil
}

// SetOverride replaces the active override.
func (s *Store) SetOverride(ctx context.Context, o Override) error {
	var ends any
	if o.EndsAt != nil {
		ends = format(*o.EndsAt)
	}
	_, err := s.w.ExecContext(ctx, `INSERT INTO overrides (id, mode, intent, ends_at, source, queue_generation, created_at) VALUES (1, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET mode = excluded.mode, intent = excluded.intent, ends_at = excluded.ends_at, source = excluded.source,
		queue_generation = excluded.queue_generation, created_at = excluded.created_at`,
		o.Mode, o.Intent, ends, o.Source, o.Generation, format(o.CreatedAt))
	return err
}

// ClearOverride removes the active override.
func (s *Store) ClearOverride(ctx context.Context) error {
	_, err := s.w.ExecContext(ctx, `DELETE FROM overrides WHERE id = 1`)
	return err
}
