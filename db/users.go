package db

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

// AuthUser is a named team account on this instance.
type AuthUser struct {
	ID         int64
	Username   string
	Hash       string
	Role       string
	PwdVersion int64
	Disabled   bool
	CreatedAt  time.Time
}

var (
	ErrUserExists   = errors.New("username already exists")
	ErrUserNotFound = errors.New("user not found")
)

// GetAuthUserByName returns a team user by username (case-sensitive).
func (d *DB) GetAuthUserByName(username string) (*AuthUser, error) {
	u := &AuthUser{}
	err := d.QueryRow(`
SELECT id, username, password_hash, role, pwd_version, disabled, created_at
FROM auth_users WHERE username = $1`, username).Scan(
		&u.ID, &u.Username, &u.Hash, &u.Role, &u.PwdVersion, &u.Disabled, &u.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrUserNotFound
	}
	if err != nil {
		return nil, err
	}
	return u, nil
}

// GetAuthUserByID returns a team user by id.
func (d *DB) GetAuthUserByID(id int64) (*AuthUser, error) {
	u := &AuthUser{}
	err := d.QueryRow(`
SELECT id, username, password_hash, role, pwd_version, disabled, created_at
FROM auth_users WHERE id = $1`, id).Scan(
		&u.ID, &u.Username, &u.Hash, &u.Role, &u.PwdVersion, &u.Disabled, &u.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrUserNotFound
	}
	if err != nil {
		return nil, err
	}
	return u, nil
}

// ListAuthUsers returns all team users (hashes omitted from API by callers).
func (d *DB) ListAuthUsers() ([]AuthUser, error) {
	rows, err := d.Query(`
SELECT id, username, password_hash, role, pwd_version, disabled, created_at
FROM auth_users ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []AuthUser
	for rows.Next() {
		var u AuthUser
		if err := rows.Scan(&u.ID, &u.Username, &u.Hash, &u.Role, &u.PwdVersion, &u.Disabled, &u.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

// CreateAuthUser inserts a team member. username must be non-empty and not "ARTEX"
// (reserved for the legacy admin password in settings).
func (d *DB) CreateAuthUser(username, hash, role string) (*AuthUser, error) {
	username = strings.TrimSpace(username)
	if username == "" {
		return nil, fmt.Errorf("username required")
	}
	if strings.EqualFold(username, "ARTEX") {
		return nil, fmt.Errorf("username ARTEX is reserved for the bootstrap admin account")
	}
	switch role {
	case "admin", "operator", "viewer":
	default:
		return nil, fmt.Errorf("role must be admin|operator|viewer")
	}
	u := &AuthUser{}
	err := d.QueryRow(`
INSERT INTO auth_users(username, password_hash, role)
VALUES ($1, $2, $3)
RETURNING id, username, password_hash, role, pwd_version, disabled, created_at`,
		username, hash, role).Scan(
		&u.ID, &u.Username, &u.Hash, &u.Role, &u.PwdVersion, &u.Disabled, &u.CreatedAt)
	if err != nil {
		if strings.Contains(err.Error(), "auth_users_username_key") || strings.Contains(err.Error(), "duplicate key") {
			return nil, ErrUserExists
		}
		return nil, err
	}
	return u, nil
}

// RotateAuthUserPassword updates hash and bumps pwd_version atomically.
func (d *DB) RotateAuthUserPassword(id int64, hash string) (int64, error) {
	var version int64
	err := d.QueryRow(`
UPDATE auth_users
SET password_hash = $2, pwd_version = pwd_version + 1, updated_at = now()
WHERE id = $1
RETURNING pwd_version`, id, hash).Scan(&version)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, ErrUserNotFound
	}
	return version, err
}

// SetAuthUserDisabled toggles access without deleting audit history.
func (d *DB) SetAuthUserDisabled(id int64, disabled bool) error {
	res, err := d.Exec(`UPDATE auth_users SET disabled = $2, updated_at = now() WHERE id = $1`, id, disabled)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrUserNotFound
	}
	return nil
}
