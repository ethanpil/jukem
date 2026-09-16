package store

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// Announcement is one file that plays instead of the music at its own
// times. The days use the same bits as Schedule.
type Announcement struct {
	ID           int64      `json:"id,omitempty" doc:"Set by the server"`
	Name         string     `json:"name" minLength:"1" maxLength:"100"`
	Enabled      bool       `json:"enabled"`
	Days         int        `json:"days" minimum:"1" maximum:"127" doc:"Bit mask, Monday is 1, Sunday is 64"`
	Mode         string     `json:"mode" enum:"at,every" doc:"at plays once a day at at_time. every plays from start_time to end_time, one play every every_minutes."`
	AtTime       *string    `json:"at_time,omitempty" pattern:"^([01][0-9]|2[0-3]):[0-5][0-9]$" doc:"Local wall-clock time HH:MM, for mode at"`
	StartTime    *string    `json:"start_time,omitempty" pattern:"^([01][0-9]|2[0-3]):[0-5][0-9]$" doc:"First play, for mode every"`
	EndTime      *string    `json:"end_time,omitempty" pattern:"^([01][0-9]|2[0-3]):[0-5][0-9]$" doc:"No play after this time, for mode every"`
	EveryMinutes *int       `json:"every_minutes,omitempty" minimum:"1" maximum:"1440" doc:"Minutes between plays, for mode every"`
	SourceKind   string     `json:"source_kind" enum:"file,random,cycle" doc:"file plays source_ref. random plays one file of the folder source_ref. cycle plays the next file of that folder each time."`
	SourceRef    string     `json:"source_ref" doc:"A file or a folder under the music root"`
	CycleIndex   int        `json:"-" doc:"Position in the folder, for cycle"`
	Volume       *int       `json:"volume,omitempty" minimum:"0" maximum:"100" doc:"Volume for the announcement. The music volume returns after it."`
	LastPlayed   *time.Time `json:"last_played,omitempty" doc:"When it played last"`
	CreatedAt    time.Time  `json:"created_at,omitempty"`
}

const announcementColumns = `id, name, enabled, days, mode, at_time, start_time, end_time, every_minutes,
	source_kind, source_ref, cycle_index, volume, last_played, created_at`

func scanAnnouncement(sc interface{ Scan(...any) error }) (Announcement, error) {
	var a Announcement
	var at, start, end, last sql.NullString
	var every, vol sql.NullInt64
	var created string
	err := sc.Scan(&a.ID, &a.Name, &a.Enabled, &a.Days, &a.Mode, &at, &start, &end, &every,
		&a.SourceKind, &a.SourceRef, &a.CycleIndex, &vol, &last, &created)
	if err != nil {
		return a, err
	}
	if at.Valid {
		a.AtTime = &at.String
	}
	if start.Valid {
		a.StartTime = &start.String
	}
	if end.Valid {
		a.EndTime = &end.String
	}
	if every.Valid {
		v := int(every.Int64)
		a.EveryMinutes = &v
	}
	if vol.Valid {
		v := int(vol.Int64)
		a.Volume = &v
	}
	if last.Valid {
		t := parse(last.String)
		a.LastPlayed = &t
	}
	a.CreatedAt = parse(created)
	return a, nil
}

// ListAnnouncements returns every announcement by name.
func (s *Store) ListAnnouncements(ctx context.Context) ([]Announcement, error) {
	rows, err := s.r.QueryContext(ctx, `SELECT `+announcementColumns+` FROM announcements ORDER BY name COLLATE NOCASE`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Announcement{}
	for rows.Next() {
		a, err := scanAnnouncement(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// GetAnnouncement returns one announcement.
func (s *Store) GetAnnouncement(ctx context.Context, id int64) (Announcement, bool, error) {
	row := s.r.QueryRowContext(ctx, `SELECT `+announcementColumns+` FROM announcements WHERE id = ?`, id)
	a, err := scanAnnouncement(row)
	if errors.Is(err, sql.ErrNoRows) {
		return a, false, nil
	}
	if err != nil {
		return a, false, err
	}
	return a, true, nil
}

// CreateAnnouncement inserts one and returns it with its id.
func (s *Store) CreateAnnouncement(ctx context.Context, a Announcement) (Announcement, error) {
	a.CreatedAt = time.Now().UTC()
	res, err := s.w.ExecContext(ctx, `INSERT INTO announcements
		(name, enabled, days, mode, at_time, start_time, end_time, every_minutes, source_kind, source_ref, cycle_index, volume, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 0, ?, ?)`,
		a.Name, a.Enabled, a.Days, a.Mode, nullString(a.AtTime), nullString(a.StartTime), nullString(a.EndTime),
		nullInt(a.EveryMinutes), a.SourceKind, a.SourceRef, nullInt(a.Volume), format(a.CreatedAt))
	if err != nil {
		return a, err
	}
	a.ID, err = res.LastInsertId()
	return a, err
}

// UpdateAnnouncement replaces one. The cycle position and the last play
// stay as they are.
func (s *Store) UpdateAnnouncement(ctx context.Context, a Announcement) error {
	_, err := s.w.ExecContext(ctx, `UPDATE announcements SET name = ?, enabled = ?, days = ?, mode = ?, at_time = ?,
		start_time = ?, end_time = ?, every_minutes = ?, source_kind = ?, source_ref = ?, volume = ? WHERE id = ?`,
		a.Name, a.Enabled, a.Days, a.Mode, nullString(a.AtTime), nullString(a.StartTime), nullString(a.EndTime),
		nullInt(a.EveryMinutes), a.SourceKind, a.SourceRef, nullInt(a.Volume), a.ID)
	return err
}

// DeleteAnnouncement removes one.
func (s *Store) DeleteAnnouncement(ctx context.Context, id int64) error {
	_, err := s.w.ExecContext(ctx, `DELETE FROM announcements WHERE id = ?`, id)
	return err
}

// MarkAnnouncementPlayed records the play time and the next position of a
// cycle, so a restart does not repeat the same file.
func (s *Store) MarkAnnouncementPlayed(ctx context.Context, id int64, at time.Time, cycleIndex int) error {
	_, err := s.w.ExecContext(ctx, `UPDATE announcements SET last_played = ?, cycle_index = ? WHERE id = ?`,
		format(at.UTC()), cycleIndex, id)
	return err
}

func nullString(s *string) any {
	if s == nil {
		return nil
	}
	return *s
}

func nullInt(v *int) any {
	if v == nil {
		return nil
	}
	return *v
}
