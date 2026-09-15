package store

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// PasswordHash returns the stored login hash. ok is false before setup.
func (s *Store) PasswordHash(ctx context.Context) (hash string, ok bool, err error) {
	err = s.r.QueryRowContext(ctx, `SELECT password_hash FROM login WHERE id = 1`).Scan(&hash)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	return hash, err == nil, err
}

// SetPasswordHash stores the login hash and signs out every session.
func (s *Store) SetPasswordHash(ctx context.Context, hash string) error {
	return s.Tx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `INSERT INTO login (id, password_hash, updated_at) VALUES (1, ?, ?)
			ON CONFLICT(id) DO UPDATE SET password_hash = excluded.password_hash, updated_at = excluded.updated_at`,
			hash, now()); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `DELETE FROM sessions`)
		return err
	})
}

// Session is one signed-in browser.
type Session struct {
	ID        string
	CSRFToken string
	CreatedAt time.Time
	LastSeen  time.Time
	ExpiresAt time.Time
	IP        string
	UserAgent string
}

// CreateSession stores a new session.
func (s *Store) CreateSession(ctx context.Context, se Session) error {
	_, err := s.w.ExecContext(ctx, `INSERT INTO sessions (id, csrf_token, created_at, last_seen_at, expires_at, ip, user_agent)
		VALUES (?, ?, ?, ?, ?, ?, ?)`,
		se.ID, se.CSRFToken, format(se.CreatedAt), format(se.LastSeen), format(se.ExpiresAt), se.IP, se.UserAgent)
	return err
}

// GetSession returns a session that has not expired.
func (s *Store) GetSession(ctx context.Context, id string) (Session, bool, error) {
	var se Session
	var created, seen, expires string
	err := s.r.QueryRowContext(ctx, `SELECT id, csrf_token, created_at, last_seen_at, expires_at, ip, user_agent
		FROM sessions WHERE id = ? AND expires_at > ?`, id, now()).
		Scan(&se.ID, &se.CSRFToken, &created, &seen, &expires, &se.IP, &se.UserAgent)
	if errors.Is(err, sql.ErrNoRows) {
		return se, false, nil
	}
	if err != nil {
		return se, false, err
	}
	se.CreatedAt, se.LastSeen, se.ExpiresAt = parse(created), parse(seen), parse(expires)
	return se, true, nil
}

// TouchSession records activity and extends the expiry.
func (s *Store) TouchSession(ctx context.Context, id string, expiresAt time.Time) error {
	_, err := s.w.ExecContext(ctx, `UPDATE sessions SET last_seen_at = ?, expires_at = ? WHERE id = ?`, now(), format(expiresAt), id)
	return err
}

// DeleteSession signs out one session.
func (s *Store) DeleteSession(ctx context.Context, id string) error {
	_, err := s.w.ExecContext(ctx, `DELETE FROM sessions WHERE id = ?`, id)
	return err
}

// PruneSessions removes expired sessions.
func (s *Store) PruneSessions(ctx context.Context) error {
	_, err := s.w.ExecContext(ctx, `DELETE FROM sessions WHERE expires_at <= ?`, now())
	return err
}

// APIKey is a key for programs. The key itself is shown once; only its
// hash is stored.
type APIKey struct {
	ID         int64      `json:"id"`
	Name       string     `json:"name"`
	CreatedAt  time.Time  `json:"created_at"`
	LastUsedAt *time.Time `json:"last_used_at,omitempty"`
	LastUsedIP string     `json:"last_used_ip,omitempty"`
	ExpiresAt  *time.Time `json:"expires_at,omitempty"`
}

// CreateAPIKey stores a key hash and returns the new id.
func (s *Store) CreateAPIKey(ctx context.Context, name, hash string, expiresAt *time.Time) (int64, error) {
	var exp any
	if expiresAt != nil {
		exp = format(*expiresAt)
	}
	res, err := s.w.ExecContext(ctx, `INSERT INTO api_keys (name, key_hash, created_at, expires_at) VALUES (?, ?, ?, ?)`,
		name, hash, now(), exp)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// ListAPIKeys returns every key, newest first.
func (s *Store) ListAPIKeys(ctx context.Context) ([]APIKey, error) {
	rows, err := s.r.QueryContext(ctx, `SELECT id, name, created_at, last_used_at, last_used_ip, expires_at FROM api_keys ORDER BY id DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	keys := []APIKey{}
	for rows.Next() {
		k, err := scanAPIKey(rows)
		if err != nil {
			return nil, err
		}
		keys = append(keys, k)
	}
	return keys, rows.Err()
}

// APIKeyByHash returns the key with this hash when it has not expired.
func (s *Store) APIKeyByHash(ctx context.Context, hash string) (APIKey, bool, error) {
	row := s.r.QueryRowContext(ctx, `SELECT id, name, created_at, last_used_at, last_used_ip, expires_at FROM api_keys
		WHERE key_hash = ? AND (expires_at IS NULL OR expires_at > ?)`, hash, now())
	k, err := scanAPIKey(row)
	if errors.Is(err, sql.ErrNoRows) {
		return k, false, nil
	}
	return k, err == nil, err
}

// TouchAPIKey records the last use.
func (s *Store) TouchAPIKey(ctx context.Context, id int64, ip string) error {
	_, err := s.w.ExecContext(ctx, `UPDATE api_keys SET last_used_at = ?, last_used_ip = ? WHERE id = ?`, now(), ip, id)
	return err
}

// DeleteAPIKey revokes a key. It reports false when no key had that id.
func (s *Store) DeleteAPIKey(ctx context.Context, id int64) (bool, error) {
	res, err := s.w.ExecContext(ctx, `DELETE FROM api_keys WHERE id = ?`, id)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

type scanner interface {
	Scan(dest ...any) error
}

func scanAPIKey(row scanner) (APIKey, error) {
	var k APIKey
	var created string
	var lastUsed, expires sql.NullString
	if err := row.Scan(&k.ID, &k.Name, &created, &lastUsed, &k.LastUsedIP, &expires); err != nil {
		return k, err
	}
	k.CreatedAt = parse(created)
	if lastUsed.Valid {
		t := parse(lastUsed.String)
		k.LastUsedAt = &t
	}
	if expires.Valid {
		t := parse(expires.String)
		k.ExpiresAt = &t
	}
	return k, nil
}

// now returns the current time in the stored format. RFC 3339 in UTC sorts
// as text, which the expiry comparisons rely on.
func now() string { return format(time.Now()) }

func format(t time.Time) string { return t.UTC().Format(time.RFC3339) }

func parse(s string) time.Time {
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return time.Time{}
	}
	return t
}
