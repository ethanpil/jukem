package store

import (
	"context"
	"database/sql"
	"errors"
)

// DeviceMixer is the remembered hardware level of one output device.
type DeviceMixer struct {
	DeviceKey string `json:"device_key" doc:"Stable device key"`
	Control   string `json:"control" doc:"ALSA control name"`
	Level     int    `json:"level" minimum:"0" maximum:"100" doc:"Level in percent"`
	Reapply   bool   `json:"reapply" doc:"Apply again each time MPD starts"`
}

// GetDeviceMixer returns the remembered level for a device key.
func (s *Store) GetDeviceMixer(ctx context.Context, key string) (DeviceMixer, bool, error) {
	var m DeviceMixer
	err := s.r.QueryRowContext(ctx, `SELECT device_key, control, level, reapply FROM device_mixer WHERE device_key = ?`, key).
		Scan(&m.DeviceKey, &m.Control, &m.Level, &m.Reapply)
	if errors.Is(err, sql.ErrNoRows) {
		return m, false, nil
	}
	return m, err == nil, err
}

// SetDeviceMixer stores the level for a device key.
func (s *Store) SetDeviceMixer(ctx context.Context, m DeviceMixer) error {
	_, err := s.w.ExecContext(ctx, `INSERT INTO device_mixer (device_key, control, level, reapply) VALUES (?, ?, ?, ?)
		ON CONFLICT(device_key) DO UPDATE SET control = excluded.control, level = excluded.level, reapply = excluded.reapply`,
		m.DeviceKey, m.Control, m.Level, m.Reapply)
	return err
}

// DeleteDeviceMixer forgets the level for a device key.
func (s *Store) DeleteDeviceMixer(ctx context.Context, key string) error {
	_, err := s.w.ExecContext(ctx, `DELETE FROM device_mixer WHERE device_key = ?`, key)
	return err
}
