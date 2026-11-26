package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

// =====================================================================
// Engagement records: the engagement-level history the task board does
// not keep — scope revisions, operator decisions, milestones, briefs,
// notes — plus per-operator review cursors and the knowledge injection
// log. Append-only: superseded rows are kept, never rewritten.
// =====================================================================

// Engagement record kinds. A scope revision's payload holds the diff; the
// active revision is the latest accepted one. operator_decision records
// what was decided, why and what it changed. milestone carries owner
// (actor), due_at and completion criteria.
const (
	RecordKindScopeRevision    = "scope_revision"
	RecordKindOperatorDecision = "operator_decision"
	RecordKindMilestone        = "milestone"
	RecordKindBrief            = "brief"
	RecordKindNote             = "note"
	RecordStatusOpen           = "open"
	RecordStatusAccepted       = "accepted"
	RecordStatusSuperseded     = "superseded"
	RecordStatusDone           = "done"
)

var recordKinds = map[string]bool{
	RecordKindScopeRevision:    true,
	RecordKindOperatorDecision: true,
	RecordKindMilestone:        true,
	RecordKindBrief:            true,
	RecordKindNote:             true,
}

var recordStatuses = map[string]bool{
	RecordStatusOpen:       true,
	RecordStatusAccepted:   true,
	RecordStatusSuperseded: true,
	RecordStatusDone:       true,
}

var (
	ErrEngagementRecordInvalid  = errors.New("invalid engagement record")
	ErrEngagementRecordNotFound = errors.New("engagement record not found")
)

// EngagementRecord is one row of engagement_records.
type EngagementRecord struct {
	ID           int64           `json:"id"`
	EngagementID int64           `json:"engagement_id"`
	Kind         string          `json:"kind"`
	Title        string          `json:"title"`
	Body         string          `json:"body"`
	Actor        string          `json:"actor"`
	Status       string          `json:"status"`
	DueAt        *time.Time      `json:"due_at,omitempty"`
	CompletedAt  *time.Time      `json:"completed_at,omitempty"`
	Criteria     string          `json:"criteria"`
	SupersedesID *int64          `json:"supersedes_id,omitempty"`
	SourceKind   string          `json:"source_kind"`
	SourceID     *int64          `json:"source_id,omitempty"`
	Payload      json.RawMessage `json:"payload"`
	CreatedAt    time.Time       `json:"created_at"`
	UpdatedAt    time.Time       `json:"updated_at"`
}

// EngagementReview is one operator review click: the cursor marking
// "seen everything through this record id".
type EngagementReview struct {
	ID           int64     `json:"id"`
	EngagementID int64     `json:"engagement_id"`
	Operator     string    `json:"operator"`
	ReviewedAt   time.Time `json:"reviewed_at"`
	UpToRecordID int64     `json:"up_to_record_id"`
}

// KnowledgeInjection logs which knowledge item was selected for which
// agent run (the rsi_lesson_usage pattern). Shadow = logged only,
// inject = appended to the prompt.
type KnowledgeInjection struct {
	ID        int64     `json:"id"`
	ItemID    int64     `json:"item_id"`
	TaskID    int64     `json:"task_id"`
	IntentID  *int64    `json:"intent_id,omitempty"`
	AgentName string    `json:"agent_name"`
	Mode      string    `json:"mode"`
	ShownAt   time.Time `json:"shown_at"`
}

const engagementRecordCols = `id, engagement_id, kind, title, body, actor, status,
	due_at, completed_at, criteria, supersedes_id, source_kind, source_id, payload,
	created_at, updated_at`

func scanEngagementRecord(row interface{ Scan(...any) error }) (EngagementRecord, error) {
	var r EngagementRecord
	err := row.Scan(&r.ID, &r.EngagementID, &r.Kind, &r.Title, &r.Body, &r.Actor, &r.Status,
		&r.DueAt, &r.CompletedAt, &r.Criteria, &r.SupersedesID, &r.SourceKind, &r.SourceID, &r.Payload,
		&r.CreatedAt, &r.UpdatedAt)
	if err == sql.ErrNoRows {
		return r, ErrEngagementRecordNotFound
	}
	return r, err
}

// CreateEngagementRecord appends one record. kind and title are required;
// a supersedes_id must point at a record of the same engagement.
func (d *DB) CreateEngagementRecord(engagementID int64, rec *EngagementRecord) (*EngagementRecord, error) {
	if rec == nil {
		return nil, ErrEngagementRecordInvalid
	}
	rec.Kind = strings.TrimSpace(rec.Kind)
	rec.Title = strings.TrimSpace(rec.Title)
	rec.Actor = strings.TrimSpace(rec.Actor)
	rec.SourceKind = strings.TrimSpace(rec.SourceKind)
	if !recordKinds[rec.Kind] {
		return nil, fmt.Errorf("%w: unknown kind %q", ErrEngagementRecordInvalid, rec.Kind)
	}
	if rec.Title == "" {
		return nil, fmt.Errorf("%w: title required", ErrEngagementRecordInvalid)
	}
	if rec.Status == "" {
		rec.Status = RecordStatusOpen
	}
	if !recordStatuses[rec.Status] {
		return nil, fmt.Errorf("%w: unknown status %q", ErrEngagementRecordInvalid, rec.Status)
	}
	if rec.Payload == nil {
		rec.Payload = json.RawMessage(`{}`)
	}
	if rec.SourceKind != "" && rec.SourceKind != "task" && rec.SourceKind != "finding" &&
		rec.SourceKind != "record" && rec.SourceKind != "checkpoint" && rec.SourceKind != "event" {
		return nil, fmt.Errorf("%w: unknown source_kind %q", ErrEngagementRecordInvalid, rec.SourceKind)
	}
	if rec.SupersedesID != nil {
		var supEngagement int64
		err := d.QueryRow(`SELECT engagement_id FROM engagement_records WHERE id=$1`, *rec.SupersedesID).Scan(&supEngagement)
		if err != nil {
			return nil, fmt.Errorf("supersedes record %d: %w", *rec.SupersedesID, err)
		}
		if supEngagement != engagementID {
			return nil, fmt.Errorf("%w: supersedes record belongs to another engagement", ErrEngagementRecordInvalid)
		}
	}
	// Accepting a scope revision or decision is a transition, not a state a
	// record may be born into: the insert must not skip superseding the record
	// it replaces, or two accepted revisions would both look current.
	status := rec.Status
	acceptOnCreate := status == RecordStatusAccepted &&
		(rec.Kind == RecordKindScopeRevision || rec.Kind == RecordKindOperatorDecision)
	if acceptOnCreate {
		status = RecordStatusOpen
	}
	created, err := scanEngagementRecord(d.QueryRow(`
INSERT INTO engagement_records (engagement_id, kind, title, body, actor, status,
	due_at, criteria, supersedes_id, source_kind, source_id, payload)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)
RETURNING `+engagementRecordCols,
		engagementID, rec.Kind, rec.Title, rec.Body, rec.Actor, status,
		rec.DueAt, rec.Criteria, rec.SupersedesID, rec.SourceKind, rec.SourceID, rec.Payload))
	if err != nil {
		return nil, err
	}
	if acceptOnCreate {
		return d.UpdateEngagementRecordStatus(created.ID, RecordStatusAccepted)
	}
	return &created, nil
}

// UpdateEngagementRecordStatus flips a record's status. Milestones can only
// complete with criteria set. Accepting a scope_revision or an
// operator_decision supersedes the previously accepted record of the same
// kind in this engagement and links back to it.
func (d *DB) UpdateEngagementRecordStatus(id int64, status string) (*EngagementRecord, error) {
	if !recordStatuses[status] {
		return nil, fmt.Errorf("%w: unknown status %q", ErrEngagementRecordInvalid, status)
	}
	var kind string
	var criteria string
	var engagementID int64
	var currentStatus string
	err := d.QueryRow(`SELECT kind, criteria, engagement_id, status FROM engagement_records WHERE id=$1`, id).
		Scan(&kind, &criteria, &engagementID, &currentStatus)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, ErrEngagementRecordNotFound
		}
		return nil, err
	}
	if currentStatus == RecordStatusSuperseded {
		return nil, fmt.Errorf("%w: superseded record %d is immutable", ErrEngagementRecordInvalid, id)
	}
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, ErrEngagementRecordNotFound
		}
		return nil, err
	}
	if status == RecordStatusDone && kind == RecordKindMilestone && strings.TrimSpace(criteria) == "" {
		return nil, fmt.Errorf("%w: milestone completion requires criteria", ErrEngagementRecordInvalid)
	}

	var completedAt *time.Time
	if status == RecordStatusDone {
		now := time.Now().UTC()
		completedAt = &now
	}

	switch {
	case status == RecordStatusAccepted && (kind == RecordKindScopeRevision || kind == RecordKindOperatorDecision):
		// Accepting supersedes the previous accepted record of the same kind.
		var prev *int64
		err := d.QueryRow(`
UPDATE engagement_records SET status=$1, updated_at=now()
WHERE engagement_id=$2 AND kind=$3 AND status=$4
RETURNING id`, RecordStatusSuperseded, engagementID, kind, RecordStatusAccepted).Scan(&prev)
		if err != nil && err != sql.ErrNoRows {
			return nil, err
		}
		var r EngagementRecord
		if prev != nil {
			r, err = scanEngagementRecord(d.QueryRow(`
UPDATE engagement_records SET status=$1, completed_at=now(), updated_at=now(),
	supersedes_id=COALESCE($3, supersedes_id)
WHERE id=$2 RETURNING `+engagementRecordCols,
				status, id, *prev))
		} else {
			r, err = scanEngagementRecord(d.QueryRow(`
UPDATE engagement_records SET status=$1, completed_at=now(), updated_at=now()
WHERE id=$2 RETURNING `+engagementRecordCols, status, id))
		}
		return &r, err
	default:
		r, err := scanEngagementRecord(d.QueryRow(`
UPDATE engagement_records SET status=$1, completed_at=COALESCE($3, completed_at), updated_at=now()
WHERE id=$2 RETURNING `+engagementRecordCols, status, id, completedAt))
		return &r, err
	}
}

// GetEngagementRecord returns one record by id.
func (d *DB) GetEngagementRecord(id int64) (*EngagementRecord, error) {
	r, err := scanEngagementRecord(d.QueryRow(`SELECT `+engagementRecordCols+` FROM engagement_records WHERE id=$1`, id))
	if err != nil {
		return nil, err
	}
	return &r, nil
}

// ListEngagementRecords returns records newest first. kinds and status are
// optional filters; sinceID is a pagination cursor (only ids below it).
// limit <= 0 means all.
func (d *DB) ListEngagementRecords(engagementID int64, kinds []string, status string, sinceID int64, limit int) ([]EngagementRecord, error) {
	q := `SELECT ` + engagementRecordCols + ` FROM engagement_records WHERE engagement_id=$1`
	args := []any{engagementID}
	if len(kinds) > 0 {
		q += ` AND kind = ANY($2)`
		args = append(args, kinds)
	}
	if status != "" {
		if !recordStatuses[status] {
			return nil, fmt.Errorf("%w: unknown status %q", ErrEngagementRecordInvalid, status)
		}
		q += fmt.Sprintf(` AND status=$%d`, len(args)+1)
		args = append(args, status)
	}
	if sinceID > 0 {
		q += fmt.Sprintf(` AND id < $%d`, len(args)+1)
		args = append(args, sinceID)
	}
	q += ` ORDER BY id DESC`
	if limit > 0 {
		q += fmt.Sprintf(` LIMIT %d`, limit)
	}
	rows, err := d.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []EngagementRecord
	for rows.Next() {
		r, err := scanEngagementRecord(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// RecordsSinceID returns records created after the given record id (the
// review-cursor delta).
func (d *DB) RecordsSinceID(engagementID int64, sinceRecordID int64) ([]EngagementRecord, error) {
	rows, err := d.Query(`
SELECT `+engagementRecordCols+` FROM engagement_records
WHERE engagement_id=$1 AND id > $2 ORDER BY id DESC`, engagementID, sinceRecordID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []EngagementRecord
	for rows.Next() {
		r, err := scanEngagementRecord(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// AddEngagementReview records an operator review click. upToRecordID is the
// newest record id at review time (0 for an empty engagement).
func (d *DB) AddEngagementReview(engagementID int64, operator string, upToRecordID int64) (*EngagementReview, error) {
	operator = strings.TrimSpace(operator)
	if operator == "" {
		return nil, errors.New("operator required")
	}
	var r EngagementReview
	err := d.QueryRow(`
INSERT INTO engagement_reviews (engagement_id, operator, up_to_record_id)
VALUES ($1,$2,$3)
RETURNING id, engagement_id, operator, reviewed_at, up_to_record_id`,
		engagementID, operator, upToRecordID).
		Scan(&r.ID, &r.EngagementID, &r.Operator, &r.ReviewedAt, &r.UpToRecordID)
	if err != nil {
		return nil, err
	}
	return &r, nil
}

// LatestEngagementReview returns the operator's most recent review, or nil
// when they have never reviewed this engagement.
func (d *DB) LatestEngagementReview(engagementID int64, operator string) (*EngagementReview, error) {
	var r EngagementReview
	err := d.QueryRow(`
SELECT id, engagement_id, operator, reviewed_at, up_to_record_id
FROM engagement_reviews WHERE engagement_id=$1 AND operator=$2
ORDER BY reviewed_at DESC, id DESC LIMIT 1`, engagementID, operator).
		Scan(&r.ID, &r.EngagementID, &r.Operator, &r.ReviewedAt, &r.UpToRecordID)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &r, nil
}

// MaxRecordID returns the newest record id of an engagement (0 when none).
func (d *DB) MaxRecordID(engagementID int64) (int64, error) {
	var id int64
	err := d.QueryRow(`SELECT COALESCE(MAX(id),0) FROM engagement_records WHERE engagement_id=$1`, engagementID).Scan(&id)
	return id, err
}

// LogKnowledgeInjection records that a knowledge item was selected for an
// agent run. Best-effort by callers: a logging failure never blocks a run.
func (d *DB) LogKnowledgeInjection(itemID, taskID int64, intentID *int64, agentName, mode string) error {
	if mode == "" {
		mode = "shadow"
	}
	_, err := d.Exec(`
INSERT INTO knowledge_injections (item_id, task_id, intent_id, agent_name, mode)
VALUES ($1,$2,$3,$4,$5)`, itemID, taskID, intentID, agentName, mode)
	return err
}

// ListKnowledgeInjections returns the most recent selections for a task.
func (d *DB) ListKnowledgeInjections(taskID int64, limit int) ([]KnowledgeInjection, error) {
	if limit <= 0 || limit > 500 {
		limit = 200
	}
	rows, err := d.Query(`
SELECT id, item_id, task_id, intent_id, agent_name, mode, shown_at
FROM knowledge_injections WHERE task_id=$1
ORDER BY shown_at DESC, id DESC LIMIT $2`, taskID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []KnowledgeInjection
	for rows.Next() {
		var k KnowledgeInjection
		if err := rows.Scan(&k.ID, &k.ItemID, &k.TaskID, &k.IntentID, &k.AgentName, &k.Mode, &k.ShownAt); err != nil {
			return nil, err
		}
		out = append(out, k)
	}
	return out, rows.Err()
}

// =====================================================================
// Engagement timeline: one unioned page over records, task outcome
// deltas, findings, retest verdicts, checkpoints and open coverage gaps.
// =====================================================================

// Timeline entry kinds (the union across sources).
const (
	EntryTaskOutcome = "task_outcome"
	EntryFinding     = "finding"
	EntryRetest      = "retest"
	EntryCheckpoint  = "checkpoint"
	EntryCoverageGap = "coverage_gap"
)

// EngagementTimelineEntry is one row of the unioned timeline. EntryID is
// the source row's id (records use their own id); SourceKind/SourceID +
// Link resolve every entry back to its evidence.
type EngagementTimelineEntry struct {
	EntryID    int64           `json:"entry_id"`
	Kind       string          `json:"kind"`
	CreatedAt  time.Time       `json:"created_at"`
	TaskID     *int64          `json:"task_id,omitempty"`
	SourceKind string          `json:"source_kind"`
	SourceID   int64           `json:"source_id"`
	Title      string          `json:"title"`
	Summary    string          `json:"summary"`
	Payload    json.RawMessage `json:"payload,omitempty"`
	Link       string          `json:"link"`
}

const timelineSourceLimit = 200

func taskDetailLink(taskID int64) string {
	return fmt.Sprintf("/function/tasks/detail?id=%d", taskID)
}

// EngagementTimeline returns up to limit entries for the engagement,
// newest first, strictly after since (zero time = from the beginning).
// kinds filters the entry kinds; nil/empty means all.
func (d *DB) EngagementTimeline(engagementID int64, since time.Time, kinds []string, limit int) ([]EngagementTimelineEntry, error) {
	if limit <= 0 {
		limit = 100
	}
	want := map[string]bool{}
	for _, k := range kinds {
		want[k] = true
	}
	all := len(want) == 0
	var entries []EngagementTimelineEntry

	// Records: entry kind = the record's kind.
	if all || want[RecordKindScopeRevision] || want[RecordKindOperatorDecision] || want[RecordKindMilestone] || want[RecordKindBrief] || want[RecordKindNote] {
		rows, err := d.Query(`
SELECT id, kind, created_at, source_kind, COALESCE(source_id,0), title, LEFT(body, 500)
FROM engagement_records
WHERE engagement_id=$1 AND created_at > $2
ORDER BY created_at DESC, id DESC LIMIT $3`, engagementID, since, timelineSourceLimit)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var e EngagementTimelineEntry
			if err := rows.Scan(&e.EntryID, &e.Kind, &e.CreatedAt, &e.SourceKind, &e.SourceID, &e.Title, &e.Summary); err != nil {
				rows.Close()
				return nil, err
			}
			e.SourceKind = "record"
			if !all && !want[e.Kind] {
				continue
			}
			entries = append(entries, e)
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return nil, err
		}
		rows.Close()
	}

	// Task outcome deltas: tasks touched since the cursor.
	if all || want[EntryTaskOutcome] {
		rows, err := d.Query(`
SELECT t.id, t.updated_at, t.name, COALESCE(t.description,''), t.status
FROM tasks t
WHERE t.engagement_id=$1 AND t.deleted_at IS NULL AND t.updated_at > $2
ORDER BY t.updated_at DESC, t.id DESC LIMIT $3`, engagementID, since, timelineSourceLimit)
		if err != nil {
			return nil, err
		}
		var taskIDs []int64
		type taskMeta struct {
			id          int64
			updatedAt   time.Time
			name        string
			description string
			status      string
		}
		var metas []taskMeta
		for rows.Next() {
			var m taskMeta
			if err := rows.Scan(&m.id, &m.updatedAt, &m.name, &m.description, &m.status); err != nil {
				rows.Close()
				return nil, err
			}
			metas = append(metas, m)
			taskIDs = append(taskIDs, m.id)
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return nil, err
		}
		rows.Close()
		outcomes := map[int64]TaskOutcome{}
		for _, id := range taskIDs {
			o, err := d.TaskOutcome(context.Background(), id)
			if err != nil {
				continue // deleted between the two queries
			}
			outcomes[id] = o
		}
		for _, m := range metas {
			o, ok := outcomes[m.id]
			if !ok {
				continue
			}
			payload, _ := json.Marshal(o)
			entries = append(entries, EngagementTimelineEntry{
				EntryID:    m.id,
				Kind:       EntryTaskOutcome,
				CreatedAt:  m.updatedAt,
				TaskID:     &m.id,
				SourceKind: "task",
				SourceID:   m.id,
				Title:      taskDisplayName(m.name, m.description),
				Summary:    fmt.Sprintf("status %s — %d findings (%d confirmed, %d open), %d retests", m.status, o.Findings.Total, o.Findings.Confirmed, o.Findings.Open, o.Retests.Reproduced+o.Retests.Fixed),
				Payload:    payload,
				Link:       taskDetailLink(m.id),
			})
		}
	}

	// Findings created since the cursor.
	if all || want[EntryFinding] {
		rows, err := d.Query(`
SELECT f.id, f.created_at, f.task_id, COALESCE(NULLIF(f.name,''), f.vulnclass), f.severity, f.status, f.report_state, COALESCE(f.summary,'')
FROM findings f JOIN tasks t ON f.task_id=t.id
WHERE t.engagement_id=$1 AND t.deleted_at IS NULL AND f.created_at > $2
ORDER BY f.created_at DESC, f.id DESC LIMIT $3`, engagementID, since, timelineSourceLimit)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var e EngagementTimelineEntry
			var severity, status, reportState string
			var summary string
			if err := rows.Scan(&e.EntryID, &e.CreatedAt, &e.TaskID, &e.Title, &severity, &status, &reportState, &summary); err != nil {
				rows.Close()
				return nil, err
			}
			payload, _ := json.Marshal(map[string]any{
				"finding_id": e.EntryID, "severity": severity, "status": status, "report_state": reportState,
			})
			e.Kind = EntryFinding
			e.SourceKind = "finding"
			e.SourceID = e.EntryID
			e.Payload = payload
			e.Summary = fmt.Sprintf("%s — %s (%s)", severity, status, reportState)
			if summary != "" {
				e.Summary = summary
			}
			if e.TaskID != nil {
				e.Link = taskDetailLink(*e.TaskID)
			}
			entries = append(entries, e)
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return nil, err
		}
		rows.Close()
	}

	// Retest verdicts completed since the cursor.
	if all || want[EntryRetest] {
		rows, err := d.Query(`
SELECT fr.id, fr.finished_at, f.task_id, COALESCE(NULLIF(f.name,''), f.vulnclass), fr.verdict, fr.finding_id
FROM finding_retests fr
JOIN findings f ON fr.finding_id=f.id
JOIN tasks t ON f.task_id=t.id
WHERE t.engagement_id=$1 AND t.deleted_at IS NULL AND fr.finished_at > $2 AND fr.verdict <> ''
ORDER BY fr.finished_at DESC, fr.id DESC LIMIT $3`, engagementID, since, timelineSourceLimit)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var e EngagementTimelineEntry
			var verdict string
			var findingID int64
			if err := rows.Scan(&e.EntryID, &e.CreatedAt, &e.TaskID, &e.Title, &verdict, &findingID); err != nil {
				rows.Close()
				return nil, err
			}
			payload, _ := json.Marshal(map[string]any{"retest_id": e.EntryID, "finding_id": findingID, "verdict": verdict})
			e.Kind = EntryRetest
			e.SourceKind = "finding"
			e.SourceID = findingID
			e.Payload = payload
			e.Summary = fmt.Sprintf("retest %s", verdict)
			if e.TaskID != nil {
				e.Link = taskDetailLink(*e.TaskID)
			}
			entries = append(entries, e)
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return nil, err
		}
		rows.Close()
	}

	// Checkpoint saves: durable progress milestones per intent.
	if all || want[EntryCheckpoint] {
		rows, err := d.Query(`
SELECT ic.id, ic.created_at, ic.task_id, ic.phase, ic.next_step,
       jsonb_array_length(ic.done_items), jsonb_array_length(ic.artifacts), COALESCE(ic.agent_name,'')
FROM intent_checkpoints ic JOIN tasks t ON ic.task_id=t.id
WHERE t.engagement_id=$1 AND t.deleted_at IS NULL AND ic.created_at > $2
ORDER BY ic.created_at DESC, ic.id DESC LIMIT $3`, engagementID, since, timelineSourceLimit)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var e EngagementTimelineEntry
			var phase, nextStep, agent string
			var done, artifacts int
			if err := rows.Scan(&e.EntryID, &e.CreatedAt, &e.TaskID, &phase, &nextStep, &done, &artifacts, &agent); err != nil {
				rows.Close()
				return nil, err
			}
			payload, _ := json.Marshal(map[string]any{"checkpoint_id": e.EntryID, "phase": phase, "next_step": nextStep, "done": done, "artifacts": artifacts, "agent": agent})
			e.Kind = EntryCheckpoint
			e.SourceKind = "checkpoint"
			e.SourceID = e.EntryID
			e.Payload = payload
			e.Title = fmt.Sprintf("checkpoint — %s", phase)
			e.Summary = nextStep
			if e.TaskID != nil {
				e.Link = taskDetailLink(*e.TaskID)
			}
			entries = append(entries, e)
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return nil, err
		}
		rows.Close()
	}

	// Open coverage gaps written since the cursor (needs_follow_up).
	if all || want[EntryCoverageGap] {
		rows, err := d.Query(`
SELECT c.id, c.updated_at, c.task_id, c.surface, c.risk_area, COALESCE(NULLIF(c.reason,''), COALESCE(NULLIF(c.evidence,''),''))
FROM coverage_ledger c JOIN tasks t ON c.task_id=t.id
WHERE t.engagement_id=$1 AND t.deleted_at IS NULL AND c.outcome='needs_follow_up' AND c.updated_at > $2
ORDER BY c.updated_at DESC, c.id DESC LIMIT $3`, engagementID, since, timelineSourceLimit)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var e EngagementTimelineEntry
			var surface, riskArea, reason string
			if err := rows.Scan(&e.EntryID, &e.CreatedAt, &e.TaskID, &surface, &riskArea, &reason); err != nil {
				rows.Close()
				return nil, err
			}
			payload, _ := json.Marshal(map[string]any{"coverage_id": e.EntryID, "surface": surface, "risk_area": riskArea})
			e.Kind = EntryCoverageGap
			e.SourceKind = "task"
			e.Payload = payload
			if e.TaskID != nil {
				e.SourceID = *e.TaskID
				e.Link = taskDetailLink(*e.TaskID)
			}
			e.Title = fmt.Sprintf("coverage gap — %s / %s", surface, riskArea)
			e.Summary = reason
			entries = append(entries, e)
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return nil, err
		}
		rows.Close()
	}

	sortTimelineEntries(entries)
	if len(entries) > limit {
		entries = entries[:limit]
	}
	return entries, nil
}

// sortTimelineEntries orders by created_at desc, then kind, then entry_id
// desc — deterministic for identical data (brief byte-stability depends on it).
func sortTimelineEntries(entries []EngagementTimelineEntry) {
	for i := 1; i < len(entries); i++ {
		for j := i; j > 0; j-- {
			a, b := entries[j-1], entries[j]
			if a.CreatedAt.Before(b.CreatedAt) ||
				(a.CreatedAt.Equal(b.CreatedAt) && (a.Kind > b.Kind || (a.Kind == b.Kind && a.EntryID < b.EntryID))) {
				entries[j-1], entries[j] = entries[j], entries[j-1]
			} else {
				break
			}
		}
	}
}
