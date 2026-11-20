package db

import (
	"database/sql"
	"errors"
	"strings"
	"time"
	"unicode/utf8"
)

// =====================================================================
// Engagement entity layer
// =====================================================================

// MaxEngagementNameRunes bounds the engagement name like task categories.
const MaxEngagementNameRunes = 80

// Engagement is a row in the engagements table: an optional top-level grouping
// for tasks (client work, CTF events, research lines). Findings and stats
// derive from the engagement's tasks; nothing else is stored per engagement.
type Engagement struct {
	ID          int64     `json:"id"`
	Name        string    `json:"name"`
	Description string    `json:"description"`
	// CompanyID is the legacy company binding; nil = unbound. Traffic scoping
	// resolves the engagement's scope domains through it (see EngagementScopeDomains).
	CompanyID  *int64    `json:"company_id,omitempty"`
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`
}

// EngagementStats aggregates the engagement's live tasks and their findings.
type EngagementStats struct {
	TasksTotal   int                   `json:"tasks_total"`
	TasksRunning int                   `json:"tasks_running"` // running or queued
	TasksDone    int                   `json:"tasks_done"`
	TasksFailed  int                   `json:"tasks_failed"` // failed or timeout
	Findings     FindingSeverityCounts `json:"findings"`
}

// EngagementWithStats is one list row.
type EngagementWithStats struct {
	Engagement
	Stats EngagementStats `json:"stats"`
}

// EngagementStore operates on the engagements table.
type EngagementStore struct{ db *DB }

var (
	ErrEngagementInvalid      = errors.New("invalid engagement")
	ErrEngagementNameConflict = errors.New("engagement name already exists")
	ErrEngagementNotFound     = errors.New("engagement not found")
	ErrEngagementTaskNotFound = errors.New("task not found")
	ErrEngagementNameTooLong  = errors.New("engagement name exceeds 80 characters")
	ErrEngagementDescTooLong  = errors.New("engagement description exceeds 2000 characters")
	MaxEngagementDescRunes    = 2000
	engagementUniqueViolation = taskCategoryUniqueViolation // same 23505 class
)

// Engagements returns the engagement store.
func (d *DB) Engagements() *EngagementStore { return &EngagementStore{db: d} }

// DB-level wrappers: handlers use *DB directly, same as task categories.

// ListEngagements returns every engagement with stats.
func (d *DB) ListEngagements() ([]EngagementWithStats, error) {
	return d.Engagements().ListEngagements()
}

// CreateEngagement inserts a new engagement.
func (d *DB) CreateEngagement(name, description string) (*Engagement, error) {
	return d.Engagements().CreateEngagement(name, description)
}

// UpdateEngagement edits name/description.
func (d *DB) UpdateEngagement(id int64, name, description string) (*Engagement, error) {
	return d.Engagements().UpdateEngagement(id, name, description)
}

// DeleteEngagement removes the engagement, detaching its tasks.
func (d *DB) DeleteEngagement(id int64) (bool, error) { return d.Engagements().DeleteEngagement(id) }

// SetTaskEngagement assigns or clears a task's engagement.
func (d *DB) SetTaskEngagement(taskID int64, engagementID *int64) error {
	return d.Engagements().SetTaskEngagement(taskID, engagementID)
}

// GetEngagement returns one engagement (nil if missing).
func (d *DB) GetEngagement(id int64) (*Engagement, error) { return d.Engagements().GetEngagement(id) }

// engagementNKey normalises a name like task categories: lowercase, trimmed.
func engagementNKey(name string) string {
	return strings.TrimSpace(strings.ToLower(name))
}

// validateEngagementInput checks name/description bounds and returns the
// trimmed name + normalized key.
func validateEngagementInput(name, description string) (string, string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", "", ErrEngagementInvalid
	}
	if utf8.RuneCountInString(name) > MaxEngagementNameRunes {
		return "", "", ErrEngagementNameTooLong
	}
	if utf8.RuneCountInString(description) > MaxEngagementDescRunes {
		return "", "", ErrEngagementDescTooLong
	}
	return name, engagementNKey(name), nil
}

// ListEngagements returns every engagement newest first, each with live-task
// and finding-severity stats. Task counts join only non-deleted tasks.
func (s *EngagementStore) ListEngagements() ([]EngagementWithStats, error) {
	rows, err := s.db.Query(`
SELECT e.id, e.name, e.description, e.company_id, e.created_at, e.updated_at,
       COUNT(t.id),
       COUNT(t.id) FILTER (WHERE t.status IN ('running','queued')),
       COUNT(t.id) FILTER (WHERE t.status IN ('done')),
       COUNT(t.id) FILTER (WHERE t.status IN ('failed','timeout')),
       COALESCE(f.critical,0), COALESCE(f.high,0), COALESCE(f.medium,0), COALESCE(f.low,0)
FROM engagements e
LEFT JOIN tasks t ON t.engagement_id = e.id AND t.deleted_at IS NULL
LEFT JOIN LATERAL (
    SELECT COUNT(*) FILTER (WHERE fl.severity='critical') AS critical,
           COUNT(*) FILTER (WHERE fl.severity='high')     AS high,
           COUNT(*) FILTER (WHERE fl.severity='medium')   AS medium,
           COUNT(*) FILTER (WHERE fl.severity='low')      AS low
    FROM findings fl
    JOIN tasks ft ON fl.task_id = ft.id AND ft.deleted_at IS NULL AND ft.engagement_id = e.id
) f ON true
GROUP BY e.id, f.critical, f.high, f.medium, f.low
ORDER BY e.id DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []EngagementWithStats
	for rows.Next() {
		var e EngagementWithStats
		if err := rows.Scan(&e.ID, &e.Name, &e.Description, &e.CompanyID, &e.CreatedAt, &e.UpdatedAt,
			&e.Stats.TasksTotal, &e.Stats.TasksRunning, &e.Stats.TasksDone, &e.Stats.TasksFailed,
			&e.Stats.Findings.Critical, &e.Stats.Findings.High, &e.Stats.Findings.Medium, &e.Stats.Findings.Low); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// CreateEngagement inserts a new engagement.
func (s *EngagementStore) CreateEngagement(name, description string) (*Engagement, error) {
	name, nkey, err := validateEngagementInput(name, description)
	if err != nil {
		return nil, err
	}
	var e Engagement
	err = s.db.QueryRow(`
INSERT INTO engagements(name, nkey, description)
VALUES ($1, $2, $3)
RETURNING id, name, description, created_at, updated_at`,
		name, nkey, strings.TrimSpace(description)).
		Scan(&e.ID, &e.Name, &e.Description, &e.CreatedAt, &e.UpdatedAt)
	if engagementUniqueViolation(err) {
		return nil, ErrEngagementNameConflict
	}
	if err != nil {
		return nil, err
	}
	return &e, nil
}

// UpdateEngagement changes name and/or description. An empty name keeps the
// current one; description is applied as given (the handler always sends the
// full field, so an empty string clears it).
func (s *EngagementStore) UpdateEngagement(id int64, name, description string) (*Engagement, error) {
	if id <= 0 {
		return nil, ErrEngagementInvalid
	}
	if strings.TrimSpace(name) == "" {
		if err := s.db.QueryRow(`SELECT name FROM engagements WHERE id=$1`, id).Scan(&name); err != nil {
			if err == sql.ErrNoRows {
				return nil, ErrEngagementNotFound
			}
			return nil, err
		}
	}
	name, nkey, err := validateEngagementInput(name, description)
	if err != nil {
		return nil, err
	}
	var e Engagement
	err = s.db.QueryRow(`
UPDATE engagements SET name=$2, nkey=$3, description=$4, updated_at=now()
WHERE id=$1
RETURNING id, name, description, created_at, updated_at`,
		id, name, nkey, strings.TrimSpace(description)).
		Scan(&e.ID, &e.Name, &e.Description, &e.CreatedAt, &e.UpdatedAt)
	if err == sql.ErrNoRows {
		return nil, ErrEngagementNotFound
	}
	if engagementUniqueViolation(err) {
		return nil, ErrEngagementNameConflict
	}
	if err != nil {
		return nil, err
	}
	return &e, nil
}

// DeleteEngagement removes the engagement; its tasks detach (ON DELETE SET NULL).
func (s *EngagementStore) DeleteEngagement(id int64) (bool, error) {
	if id <= 0 {
		return false, ErrEngagementInvalid
	}
	res, err := s.db.Exec(`DELETE FROM engagements WHERE id=$1`, id)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n > 0, err
}

// SetTaskEngagement moves one task into an engagement; nil clears it.
func (s *EngagementStore) SetTaskEngagement(taskID int64, engagementID *int64) error {
	if taskID <= 0 {
		return ErrEngagementTaskNotFound
	}
	if engagementID != nil && *engagementID <= 0 {
		return ErrEngagementInvalid
	}
	var exists bool
	if engagementID != nil {
		if err := s.db.QueryRow(`SELECT true FROM engagements WHERE id=$1`, *engagementID).Scan(&exists); err != nil {
			if err == sql.ErrNoRows {
				return ErrEngagementNotFound
			}
			return err
		}
	}
	res, err := s.db.Exec(`UPDATE tasks SET engagement_id=$2 WHERE id=$1 AND deleted_at IS NULL`, taskID, engagementID)
	if err != nil {
		if engagementUniqueViolation(err) {
			return ErrEngagementNameConflict
		}
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrEngagementTaskNotFound
	}
	return nil
}

// engagementCols is the shared select for single-engagement fetches.
const engagementCols = `id, name, description, company_id, created_at, updated_at`

// GetEngagement returns one engagement (nil if missing).
func (s *EngagementStore) GetEngagement(id int64) (*Engagement, error) {
	if id <= 0 {
		return nil, ErrEngagementInvalid
	}
	var e Engagement
	err := s.db.QueryRow(`SELECT `+engagementCols+` FROM engagements WHERE id=$1`, id).
		Scan(&e.ID, &e.Name, &e.Description, &e.CompanyID, &e.CreatedAt, &e.UpdatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &e, nil
}

// EngagementScopeDomains returns the engagement's company scope domains (company_scope
// kind='domain') via the legacy company binding. A traffic exchange host matches the
// scope when host == domain or host ends with "."+domain. An engagement with no
// company or no domain scope returns an empty (but non-nil) list — callers treat
// that as fail-closed (no traffic matches), never as "unscoped".
func (d *DB) EngagementScopeDomains(engagementID int64) ([]string, error) {
	rows, err := d.Query(`SELECT cs.domain
		FROM company_scope cs
		JOIN engagements e ON e.company_id = cs.company_id
		WHERE e.id = $1 AND cs.kind = 'domain' AND COALESCE(cs.domain, '') <> ''`, engagementID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var domain string
		if err := rows.Scan(&domain); err != nil {
			return nil, err
		}
		out = append(out, domain)
	}
	return out, rows.Err()
}
