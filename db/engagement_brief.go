package db

// The deterministic engagement brief: one structured snapshot an operator
// (or a session-start agent brief) can read without an LLM in the loop.
// Same data in, byte-identical brief out — ordering is fixed everywhere.

import (
	"context"
	"database/sql"
	"sort"
	"time"
)

// BriefTask pairs one live engagement task with its outcome card.
type BriefTask struct {
	TaskID  int64       `json:"task_id"`
	Name    string      `json:"name"`
	Status  string      `json:"status"`
	Link    string      `json:"link"`
	Outcome TaskOutcome `json:"outcome"`
}

// BriefSeverity is the engagement-wide finding split (confirmed-only for
// severities, mirroring the per-task outcome card).
type BriefSeverity struct {
	Total         int `json:"total"`
	Confirmed     int `json:"confirmed"`
	Open          int `json:"open"`
	FalsePositive int `json:"false_positive"`
	Duplicate     int `json:"duplicate"`
	Critical      int `json:"critical"`
	High          int `json:"high"`
	Medium        int `json:"medium"`
	Low           int `json:"low"`
}

// BriefCoverageGap is one unresolved coverage_ledger needs_follow_up row.
type BriefCoverageGap struct {
	TaskID    int64     `json:"task_id"`
	Surface   string    `json:"surface"`
	RiskArea  string    `json:"risk_area"`
	Evidence  string    `json:"evidence"`
	Reason    string    `json:"reason"`
	Worker    string    `json:"worker"`
	UpdatedAt time.Time `json:"updated_at"`
	Link      string    `json:"link"`
}

// EngagementBrief is the engagement handoff brief. Every slice has a fixed
// order so the same data always marshals to the same JSON.
type EngagementBrief struct {
	EngagementID   int64              `json:"engagement_id"`
	Name           string             `json:"name"`
	GeneratedAt    time.Time          `json:"generated_at"`
	Review         *EngagementReview  `json:"review,omitempty"`
	RecentRecords  []EngagementRecord `json:"recent_records"`
	ActiveScope    *EngagementRecord  `json:"active_scope,omitempty"`
	OpenDecisions  []EngagementRecord `json:"open_decisions"`
	OpenMilestones []EngagementRecord `json:"open_milestones"`
	Tasks          []BriefTask        `json:"tasks"`
	Severity       BriefSeverity      `json:"severity"`
	CoverageGaps   []BriefCoverageGap `json:"coverage_gaps"`
}

// EngagementBrief assembles the brief. operator is optional: when set the
// Review/RecentRecords section is the "changes since my last review" delta
// for that operator; without it RecentRecords holds all records.
func (d *DB) EngagementBrief(ctx context.Context, engagementID int64, operator string) (*EngagementBrief, error) {
	b := &EngagementBrief{EngagementID: engagementID, GeneratedAt: time.Now().UTC()}

	if err := d.QueryRowContext(ctx, `SELECT name FROM engagements WHERE id=$1`, engagementID).Scan(&b.Name); err != nil {
		return nil, err
	}

	if operator != "" {
		rv, err := d.LatestEngagementReview(engagementID, operator)
		if err != nil {
			return nil, err
		}
		if rv != nil {
			b.Review = rv
			recent, err := d.RecordsSinceID(engagementID, rv.UpToRecordID)
			if err != nil {
				return nil, err
			}
			if recent == nil {
				recent = []EngagementRecord{} // reviewed everything != never reviewed
			}
			b.RecentRecords = recent
		}
	}
	if b.RecentRecords == nil {
		recent, err := d.ListEngagementRecords(engagementID, nil, "", 0, 100)
		if err != nil {
			return nil, err
		}
		if recent == nil {
			recent = []EngagementRecord{}
		}
		b.RecentRecords = recent
	}

	// Active scope revision: latest accepted.
	var active EngagementRecord
	err := d.QueryRowContext(ctx, `SELECT `+engagementRecordCols+` FROM engagement_records
WHERE engagement_id=$1 AND kind=$2 AND status=$3 ORDER BY id DESC LIMIT 1`,
		engagementID, RecordKindScopeRevision, RecordStatusAccepted).
		Scan(scanEngagementRecordFields(&active)...)
	switch err {
	case nil:
		b.ActiveScope = &active
	case sql.ErrNoRows:
		// no accepted revision yet
	default:
		return nil, err
	}

	// Open + accepted operator decisions, newest first.
	decisions, err := d.ListEngagementRecords(engagementID, []string{RecordKindOperatorDecision}, "", 0, 0)
	if err != nil {
		return nil, err
	}
	for _, r := range decisions {
		if r.Status == RecordStatusOpen || r.Status == RecordStatusAccepted {
			b.OpenDecisions = append(b.OpenDecisions, r)
		}
	}

	// Open milestones: due first, undated last, then oldest first.
	milestones, err := d.ListEngagementRecords(engagementID, []string{RecordKindMilestone}, RecordStatusOpen, 0, 0)
	if err != nil {
		return nil, err
	}
	sortBriefMilestones(milestones)
	b.OpenMilestones = milestones

	// Live tasks with outcome cards, id order.
	trows, err := d.QueryContext(ctx, `SELECT id, COALESCE(name,''), COALESCE(description,''), status
FROM tasks WHERE engagement_id=$1 AND deleted_at IS NULL ORDER BY id`, engagementID)
	if err != nil {
		return nil, err
	}
	defer trows.Close()
	type taskMeta struct {
		id          int64
		name        string
		description string
		status      string
	}
	var metas []taskMeta
	for trows.Next() {
		var m taskMeta
		if err := trows.Scan(&m.id, &m.name, &m.description, &m.status); err != nil {
			return nil, err
		}
		metas = append(metas, m)
	}
	if err := trows.Err(); err != nil {
		return nil, err
	}
	for _, m := range metas {
		outcome, err := d.TaskOutcome(ctx, m.id)
		if err != nil {
			return nil, err
		}
		b.Tasks = append(b.Tasks, BriefTask{
			TaskID:  m.id,
			Name:    taskDisplayName(m.name, m.description),
			Status:  m.status,
			Link:    taskDetailLink(m.id),
			Outcome: outcome,
		})
	}

	// Engagement-wide severity split (confirmed-only severities).
	err = d.QueryRowContext(ctx, `SELECT COUNT(*),
	       COUNT(*) FILTER (WHERE status IN ('confirmed','resolved','fixed')),
	       COUNT(*) FILTER (WHERE status IN ('pending','in_progress')),
	       COUNT(*) FILTER (WHERE status='false_positive'),
	       COUNT(*) FILTER (WHERE status='duplicate'),
	       COUNT(*) FILTER (WHERE status IN ('confirmed','resolved','fixed') AND severity='critical'),
	       COUNT(*) FILTER (WHERE status IN ('confirmed','resolved','fixed') AND severity='high'),
	       COUNT(*) FILTER (WHERE status IN ('confirmed','resolved','fixed') AND severity='medium'),
	       COUNT(*) FILTER (WHERE status IN ('confirmed','resolved','fixed') AND severity='low')
FROM findings WHERE task_id IN (SELECT id FROM tasks WHERE engagement_id=$1 AND deleted_at IS NULL)`,
		engagementID).Scan(
		&b.Severity.Total, &b.Severity.Confirmed, &b.Severity.Open,
		&b.Severity.FalsePositive, &b.Severity.Duplicate,
		&b.Severity.Critical, &b.Severity.High, &b.Severity.Medium, &b.Severity.Low)
	if err != nil {
		return nil, err
	}

	// Unresolved coverage gaps.
	grows, err := d.QueryContext(ctx, `SELECT c.task_id, c.surface, c.risk_area, COALESCE(c.evidence,''),
COALESCE(c.reason,''), COALESCE(c.worker,''), c.updated_at
FROM coverage_ledger c
WHERE c.outcome='needs_follow_up'
  AND c.task_id IN (SELECT id FROM tasks WHERE engagement_id=$1 AND deleted_at IS NULL)
ORDER BY c.updated_at DESC, c.task_id, c.surface, c.risk_area`, engagementID)
	if err != nil {
		return nil, err
	}
	defer grows.Close()
	for grows.Next() {
		var g BriefCoverageGap
		if err := grows.Scan(&g.TaskID, &g.Surface, &g.RiskArea, &g.Evidence, &g.Reason, &g.Worker, &g.UpdatedAt); err != nil {
			return nil, err
		}
		g.Link = taskDetailLink(g.TaskID)
		b.CoverageGaps = append(b.CoverageGaps, g)
	}
	return b, grows.Err()
}

// sortBriefMilestones orders milestones by due date (undated last), then id.
func sortBriefMilestones(ms []EngagementRecord) {
	sort.SliceStable(ms, func(i, j int) bool {
		a, b := ms[i], ms[j]
		aDated, bDated := a.DueAt != nil, b.DueAt != nil
		if aDated != bDated {
			return aDated
		}
		if aDated && bDated && !a.DueAt.Equal(*b.DueAt) {
			return a.DueAt.Before(*b.DueAt)
		}
		return a.ID < b.ID
	})
}

// scanEngagementRecordFields returns Scan destinations in column order.
func scanEngagementRecordFields(r *EngagementRecord) []any {
	return []any{&r.ID, &r.EngagementID, &r.Kind, &r.Title, &r.Body, &r.Actor, &r.Status,
		&r.DueAt, &r.CompletedAt, &r.Criteria, &r.SupersedesID, &r.SourceKind, &r.SourceID, &r.Payload,
		&r.CreatedAt, &r.UpdatedAt}
}
