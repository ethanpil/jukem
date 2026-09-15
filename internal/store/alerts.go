package store

import (
	"context"
	"database/sql"
	"time"
)

// Alert is a problem the watchdog noticed. One active alert exists per
// kind; a repeat raises its count and updates the message.
type Alert struct {
	ID          int64      `json:"id"`
	Kind        string     `json:"kind" doc:"Stable identifier, for example dead_air or disk_full"`
	Message     string     `json:"message" doc:"What is wrong, in plain language"`
	Fix         string     `json:"fix,omitempty" doc:"What to do"`
	RaisedAt    time.Time  `json:"raised_at"`
	UpdatedAt   time.Time  `json:"updated_at" doc:"Last time the problem was seen"`
	DismissedAt *time.Time `json:"dismissed_at,omitempty"`
	Count       int        `json:"count" doc:"How many times the problem was seen"`
}

const alertColumns = `id, kind, message, fix, raised_at, updated_at, dismissed_at, count`

// RaiseAlert records a problem. It returns the alert and whether it is
// new, so the caller sends a notification once per problem. The partial
// unique index on active kinds makes this one upsert.
func (s *Store) RaiseAlert(ctx context.Context, kind, message, fix string) (Alert, bool, error) {
	t := now()
	a, err := scanAlert(s.w.QueryRowContext(ctx, `INSERT INTO alerts (kind, message, fix, raised_at, updated_at) VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(kind) WHERE dismissed_at IS NULL DO UPDATE SET message = excluded.message, fix = excluded.fix, updated_at = excluded.updated_at, count = count + 1
		RETURNING `+alertColumns, kind, message, fix, t, t))
	return a, a.Count == 1, err
}

// ResolveAlert ends the active alert of a kind, because the problem is
// gone. It reports whether one was active.
func (s *Store) ResolveAlert(ctx context.Context, kind string) (bool, error) {
	res, err := s.w.ExecContext(ctx, `UPDATE alerts SET dismissed_at = ? WHERE kind = ? AND dismissed_at IS NULL`, now(), kind)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

// DismissAlert ends an alert by id, at a person's request.
func (s *Store) DismissAlert(ctx context.Context, id int64) (bool, error) {
	res, err := s.w.ExecContext(ctx, `UPDATE alerts SET dismissed_at = ? WHERE id = ? AND dismissed_at IS NULL`, now(), id)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

// ActiveAlerts lists the alerts nobody dismissed, newest first.
func (s *Store) ActiveAlerts(ctx context.Context) ([]Alert, error) {
	rows, err := s.r.QueryContext(ctx, `SELECT `+alertColumns+` FROM alerts WHERE dismissed_at IS NULL ORDER BY raised_at DESC, id DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Alert{}
	for rows.Next() {
		a, err := scanAlert(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// TrimAlerts removes dismissed alerts older than days.
func (s *Store) TrimAlerts(ctx context.Context, days int) error {
	cutoff := format(time.Now().AddDate(0, 0, -days))
	_, err := s.w.ExecContext(ctx, `DELETE FROM alerts WHERE dismissed_at IS NOT NULL AND dismissed_at < ?`, cutoff)
	return err
}

func scanAlert(row interface{ Scan(dest ...any) error }) (Alert, error) {
	var a Alert
	var raised, updated string
	var dismissed sql.NullString
	if err := row.Scan(&a.ID, &a.Kind, &a.Message, &a.Fix, &raised, &updated, &dismissed, &a.Count); err != nil {
		return a, err
	}
	a.RaisedAt, a.UpdatedAt = parse(raised), parse(updated)
	if dismissed.Valid {
		t := parse(dismissed.String)
		a.DismissedAt = &t
	}
	return a, nil
}
