package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
)

// GetState reads a JSON value from the state table into v. It returns false
// when the key does not exist.
func (s *Store) GetState(ctx context.Context, key string, v any) (bool, error) {
	var raw string
	err := s.r.QueryRowContext(ctx, `SELECT value FROM state WHERE key = ?`, key).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, json.Unmarshal([]byte(raw), v)
}

// SetState writes v as JSON under key.
func (s *Store) SetState(ctx context.Context, key string, v any) error {
	raw, err := json.Marshal(v)
	if err != nil {
		return err
	}
	_, err = s.w.ExecContext(ctx,
		`INSERT INTO state (key, value) VALUES (?, ?) ON CONFLICT(key) DO UPDATE SET value = excluded.value`,
		key, string(raw))
	return err
}

// DeleteState removes key.
func (s *Store) DeleteState(ctx context.Context, key string) error {
	_, err := s.w.ExecContext(ctx, `DELETE FROM state WHERE key = ?`, key)
	return err
}
