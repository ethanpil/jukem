package store

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// Weekday bits for Schedule.Days. Monday is bit 0.
const (
	Monday = 1 << iota
	Tuesday
	Wednesday
	Thursday
	Friday
	Saturday
	Sunday
	AllDays = Monday | Tuesday | Wednesday | Thursday | Friday | Saturday | Sunday
)

// Schedule is one weekly rule.
type Schedule struct {
	ID         int64  `json:"id,omitempty" doc:"Set by the server"`
	Name       string `json:"name" minLength:"1" maxLength:"100"`
	Enabled    bool   `json:"enabled"`
	Days       int    `json:"days" minimum:"1" maximum:"127" doc:"Bit mask, Monday is 1, Sunday is 64. A day is the day the window starts."`
	StartTime  string `json:"start_time" pattern:"^([01][0-9]|2[0-3]):[0-5][0-9]$" doc:"Local wall-clock time HH:MM"`
	EndTime    string `json:"end_time" pattern:"^([01][0-9]|2[0-3]):[0-5][0-9]$" doc:"Local wall-clock time HH:MM. Earlier than the start means past midnight."`
	SourceType string `json:"source_type" enum:"directory,playlist,stream"`
	SourceRef  string `json:"source_ref" doc:"A path under the music root, a playlist id, or the address of a stream"`
	Shuffle    bool   `json:"shuffle"`
	Volume     *int   `json:"volume,omitempty" minimum:"0" maximum:"100" doc:"Applied at the window start, clamped to the limits"`
}

// Exception is a date that overrides the weekly rules.
type Exception struct {
	Date       string  `json:"date,omitempty" pattern:"^[0-9]{4}-[0-9]{2}-[0-9]{2}$" doc:"Required on create; the path names it on update"`
	Kind       string  `json:"kind" enum:"silent,hours,source" doc:"With silent, nothing plays that day. With hours, the given hours play the given source. With source, the normal hours play a different source."`
	Note       string  `json:"note,omitempty" maxLength:"200"`
	StartTime  *string `json:"start_time,omitempty" pattern:"^([01][0-9]|2[0-3]):[0-5][0-9]$"`
	EndTime    *string `json:"end_time,omitempty" pattern:"^([01][0-9]|2[0-3]):[0-5][0-9]$"`
	SourceType *string `json:"source_type,omitempty" enum:"directory,playlist,stream"`
	SourceRef  *string `json:"source_ref,omitempty"`
	Shuffle    *bool   `json:"shuffle,omitempty"`
	Volume     *int    `json:"volume,omitempty" minimum:"0" maximum:"100"`
}

// ListSchedules returns every rule by name.
func (s *Store) ListSchedules(ctx context.Context) ([]Schedule, error) {
	rows, err := s.r.QueryContext(ctx, `SELECT id, name, enabled, days, start_time, end_time, source_type, source_ref, shuffle, volume FROM schedules ORDER BY start_time, name COLLATE NOCASE`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Schedule{}
	for rows.Next() {
		var r Schedule
		var vol sql.NullInt64
		if err := rows.Scan(&r.ID, &r.Name, &r.Enabled, &r.Days, &r.StartTime, &r.EndTime, &r.SourceType, &r.SourceRef, &r.Shuffle, &vol); err != nil {
			return nil, err
		}
		if vol.Valid {
			v := int(vol.Int64)
			r.Volume = &v
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// GetSchedule returns one rule.
func (s *Store) GetSchedule(ctx context.Context, id int64) (Schedule, bool, error) {
	var r Schedule
	var vol sql.NullInt64
	err := s.r.QueryRowContext(ctx, `SELECT id, name, enabled, days, start_time, end_time, source_type, source_ref, shuffle, volume FROM schedules WHERE id = ?`, id).
		Scan(&r.ID, &r.Name, &r.Enabled, &r.Days, &r.StartTime, &r.EndTime, &r.SourceType, &r.SourceRef, &r.Shuffle, &vol)
	if errors.Is(err, sql.ErrNoRows) {
		return r, false, nil
	}
	if vol.Valid {
		v := int(vol.Int64)
		r.Volume = &v
	}
	return r, err == nil, err
}

// arg turns an optional value into a SQL argument, nil when unset.
func arg[T any](v *T) any {
	if v == nil {
		return nil
	}
	return *v
}

// CreateSchedule inserts a rule and returns its id.
func (s *Store) CreateSchedule(ctx context.Context, r Schedule) (int64, error) {
	res, err := s.w.ExecContext(ctx, `INSERT INTO schedules (name, enabled, days, start_time, end_time, source_type, source_ref, shuffle, volume, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		r.Name, r.Enabled, r.Days, r.StartTime, r.EndTime, r.SourceType, r.SourceRef, r.Shuffle, arg(r.Volume), now(), now())
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// UpdateSchedule replaces a rule. It reports false when the id is unknown.
func (s *Store) UpdateSchedule(ctx context.Context, r Schedule) (bool, error) {
	res, err := s.w.ExecContext(ctx, `UPDATE schedules SET name = ?, enabled = ?, days = ?, start_time = ?, end_time = ?, source_type = ?, source_ref = ?, shuffle = ?, volume = ?, updated_at = ? WHERE id = ?`,
		r.Name, r.Enabled, r.Days, r.StartTime, r.EndTime, r.SourceType, r.SourceRef, r.Shuffle, arg(r.Volume), now(), r.ID)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

// DeleteSchedule removes a rule.
func (s *Store) DeleteSchedule(ctx context.Context, id int64) (bool, error) {
	res, err := s.w.ExecContext(ctx, `DELETE FROM schedules WHERE id = ?`, id)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

// ListExceptions returns the exceptions between two dates inclusive, or
// all when both are empty.
func (s *Store) ListExceptions(ctx context.Context, from, to string) ([]Exception, error) {
	q := `SELECT date, kind, note, start_time, end_time, source_type, source_ref, shuffle, volume FROM schedule_exceptions WHERE 1 = 1`
	var args []any
	if from != "" {
		q += ` AND date >= ?`
		args = append(args, from)
	}
	if to != "" {
		q += ` AND date <= ?`
		args = append(args, to)
	}
	rows, err := s.r.QueryContext(ctx, q+` ORDER BY date`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Exception{}
	for rows.Next() {
		e, err := scanException(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// GetException returns the exception for a date.
func (s *Store) GetException(ctx context.Context, date string) (Exception, bool, error) {
	row := s.r.QueryRowContext(ctx, `SELECT date, kind, note, start_time, end_time, source_type, source_ref, shuffle, volume FROM schedule_exceptions WHERE date = ?`, date)
	e, err := scanException(row)
	if errors.Is(err, sql.ErrNoRows) {
		return e, false, nil
	}
	return e, err == nil, err
}

func scanException(row interface{ Scan(dest ...any) error }) (Exception, error) {
	var e Exception
	var start, end, st, sr sql.NullString
	var shuffle sql.NullBool
	var vol sql.NullInt64
	if err := row.Scan(&e.Date, &e.Kind, &e.Note, &start, &end, &st, &sr, &shuffle, &vol); err != nil {
		return e, err
	}
	if start.Valid {
		e.StartTime = &start.String
	}
	if end.Valid {
		e.EndTime = &end.String
	}
	if st.Valid {
		e.SourceType = &st.String
	}
	if sr.Valid {
		e.SourceRef = &sr.String
	}
	if shuffle.Valid {
		e.Shuffle = &shuffle.Bool
	}
	if vol.Valid {
		v := int(vol.Int64)
		e.Volume = &v
	}
	return e, nil
}

// PutException inserts or replaces the exception for a date.
func (s *Store) PutException(ctx context.Context, e Exception) error {
	_, err := s.w.ExecContext(ctx, `INSERT INTO schedule_exceptions (date, kind, note, start_time, end_time, source_type, source_ref, shuffle, volume)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(date) DO UPDATE SET kind = excluded.kind, note = excluded.note, start_time = excluded.start_time, end_time = excluded.end_time,
		source_type = excluded.source_type, source_ref = excluded.source_ref, shuffle = excluded.shuffle, volume = excluded.volume`,
		e.Date, e.Kind, e.Note, arg(e.StartTime), arg(e.EndTime), arg(e.SourceType), arg(e.SourceRef), arg(e.Shuffle), arg(e.Volume))
	return err
}

// DeleteException removes the exception for a date.
func (s *Store) DeleteException(ctx context.Context, date string) (bool, error) {
	res, err := s.w.ExecContext(ctx, `DELETE FROM schedule_exceptions WHERE date = ?`, date)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

// DoNotPlayEntry is a track kept out of scheduled playback.
type DoNotPlayEntry struct {
	File    string    `json:"file"`
	Title   string    `json:"title,omitempty"`
	AddedAt time.Time `json:"added_at"`
}

// AddDoNotPlay records a track.
func (s *Store) AddDoNotPlay(ctx context.Context, file, title string) error {
	_, err := s.w.ExecContext(ctx, `INSERT INTO do_not_play (file, title, added_at) VALUES (?, ?, ?) ON CONFLICT(file) DO UPDATE SET title = excluded.title`, file, title, now())
	return err
}

// RemoveDoNotPlay forgets a track.
func (s *Store) RemoveDoNotPlay(ctx context.Context, file string) (bool, error) {
	res, err := s.w.ExecContext(ctx, `DELETE FROM do_not_play WHERE file = ?`, file)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

// ListDoNotPlay returns every entry, newest first.
func (s *Store) ListDoNotPlay(ctx context.Context) ([]DoNotPlayEntry, error) {
	rows, err := s.r.QueryContext(ctx, `SELECT file, title, added_at FROM do_not_play ORDER BY added_at DESC, file`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []DoNotPlayEntry{}
	for rows.Next() {
		var e DoNotPlayEntry
		var at string
		if err := rows.Scan(&e.File, &e.Title, &at); err != nil {
			return nil, err
		}
		e.AddedAt = parse(at)
		out = append(out, e)
	}
	return out, rows.Err()
}

// DoNotPlaySet returns the files as a set.
func (s *Store) DoNotPlaySet(ctx context.Context) (map[string]bool, error) {
	list, err := s.ListDoNotPlay(ctx)
	if err != nil {
		return nil, err
	}
	set := make(map[string]bool, len(list))
	for _, e := range list {
		set[e.File] = true
	}
	return set, nil
}
