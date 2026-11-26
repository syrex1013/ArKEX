package server

import (
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/Autumn-27/artex/db"
)

// engagementQueueLimit caps one page of the operator queue. The queue is a "what
// needs a human now" view, not a log; anything beyond this is history that the
// timeline endpoint already serves.
const engagementQueueLimit = 200

// queueItem is one row of the union operator queue: a single thing waiting on a
// person, with enough context to decide without opening another page.
type queueItem struct {
	ID        int64     `json:"id"`
	Kind      string    `json:"kind"`
	Title     string    `json:"title"`
	Detail    string    `json:"detail"`
	TaskID    int64     `json:"task_id"`
	TaskName  string    `json:"task_name"`
	Severity  string    `json:"severity"`
	CreatedAt time.Time `json:"created_at"`
	Link      string    `json:"link"`
}

// queueCounts is the per-kind breakdown the queue header renders as badges.
type queueCounts struct {
	Decision    int `json:"decision"`
	Milestone   int `json:"milestone"`
	Finding     int `json:"finding"`
	Approval    int `json:"approval"`
	Path        int `json:"path"`
	CoverageGap int `json:"coverage_gap"`
}

// engagementLiveTasks loads the engagement's live tasks once: every query below
// scopes to this set, so the queue matches the engagement timeline's task list.
type queueTask struct {
	ID   int64
	Name string
}

// engagementTasksForQueue returns the live tasks of an engagement ordered by id.
func (s *Server) engagementTasksForQueue(engagementID int64) ([]queueTask, error) {
	rows, err := s.m.PG().Query(`SELECT id, COALESCE(NULLIF(name,''), NULLIF(description,''), 'task '||id)
FROM tasks WHERE engagement_id=$1 AND deleted_at IS NULL ORDER BY id`, engagementID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []queueTask{}
	for rows.Next() {
		var t queueTask
		if err := rows.Scan(&t.ID, &t.Name); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// engagementQueue serves GET /api/engagements/{id}/queue: every item across the
// engagement that currently needs an operator decision, newest first.
//
// The union deliberately spans five different store types because an operator does
// not care where a blocker lives — an undecided scope revision, an unverified
// finding, a validating attack path, an unanswered tool approval and an untested
// coverage gap all mean "nobody can move until a human looks".
func (s *Server) engagementQueue(w http.ResponseWriter, r *http.Request) {
	eng := s.pg(w)
	if eng == nil {
		return
	}
	id, ok := pathInt(r, "id")
	if !ok || id <= 0 {
		writeErr(w, 400, "bad engagement id")
		return
	}
	found, err := eng.GetEngagement(id)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	if found == nil {
		writeErr(w, 404, "engagement not found")
		return
	}
	tasks, err := s.engagementTasksForQueue(id)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	taskIDs := make([]int64, 0, len(tasks))
	taskNames := make(map[int64]string, len(tasks))
	for _, t := range tasks {
		taskIDs = append(taskIDs, t.ID)
		taskNames[t.ID] = t.Name
	}
	counts := queueCounts{}
	items := []queueItem{}

	// 1. Operator records still carrying a decision: open or accepted scope
	// revisions and operator decisions, plus open milestones.
	records, err := eng.ListEngagementRecords(id, nil, "", 0, 0)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	for _, rec := range records {
		switch rec.Kind {
		case db.RecordKindOperatorDecision, db.RecordKindScopeRevision:
			if rec.Status != db.RecordStatusOpen && rec.Status != db.RecordStatusAccepted {
				continue
			}
			counts.Decision++
			title := rec.Title
			if rec.Kind == db.RecordKindScopeRevision {
				title = "Scope revision: " + rec.Title
			}
			items = append(items, queueItem{
				ID: rec.ID, Kind: "decision", Title: title,
				Detail: queueDetail(rec.Body, rec.Status), Severity: "",
				CreatedAt: rec.CreatedAt, Link: queueRecordLink(id),
			})
		case db.RecordKindMilestone:
			if rec.Status != db.RecordStatusOpen {
				continue
			}
			counts.Milestone++
			detail := rec.Body
			if rec.Criteria == "" {
				detail = queueDetail(detail, "completion criteria not set")
			}
			items = append(items, queueItem{
				ID: rec.ID, Kind: "milestone", Title: rec.Title, Detail: queueDetail(detail, rec.Status),
				CreatedAt: rec.CreatedAt, Link: queueRecordLink(id),
			})
		}
	}

	if len(taskIDs) > 0 {
		pg := s.m.PG()

		// 2. Findings nobody has verified yet.
		rows, err := pg.Query(`SELECT id, task_id, COALESCE(NULLIF(name,''), vulnclass), severity, summary, created_at
FROM findings WHERE task_id = ANY($1) AND status=$2 ORDER BY created_at DESC LIMIT $3`,
			taskIDs, db.FindingPending, engagementQueueLimit)
		if err != nil {
			writeErr(w, 500, err.Error())
			return
		}
		for rows.Next() {
			var it queueItem
			var taskID int64
			if err := rows.Scan(&it.ID, &taskID, &it.Title, &it.Severity, &it.Detail, &it.CreatedAt); err != nil {
				rows.Close()
				writeErr(w, 500, err.Error())
				return
			}
			it.Kind = "finding"
			it.TaskID = taskID
			it.TaskName = taskNames[taskID]
			it.Link = queueTaskLink(taskID)
			counts.Finding++
			items = append(items, it)
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			writeErr(w, 500, err.Error())
			return
		}
		rows.Close()

		// 3. Attack paths a chain analyst is still validating.
		rows, err = pg.Query(`SELECT path.id, path.task_id, path.title, path.rationale, path.created_at
FROM attack_paths path WHERE path.task_id = ANY($1) AND path.state=$2
ORDER BY path.created_at DESC LIMIT $3`, taskIDs, db.PathStateValidating, engagementQueueLimit)
		if err != nil {
			writeErr(w, 500, err.Error())
			return
		}
		for rows.Next() {
			var it queueItem
			var taskID int64
			if err := rows.Scan(&it.ID, &taskID, &it.Title, &it.Detail, &it.CreatedAt); err != nil {
				rows.Close()
				writeErr(w, 500, err.Error())
				return
			}
			it.Kind = "path"
			it.TaskID = taskID
			it.TaskName = taskNames[taskID]
			it.Link = queueTaskLink(taskID)
			counts.Path++
			items = append(items, it)
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			writeErr(w, 500, err.Error())
			return
		}
		rows.Close()

		// 4. Coverage gaps: surfaces the swarm closed without evidence.
		rows, err = pg.Query(`SELECT ledger.id, ledger.task_id, ledger.surface, ledger.risk_area, ledger.reason, ledger.updated_at
FROM coverage_ledger ledger WHERE ledger.task_id = ANY($1) AND ledger.outcome=$2
ORDER BY ledger.updated_at DESC LIMIT $3`, taskIDs, db.OutcomeNeedsFollowUp, engagementQueueLimit)
		if err != nil {
			writeErr(w, 500, err.Error())
			return
		}
		for rows.Next() {
			var it queueItem
			var taskID int64
			var surface, riskArea, reason string
			if err := rows.Scan(&it.ID, &taskID, &surface, &riskArea, &reason, &it.CreatedAt); err != nil {
				rows.Close()
				writeErr(w, 500, err.Error())
				return
			}
			it.Kind = "coverage_gap"
			it.Title = surface + " / " + riskArea
			it.Detail = reason
			it.TaskID = taskID
			it.TaskName = taskNames[taskID]
			it.Link = queueTaskLink(taskID)
			counts.CoverageGap++
			items = append(items, it)
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			writeErr(w, 500, err.Error())
			return
		}
		rows.Close()

		// 5. Tool approvals a worker is parked on. task_id is TEXT there, so the
		// numeric guard keeps a non-numeric row from failing the whole cast.
		rows, err = pg.Query(`SELECT pending.id, pending.task_id::bigint, pending.tool_name, pending.agent_name, pending.created_at
FROM intercept_pending pending
JOIN tasks task ON task.id = pending.task_id::bigint
WHERE pending.status='pending' AND task.deleted_at IS NULL
  AND task.id = ANY($1) AND pending.task_id ~ '^[0-9]+$'
ORDER BY pending.created_at DESC LIMIT $2`, taskIDs, engagementQueueLimit)
		if err != nil {
			writeErr(w, 500, err.Error())
			return
		}
		for rows.Next() {
			var it queueItem
			var taskID int64
			var tool, agent string
			if err := rows.Scan(&it.ID, &taskID, &tool, &agent, &it.CreatedAt); err != nil {
				rows.Close()
				writeErr(w, 500, err.Error())
				return
			}
			it.Kind = "approval"
			it.Title = tool
			it.Detail = queueDetail(agent, "pending approval")
			it.TaskID = taskID
			it.TaskName = taskNames[taskID]
			it.Link = queueTaskLink(taskID)
			counts.Approval++
			items = append(items, it)
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			writeErr(w, 500, err.Error())
			return
		}
		rows.Close()
	}

	// One deterministic order for every source: newest first, kind breaks ties so
	// a finding and a decision created in the same second never swap places.
	sort.SliceStable(items, func(i, j int) bool {
		if !items[i].CreatedAt.Equal(items[j].CreatedAt) {
			return items[i].CreatedAt.After(items[j].CreatedAt)
		}
		if items[i].Kind != items[j].Kind {
			return items[i].Kind < items[j].Kind
		}
		return items[i].ID > items[j].ID
	})
	if len(items) > engagementQueueLimit {
		items = items[:engagementQueueLimit]
	}
	writeJSON(w, 200, map[string]any{"items": items, "counts": counts})
}

// queueDetail trims a free-text field onto one line so the queue stays scannable.
func queueDetail(text, fallback string) string {
	text = strings.TrimSpace(strings.Join(strings.Fields(text), " "))
	if text == "" {
		text = fallback
	}
	if runes := []rune(text); len(runes) > 240 {
		text = string(runes[:240]) + "…"
	}
	return text
}

// queueTaskLink deep-links the task detail page (query param id, not a path segment).
func queueTaskLink(taskID int64) string {
	return "/function/tasks/detail?id=" + strconv.FormatInt(taskID, 10)
}

// queueRecordLink deep-links the engagement review surface for record-backed items.
func queueRecordLink(engagementID int64) string {
	return "/function/engagements?id=" + strconv.FormatInt(engagementID, 10)
}
