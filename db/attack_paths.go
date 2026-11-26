package db

// Attack paths: a scored, evidence-backed chain of assets, findings and facts.
// The store owns every rule (validation of step refs, scoring, state derivation);
// the chain agent's tools stay thin.

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"math"
	"time"
)

// Attack path states.
const (
	PathStateProposed   = "proposed"
	PathStateValidating = "validating"
	PathStateValidated  = "validated"
	PathStateRefuted    = "refuted"
)

// Step verdicts.
const (
	StepStatusProposed = "proposed"
	StepStatusVerified = "verified"
	StepStatusRefuted  = "refuted"
)

// AttackPath is one proposed chain.
type AttackPath struct {
	ID           int64     `json:"id"`
	TaskID       int64     `json:"task_id"`
	Title        string    `json:"title"`
	Rationale    string    `json:"rationale"`
	State        string    `json:"state"`
	Score        float64   `json:"score"`
	RefuteReason string    `json:"refute_reason,omitempty"`
	CreatedBy    string    `json:"created_by,omitempty"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

// AttackPathStep is one hop of a path. RefExists is false when the referenced
// finding/node/asset is gone; such a step contributes no score.
type AttackPathStep struct {
	ID            int64     `json:"id"`
	PathID        int64     `json:"path_id"`
	Position      int       `json:"position"`
	Kind          string    `json:"kind"`
	RefID         int64     `json:"ref_id"`
	Edge          string    `json:"edge"`
	Precondition  string    `json:"precondition"`
	Postcondition string    `json:"postcondition"`
	Status        string    `json:"status"`
	EvidenceNote  string    `json:"evidence_note,omitempty"`
	ValidatedBy   string    `json:"validated_by,omitempty"`
	RefExists     bool      `json:"ref_exists"`
}

// PathStepInput is one step of a proposed path.
type PathStepInput struct {
	Kind          string `json:"kind"`
	RefID         int64  `json:"ref_id"`
	Edge          string `json:"edge"`
	Precondition  string `json:"precondition"`
	Postcondition string `json:"postcondition"`
}

// PathInput is one whole proposed path.
type PathInput struct {
	TaskID    int64           `json:"task_id"`
	Title     string          `json:"title"`
	Rationale string          `json:"rationale"`
	CreatedBy string          `json:"created_by"`
	Steps     []PathStepInput `json:"steps"`
}

// MaxPathSteps caps one path.
const MaxPathSteps = 8

func validStepEdge(e string) bool {
	switch e {
	case "enables", "requires", "pivots_to", "escalates":
		return true
	}
	return false
}

// proposePathTx inserts a path and its steps. Step refs are validated against the
// task first: finding/fact must be an exploration node of the task, asset must be
// a task-linked asset.
func proposePathTx(ctx context.Context, tx *sql.Tx, in PathInput) (*AttackPath, error) {
	if len(in.Steps) < 2 {
		return nil, fmt.Errorf("a path needs at least 2 steps, got %d", len(in.Steps))
	}
	if len(in.Steps) > MaxPathSteps {
		return nil, fmt.Errorf("a path takes at most %d steps, got %d", MaxPathSteps, len(in.Steps))
	}
	for i, st := range in.Steps {
		if st.Kind != "finding" && st.Kind != "fact" && st.Kind != "asset" {
			return nil, fmt.Errorf("step %d: kind must be finding, fact or asset", i+1)
		}
		if !validStepEdge(st.Edge) {
			return nil, fmt.Errorf("step %d: edge must be enables, requires, pivots_to or escalates", i+1)
		}
		var ok bool
		switch st.Kind {
		case "asset":
			err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM task_asset_links l
WHERE l.task_id=$1 AND l.asset_id=$2)`, in.TaskID, st.RefID).Scan(&ok)
			if err != nil {
				return nil, err
			}
			if !ok {
				return nil, fmt.Errorf("step %d: asset %d is not linked to this task", i+1, st.RefID)
			}
		default:
			// A finding step refs the canonical findings row (its id carries severity/
			// confidence/asset_ids, which scoring and validation read); a fact step refs
			// the exploration node. Both must belong to this task.
			if st.Kind == "finding" {
				err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM findings
WHERE id=$1 AND task_id=$2 AND node_id IS NOT NULL)`, st.RefID, in.TaskID).Scan(&ok)
				if err != nil {
					return nil, err
				}
				if !ok {
					return nil, fmt.Errorf("step %d: finding %d does not exist in this task", i+1, st.RefID)
				}
			} else {
				err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM exploration_nodes
WHERE id=$1 AND exploration_id=(SELECT exploration_id FROM tasks WHERE id=$2) AND kind=$3)`,
					st.RefID, in.TaskID, st.Kind).Scan(&ok)
				if err != nil {
					return nil, err
				}
				if !ok {
					return nil, fmt.Errorf("step %d: %s node %d does not exist in this task", i+1, st.Kind, st.RefID)
				}
			}
		}
	}
	var p AttackPath
	err := tx.QueryRowContext(ctx, `INSERT INTO attack_paths(task_id, title, rationale, created_by)
VALUES ($1,$2,$3,$4) RETURNING id, task_id, title, rationale, state, score, created_at, updated_at`,
		in.TaskID, in.Title, in.Rationale, in.CreatedBy).
		Scan(&p.ID, &p.TaskID, &p.Title, &p.Rationale, &p.State, &p.Score, &p.CreatedAt, &p.UpdatedAt)
	if err != nil {
		return nil, err
	}
	for i, st := range in.Steps {
		if _, err := tx.ExecContext(ctx, `INSERT INTO attack_path_steps
(path_id, position, kind, ref_id, edge, precondition, postcondition) VALUES ($1,$2,$3,$4,$5,$6,$7)`,
			p.ID, i+1, st.Kind, st.RefID, st.Edge, st.Precondition, st.Postcondition); err != nil {
			return nil, err
		}
	}
	if err := RecomputePathScoreTx(ctx, tx, p.ID); err != nil {
		return nil, err
	}
	if err := tx.QueryRowContext(ctx, `SELECT score, state FROM attack_paths WHERE id=$1`, p.ID).Scan(&p.Score, &p.State); err != nil {
		return nil, err
	}
	return &p, nil
}

// ProposePathTx runs ProposePath in a transaction the caller owns (the tool also
// spawns the follow-up intent in the same tx).
func ProposePathTx(ctx context.Context, tx *sql.Tx, in PathInput) (*AttackPath, error) {
	return proposePathTx(ctx, tx, in)
}

// PutPathStepTx appends a step at the end of a path.
func PutPathStepTx(ctx context.Context, tx *sql.Tx, pathID int64, st PathStepInput) error {
	var next int
	if err := tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(position),0)+1 FROM attack_path_steps WHERE path_id=$1`, pathID).Scan(&next); err != nil {
		return err
	}
	_, err := tx.ExecContext(ctx, `INSERT INTO attack_path_steps
(path_id, position, kind, ref_id, edge, precondition, postcondition) VALUES ($1,$2,$3,$4,$5,$6,$7)`,
		pathID, next, st.Kind, st.RefID, st.Edge, st.Precondition, st.Postcondition)
	return err
}

func severityWeight(severity string) float64 {
	switch severity {
	case "critical":
		return 10
	case "high":
		return 6
	case "medium":
		return 3
	case "low":
		return 1
	}
	return 1
}

// RecomputePathScoreTx derives score and state from the steps: the sum of each
// step's severity × finding confidence × status multiplier. Any refuted step
// refutes the path; all verified validates it.
func RecomputePathScoreTx(ctx context.Context, tx *sql.Tx, pathID int64) error {
	// ref_exists guards the score: a step whose referenced row is gone contributes
	// nothing (GetAttackPath renders it as missing) instead of scoring from defaults.
	rows, err := tx.QueryContext(ctx, `SELECT s.status, s.kind, s.ref_id,
COALESCE(f.severity, ''), COALESCE(f.confidence, 0),
CASE s.kind
  WHEN 'finding' THEN (f.id IS NOT NULL)
  WHEN 'fact' THEN EXISTS(SELECT 1 FROM exploration_nodes n WHERE n.id = s.ref_id AND n.kind = 'fact')
  WHEN 'asset' THEN EXISTS(SELECT 1 FROM task_asset_links l WHERE l.asset_id = s.ref_id AND l.task_id = p.task_id)
  ELSE false
END
FROM attack_path_steps s
JOIN attack_paths p ON p.id = s.path_id
LEFT JOIN findings f ON f.id = s.ref_id AND s.kind = 'finding'
WHERE s.path_id=$1 ORDER BY s.position`, pathID)
	if err != nil {
		return err
	}
	defer rows.Close()
	var score float64
	var total, verified, refuted int
	for rows.Next() {
		var status, kind, severity string
		var refID int64
		var confidence int
		var refExists bool
		if err := rows.Scan(&status, &kind, &refID, &severity, &confidence, &refExists); err != nil {
			return err
		}
		total++
		switch status {
		case StepStatusRefuted:
			refuted++
			continue
		case StepStatusVerified:
			verified++
		}
		if !refExists {
			continue // missing ref drops its weight
		}
		weight := 1.0
		if kind == "fact" {
			weight = 2
		} else if kind == "finding" {
			weight = severityWeight(severity) * float64(confidence) / 100
		}
		mult := 0.6
		if status == StepStatusVerified {
			mult = 1.2
		}
		score += weight * mult
	}
	if err := rows.Err(); err != nil {
		return err
	}
	score = math.Round(score*100) / 100
	state := PathStateProposed
	switch {
	case refuted > 0:
		state = PathStateRefuted
		score = 0
	case total > 0 && verified == total:
		state = PathStateValidated
	case verified > 0:
		state = PathStateValidating
	}
	if state == PathStateRefuted {
		_, err = tx.ExecContext(ctx, `UPDATE attack_paths SET score=0, state=$2, updated_at=now() WHERE id=$1`, pathID, state)
	} else {
		_, err = tx.ExecContext(ctx, `UPDATE attack_paths SET score=$2, state=$3, updated_at=now() WHERE id=$1`, pathID, score, state)
	}
	return err
}

// SetStepStatusTx records a verdict on one step and rescores the path.
func SetStepStatusTx(ctx context.Context, tx *sql.Tx, pathID int64, position int, status, note, by string) error {
	res, err := tx.ExecContext(ctx, `UPDATE attack_path_steps
SET status=$3, evidence_note=$4, validated_by=$5, updated_at=now()
WHERE path_id=$1 AND position=$2`, pathID, position, status, note, by)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("path %d has no step %d", pathID, position)
	}
	return RecomputePathScoreTx(ctx, tx, pathID)
}

// RefutePath refutes a whole path with a reason.
func (d *DB) RefutePath(ctx context.Context, pathID int64, reason string) error {
	res, err := d.ExecContext(ctx, `UPDATE attack_paths
SET state='refuted', score=0, refute_reason=$2, updated_at=now() WHERE id=$1`, pathID, reason)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("path %d not found", pathID)
	}
	return nil
}

// ListAttackPaths returns a task's paths, optionally filtered by state, best first.
func (d *DB) ListAttackPaths(taskID int64, states []string, limit int) ([]*AttackPath, error) {
	if limit <= 0 {
		limit = 50
	}
	q := `SELECT id, task_id, title, rationale, state, score, refute_reason, created_by, created_at, updated_at
FROM attack_paths WHERE task_id=$1`
	args := []any{taskID}
	if len(states) > 0 {
		args = append(args, states)
		q += ` AND state = ANY($2)`
	}
	q += fmt.Sprintf(` ORDER BY score DESC, id DESC LIMIT $%d`, len(args)+1)
	args = append(args, limit)
	rows, err := d.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*AttackPath
	for rows.Next() {
		var p AttackPath
		if err := rows.Scan(&p.ID, &p.TaskID, &p.Title, &p.Rationale, &p.State, &p.Score, &p.RefuteReason, &p.CreatedBy, &p.CreatedAt, &p.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, &p)
	}
	return out, rows.Err()
}

// PathSteps returns one path's steps ordered by position.
func (d *DB) PathSteps(pathID int64) ([]*AttackPathStep, error) {
	rows, err := d.Query(`SELECT s.id, s.path_id, s.position, s.kind, s.ref_id, s.edge, s.precondition,
s.postcondition, s.status, s.evidence_note, s.validated_by,
CASE s.kind
  WHEN 'asset' THEN EXISTS(SELECT 1 FROM assets a WHERE a.id = s.ref_id)
  ELSE EXISTS(SELECT 1 FROM exploration_nodes n WHERE n.id = s.ref_id AND n.kind = s.kind)
END
FROM attack_path_steps s WHERE s.path_id=$1 ORDER BY s.position`, pathID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var steps []*AttackPathStep
	for rows.Next() {
		var st AttackPathStep
		if err := rows.Scan(&st.ID, &st.PathID, &st.Position, &st.Kind, &st.RefID, &st.Edge, &st.Precondition,
			&st.Postcondition, &st.Status, &st.EvidenceNote, &st.ValidatedBy, &st.RefExists); err != nil {
			return nil, err
		}
		steps = append(steps, &st)
	}
	return steps, rows.Err()
}

// GetAttackPath returns one path with its steps.
func (d *DB) GetAttackPath(pathID int64) (*AttackPath, []*AttackPathStep, error) {
	var p AttackPath
	err := d.QueryRow(`SELECT id, task_id, title, rationale, state, score, refute_reason, created_by, created_at, updated_at
FROM attack_paths WHERE id=$1`, pathID).
		Scan(&p.ID, &p.TaskID, &p.Title, &p.Rationale, &p.State, &p.Score, &p.RefuteReason, &p.CreatedBy, &p.CreatedAt, &p.UpdatedAt)
	if err != nil {
		return nil, nil, err
	}
	rows, err := d.Query(`SELECT s.id, s.path_id, s.position, s.kind, s.ref_id, s.edge, s.precondition,
s.postcondition, s.status, s.evidence_note, s.validated_by,
CASE s.kind
  WHEN 'asset' THEN EXISTS(SELECT 1 FROM assets a WHERE a.id = s.ref_id)
  ELSE EXISTS(SELECT 1 FROM exploration_nodes n WHERE n.id = s.ref_id AND n.kind = s.kind)
END
FROM attack_path_steps s WHERE s.path_id=$1 ORDER BY s.position`, pathID)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	var steps []*AttackPathStep
	for rows.Next() {
		var st AttackPathStep
		if err := rows.Scan(&st.ID, &st.PathID, &st.Position, &st.Kind, &st.RefID, &st.Edge, &st.Precondition,
			&st.Postcondition, &st.Status, &st.EvidenceNote, &st.ValidatedBy, &st.RefExists); err != nil {
			return nil, nil, err
		}
		steps = append(steps, &st)
	}
	return &p, steps, rows.Err()
}

// CountOpenPathIntents counts live intents spawned for a path.
func (d *DB) CountOpenPathIntents(pathID int64) (int, error) {
	var n int
	err := d.QueryRow(`SELECT count(*) FROM exploration_nodes
WHERE kind='intent' AND state IN ('open','running') AND payload ? 'path_id'
  AND (payload->>'path_id')::bigint = $1`, pathID).Scan(&n)
	return n, err
}

// AdvanceChainCursor moves the chain agent's "seen findings" cursor forward.
func (d *DB) AdvanceChainCursor(taskID, lastFindingID int64) error {
	_, err := d.Exec(`INSERT INTO chain_cursor(task_id, last_finding_id) VALUES ($1,$2)
ON CONFLICT (task_id) DO UPDATE SET last_finding_id=GREATEST(chain_cursor.last_finding_id, EXCLUDED.last_finding_id), updated_at=now()`,
		taskID, lastFindingID)
	return err
}

// ChainCursor returns the last finding id the chain agent consumed (0 when none).
func (d *DB) ChainCursor(taskID int64) (int64, error) {
	var id int64
	err := d.QueryRow(`SELECT last_finding_id FROM chain_cursor WHERE task_id=$1`, taskID).Scan(&id)
	if err == sql.ErrNoRows {
		return 0, nil
	}
	return id, err
}

// PathForFinding returns the open path ids that reference a finding.
func (d *DB) PathForFinding(findingID int64) ([]int64, error) {
	rows, err := d.Query(`SELECT DISTINCT s.path_id FROM attack_path_steps s
JOIN attack_paths p ON p.id = s.path_id
WHERE s.kind='finding' AND s.ref_id=$1 AND p.state <> 'refuted'`, findingID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// jsonbBytes is the JSONB write form used by the event stores.
func jsonbBytes(raw json.RawMessage) []byte {
	if len(raw) == 0 {
		return []byte("{}")
	}
	return raw
}
