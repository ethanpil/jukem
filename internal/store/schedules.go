package store

import (
	"context"
	"database/sql"
	"errors"
	"strings"
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
	ID         int64  `json:"id"`
	Name       string `json:"name" minLength:"1" maxLength:"100"`
	Enabled    bool   `json:"enabled"`
	Days       int    `json:"days" minimum:"1" maximum:"127" doc:"Bit mask, Monday is 1, Sunday is 64. A day is the day the window starts."`
	StartTime  string `json:"start_time" pattern:"^[0-2][0-9]:[0-5][0-9]$" doc:"Local wall-clock time HH:MM"`
	EndTime    string `json:"end_time" pattern:"^[0-2][0-9]:[0-5][0-9]$" doc:"Local wall-clock time HH:MM. Earlier than the start means past midnight."`
	SourceType string `json:"source_type" enum:"directory,playlist"`
	SourceRef  string `json:"source_ref" doc:"A path under the music root, or a playlist id"`
	Shuffle    bool   `json:"shuffle"`
	Volume     *int   `json:"volume,omitempty" minimum:"0" maximum:"100" doc:"Applied at the window start, clamped to the limits"`
}

// Exception is a date that overrides the weekly rules.
type Exception struct {
	Date       string  `json:"date" pattern:"^[0-9]{4}-[0-9]{2}-[0-9]{2}$"`
	Kind       string  `json:"kind" enum:"silent,hours,source" doc:"silent: nothing plays. hours: the given hours with the given source. source: the normal hours with a different source."`
	Note       string  `json:"note,omitempty" maxLength:"200"`
	StartTime  *string `json:"start_time,omitempty" pattern:"^[0-2][0-9]:[0-5][0-9]$"`
	EndTime    *string `json:"end_time,omitempty" pattern:"^[0-2][0-9]:[0-5][0-9]$"`
	SourceType *string `json:"source_type,omitempty" enum:"directory,playlist"`
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

func volArg(v *int) any {
	if v == nil {
		return nil
	}
	return *v
}

// CreateSchedule inserts a rule and returns its id.
func (s *Store) CreateSchedule(ctx context.Context, r Schedule) (int64, error) {
	res, err := s.w.ExecContext(ctx, `INSERT INTO schedules (name, enabled, days, start_time, end_time, source_type, source_ref, shuffle, volume, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		r.Name, r.Enabled, r.Days, r.StartTime, r.EndTime, r.SourceType, r.SourceRef, r.Shuffle, volArg(r.Volume), now(), now())
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// UpdateSchedule replaces a rule. It reports false when the id is unknown.
func (s *Store) UpdateSchedule(ctx context.Context, r Schedule) (bool, error) {
	res, err := s.w.ExecContext(ctx, `UPDATE schedules SET name = ?, enabled = ?, days = ?, start_time = ?, end_time = ?, source_type = ?, source_ref = ?, shuffle = ?, volume = ?, updated_at = ? WHERE id = ?`,
		r.Name, r.Enabled, r.Days, r.StartTime, r.EndTime, r.SourceType, r.SourceRef, r.Shuffle, volArg(r.Volume), now(), r.ID)
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
	q := `SELECT date, kind, note, start_time, end_time, source_type, source_ref, shuffle, volume FROM schedule_exceptions`
	var args []any
	if from != "" || to != "" {
		q += ` WHERE date >= ? AND date <= ?`
		args = append(args, from, to)
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
	var shuffle any
	if e.Shuffle != nil {
		shuffle = *e.Shuffle
	}
	_, err := s.w.ExecContext(ctx, `INSERT INTO schedule_exceptions (date, kind, note, start_time, end_time, source_type, source_ref, shuffle, volume)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(date) DO UPDATE SET kind = excluded.kind, note = excluded.note, start_time = excluded.start_time, end_time = excluded.end_time,
		source_type = excluded.source_type, source_ref = excluded.source_ref, shuffle = excluded.shuffle, volume = excluded.volume`,
		e.Date, e.Kind, e.Note, strArg(e.StartTime), strArg(e.EndTime), strArg(e.SourceType), strArg(e.SourceRef), shuffle, volArg(e.Volume))
	return err
}

func strArg(v *string) any {
	if v == nil {
		return nil
	}
	return *v
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

// RepointDirectory updates rules and exceptions whose directory source is
// old or below it, after a move. It returns the number of rows changed.
func (s *Store) RepointDirectory(ctx context.Context, old, new string) (int64, error) {
	var total int64
	err := s.Tx(ctx, func(tx *sql.Tx) error {
		for _, table := range []string{"schedules", "schedule_exceptions"} {
			res, err := tx.ExecContext(ctx, `UPDATE `+table+` SET source_ref = ? || substr(source_ref, ?) WHERE source_type = 'directory' AND (source_ref = ? OR source_ref LIKE ? ESCAPE '\')`,
				new, len(old)+1, old, escapeLike(old)+"/%")
			if err != nil {
				return err
			}
			n, _ := res.RowsAffected()
			total += n
		}
		return nil
	})
	return total, err
}

// DirectoryReferences returns the names of rules and exceptions whose
// directory source is path or below it.
func (s *Store) DirectoryReferences(ctx context.Context, path string) ([]string, error) {
	rows, err := s.r.QueryContext(ctx, `SELECT name FROM schedules WHERE source_type = 'directory' AND (source_ref = ? OR source_ref LIKE ? ESCAPE '\')
		UNION ALL SELECT 'Exception ' || date FROM schedule_exceptions WHERE source_type = 'directory' AND (source_ref = ? OR source_ref LIKE ? ESCAPE '\')`,
		path, escapeLike(path)+"/%", path, escapeLike(path)+"/%")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var names []string
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			return nil, err
		}
		names = append(names, n)
	}
	return names, rows.Err()
}

func escapeLike(s string) string {
	r := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)
	return r.Replace(s)
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
	rows, err := s.r.QueryContext(ctx, `SELECT file, title, added_at FROM do_not_play ORDER BY added_at DESC`)
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

// MoveDoNotPlay follows a moved file or folder.
func (s *Store) MoveDoNotPlay(ctx context.Context, old, new string, isDir bool) error {
	if !isDir {
		_, err := s.w.ExecContext(ctx, `UPDATE do_not_play SET file = ? WHERE file = ?`, new, old)
		return err
	}
	_, err := s.w.ExecContext(ctx, `UPDATE do_not_play SET file = ? || substr(file, ?) WHERE file LIKE ? ESCAPE '\'`, new, len(old)+1, escapeLike(old)+"/%")
	return err
}

// DeleteDoNotPlayUnder removes entries for a deleted file or folder.
func (s *Store) DeleteDoNotPlayUnder(ctx context.Context, path string) error {
	_, err := s.w.ExecContext(ctx, `DELETE FROM do_not_play WHERE file = ? OR file LIKE ? ESCAPE '\'`, path, escapeLike(path)+"/%")
	return err
}
