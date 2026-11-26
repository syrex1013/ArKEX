package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"

	"github.com/Autumn-27/artex/agent"
	"github.com/Autumn-27/artex/db"
)

const maxBatchControlIDs = 100

type taskControlResult struct {
	ID     string `json:"id"`
	Paused bool   `json:"paused"`
	Queued bool   `json:"queued"`
	Status string `json:"status"`
}

type intentControlResult struct {
	ID      int64             `json:"id"`
	State   string            `json:"state"`
	Deleted *db.IntentCleanup `json:"deleted,omitempty"`
}

// parsedTaskID carries one requested batch id together with whether it parsed.
// Invalid ids are kept rather than dropped so the response can name them.
type parsedTaskID struct {
	id    string
	valid bool
}

// normalizeBatchTaskIDs trims, canonicalizes and de-duplicates the ids of one
// batch request while preserving the caller's order. Shared by every batch
// endpoint so they agree on what counts as a duplicate.
func normalizeBatchTaskIDs(raw []string) []parsedTaskID {
	seen := map[string]bool{}
	seenInvalid := map[string]bool{}
	taskIDs := make([]parsedTaskID, 0, len(raw))
	for _, item := range raw {
		trimmed := strings.TrimSpace(item)
		id, valid := canonicalTaskID(trimmed)
		if !valid {
			if seenInvalid[trimmed] {
				continue
			}
			seenInvalid[trimmed] = true
			taskIDs = append(taskIDs, parsedTaskID{id: trimmed})
			continue
		}
		if seen[id] {
			continue
		}
		seen[id] = true
		taskIDs = append(taskIDs, parsedTaskID{id: id, valid: true})
	}
	return taskIDs
}

type batchControlItem struct {
	ID     string `json:"id"`
	OK     bool   `json:"ok"`
	Status string `json:"status,omitempty"`
	Queued bool   `json:"queued,omitempty"`
	Error  string `json:"error,omitempty"`
}

func (s *Server) resumeAdmissionMode(t *Task) string {
	if t == nil {
		return "resume"
	}
	lifecycle := t.lifecycleSnapshot()
	if lifecycle.QueueMode == "bootstrap" {
		return "bootstrap"
	}
	if lifecycle.FirstRunAt == 0 {
		goals, err := t.Store.ListByKind(db.KindGoal, 1)
		if err == nil && len(goals) == 0 {
			return "bootstrap"
		}
	}
	return "resume"
}

// applyTaskControl is shared by the single and batch endpoints. Task resume is
// deliberately limited to paused tasks; reruns and finding follow-ups use
// admitTask directly when they need to revive a terminal task.
func (s *Server) applyTaskControl(t *Task, action string) (taskControlResult, error) {
	return s.applyTaskControlWithCause(t, action, agent.AbortPausedByUser)
}

func (s *Server) applyTaskControlWithCause(t *Task, action string, pauseCause error) (taskControlResult, error) {
	if t == nil {
		return taskControlResult{}, fmt.Errorf("task not found")
	}
	out := taskControlResult{ID: t.ID}
	switch action {
	case "pause":
		s.concMu.Lock()
		defer s.concMu.Unlock()
		current, exists := s.m.Task(t.ID)
		if !exists || current != t || s.engine.IsDeleting(t.ID) {
			return out, fmt.Errorf("task is being deleted, control unavailable")
		}
		if !s.engine.beginTaskOperation(t.ID) {
			return out, fmt.Errorf("task is being deleted, control unavailable")
		}
		defer s.engine.decInflight(t.ID)
		lifecycle := t.lifecycleSnapshot()
		if isTerminalStatus(lifecycle.Status) {
			return out, fmt.Errorf("cannot pause a task in a terminal state")
		}
		if lifecycle.Paused {
			return out, fmt.Errorf("task is already paused")
		}
		wasQueued := lifecycle.Queued
		wasEnginePaused := s.engine.IsPaused(t.ID)
		if pauseCause == nil {
			pauseCause = agent.AbortPausedByUser
		}
		s.engine.Pause(t.ID, pauseCause)
		if err := s.m.ApplyTaskPause(t.ID); err != nil {
			if !wasEnginePaused && !wasQueued {
				s.engine.Resume(t)
			}
			return out, err
		}
		// Main Agent is independently cancellable. Only cancel its current turn
		// after the persistent pause commits, so a failed control request is fully
		// compensated and does not lose an otherwise valid conversation turn.
		s.cancelTaskChat(t.ID, agent.AbortChatPausedWithTask)
		out.Paused, out.Status = true, "paused"
		go s.reconcileConcurrency()
	case "resume":
		queued, err := s.admitPausedTask(t)
		if err != nil {
			return out, err
		}
		out.Queued = queued
		out.Status = map[bool]string{true: "queued", false: "running"}[queued]
	default:
		return out, fmt.Errorf("action must be pause|resume")
	}
	log.Printf("[task] #%s %s", t.ID, map[string]string{"pause": "paused", "resume": "resumed"}[action])
	return out, nil
}

// intentSummaryOf extracts the summary from the intent payload so the deletion notification has a record before the intent node disappears (hard delete).
func intentSummaryOf(n *db.Node) string {
	if n == nil {
		return ""
	}
	var p map[string]any
	if json.Unmarshal(n.Payload, &p) == nil {
		if s, ok := p["summary"].(string); ok {
			return s
		}
	}
	return ""
}

func (s *Server) applyIntentControl(ctx context.Context, t *Task, iid int64, action, reason, mode string) (intentControlResult, error) {
	out := intentControlResult{ID: iid}
	node, err := t.Store.GetNode(iid)
	if err != nil {
		return out, err
	}
	if node == nil {
		if inherited, sourceErr := t.Store.GetNodeWithSources(iid); sourceErr == nil && inherited != nil && inherited.Inherited {
			return out, fmt.Errorf("inherited intents are read-only and cannot be controlled")
		}
		return out, fmt.Errorf("intent not found")
	}
	if node.Kind != db.KindIntent {
		return out, fmt.Errorf("node is not an intent")
	}
	switch action {
	case "pause":
		if node.State != "running" {
			return out, fmt.Errorf("only running intents can be paused")
		}
		if err := s.engine.ControlWork(ctx, iid, "pause"); err != nil {
			return out, err
		}
		out.State = "paused"
	case "resume":
		if node.State != "paused" {
			return out, fmt.Errorf("only paused intents can be resumed")
		}
		changed, err := t.Store.CompareAndSetIntentState(iid, "paused", "open")
		if err != nil {
			return out, err
		}
		if !changed {
			return out, fmt.Errorf("%w: intent is no longer in the paused state", db.ErrIntentStateConflict)
		}
		t.Notify()
		out.State = "open"
	case "cancel":
		// Deletion supports two modes:
		//   soft (default, soft delete): the intent moves to state='deleted', the deletion
		//     reason goes into the delete_reason field; the intent node and all outputs/lineage are kept, and no extra fact is hung on the graph.
		//   hard (hard delete): physically deletes the intent and the exclusive descendants "supported only by it" (cascading to leaves),
		//     avoiding orphaned data; shared nodes, goals, and the task root fact are kept.
		// Both modes use the cancelled trigger to inform the planner (intent content + deletion reason) so it can replan accordingly.
		if node.State != "running" && node.State != "paused" && node.State != "open" {
			return out, fmt.Errorf("only open/running/paused intents can be deleted")
		}
		reason = strings.TrimSpace(reason)
		if reason == "" {
			return out, fmt.Errorf("a deletion reason is required")
		}
		if node.State == "running" {
			if err := s.engine.ControlWork(ctx, iid, "cancel"); err != nil {
				return out, err
			}
		}
		summary := intentSummaryOf(node)
		if mode == "hard" {
			cleanup, err := t.Store.CancelIntent(iid)
			if err != nil {
				return out, err
			}
			s.cancelWorkerSide(t.ID, t.ExpID, iid)
			t.NotifyCancelled(iid, summary, reason)
			out.Deleted = &cleanup
			out.State = "" // node deleted; the frontend removes it from the list via Deleted
		} else {
			if _, err := t.Store.SoftDeleteIntent(iid, reason); err != nil {
				return out, err
			}
			s.cancelWorkerSide(t.ID, t.ExpID, iid)
			t.NotifyCancelled(iid, summary, reason)
			out.State = db.StateIntentDeleted
		}
	default:
		return out, fmt.Errorf("action must be pause|resume|cancel")
	}
	return out, nil
}

func (s *Server) controlTasksBatch(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 256<<10)
	var req struct {
		TaskIDs []string `json:"task_ids"`
		Action  string   `json:"action"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, 400, "bad json: "+err.Error())
		return
	}
	if req.Action != "pause" && req.Action != "resume" {
		writeErr(w, 400, "action must be pause|resume")
		return
	}
	taskIDs := normalizeBatchTaskIDs(req.TaskIDs)
	if len(taskIDs) == 0 || len(taskIDs) > maxBatchControlIDs {
		writeErr(w, 400, fmt.Sprintf("task_ids count must be 1-%d", maxBatchControlIDs))
		return
	}
	items := make([]batchControlItem, 0, len(taskIDs))
	for _, parsed := range taskIDs {
		item := batchControlItem{ID: parsed.id}
		if !parsed.valid {
			item.Error = "bad task id"
			items = append(items, item)
			continue
		}
		t, ok := s.m.Task(parsed.id)
		if !ok {
			item.Error = "task not found"
			items = append(items, item)
			continue
		}
		result, err := s.applyTaskControl(t, req.Action)
		if err != nil {
			item.Error = err.Error()
		} else {
			item.OK, item.Status, item.Queued = true, result.Status, result.Queued
		}
		items = append(items, item)
	}
	writeJSON(w, 200, map[string]any{"items": items})
}

// rerunTask revives a terminal task (done/failed/timeout) through the shared
// admission path. Pause/resume stay limited to non-terminal tasks; this is the
// explicit operator action for "run it again".
func (s *Server) rerunTask(w http.ResponseWriter, r *http.Request) {
	t, ok := s.m.Task(r.PathValue("id"))
	if !ok {
		writeErr(w, 404, "task not found")
		return
	}
	lifecycle := t.lifecycleSnapshot()
	if !isTerminalStatus(lifecycle.Status) {
		writeErr(w, 409, "only a completed, failed, or timed-out task can be rerun")
		return
	}
	if err := prepareTaskRerun(t); err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	queued, err := s.admitTask(t, "resume")
	if err != nil {
		writeErr(w, 409, err.Error())
		return
	}
	// Wake planner after status is already running (or queued-but-running). Doing
	// this before admit races a terminal round that returns early; after admit,
	// Resume/Run have live loops and the hint survives a paused FIFO wait.
	notifyTaskRerun(t)
	status := "running"
	if queued {
		status = "queued"
	}
	log.Printf("[task] #%s rerun (%s)", t.ID, status)
	writeJSON(w, 200, taskControlResult{ID: t.ID, Queued: queued, Status: status})
}

// prepareTaskRerun reopens settled graph work before admission so a completed
// task cannot flip running→done on the first planner wake with an empty frontier.
func prepareTaskRerun(t *Task) error {
	if t == nil || t.Store == nil {
		return fmt.Errorf("task store unavailable")
	}
	if _, err := t.Store.ReopenMetGoals(); err != nil {
		return err
	}
	openGoal, err := t.Store.HasOpenGoal()
	if err != nil {
		return err
	}
	if !openGoal {
		if _, err := t.Store.ReopenTerminalIntents(); err != nil {
			return err
		}
	}
	return nil
}

// notifyTaskRerun records the operator rerun hint and nudges the planner/workers.
// Call only after admitTask so the task is no longer terminal.
func notifyTaskRerun(t *Task) {
	if t == nil {
		return
	}
	t.NotifyHint("human", []string{
		"Operator requested a full task rerun. Re-explore open goals with fresh intents; do not prove_goal from prior-run evidence alone — dispatch verification or exploration intents first.",
	})
}

// duplicateTask creates a fresh task with the same brief and settings as the
// source. Graph history is not copied; credentials and task-level intercept
// rules are. The new task launches like a normal create.
func (s *Server) duplicateTask(w http.ResponseWriter, r *http.Request) {
	src, ok := s.m.Task(r.PathValue("id"))
	if !ok {
		writeErr(w, 404, "task not found")
		return
	}
	life := src.lifecycleSnapshot()
	llm := src.llmStateSnapshot()
	name := strings.TrimSpace(life.Name)
	if name == "" {
		name = strings.TrimSpace(src.Description)
	}
	if name != "" {
		name = name + " (copy)"
	}
	quiet := src.PlanQuietMode
	coverage := src.CoverageEnabled
	rotate := llm.Rotate
	gate := src.targetGate.Load()

	srcID, _ := strconv.ParseInt(src.ID, 10, 64)
	var interceptRules []db.TaskInterceptRuleInput
	if as := s.m.Assets(); as != nil && srcID > 0 {
		rules, err := as.ListTaskInterceptRules(srcID)
		if err != nil {
			writeErr(w, 500, err.Error())
			return
		}
		for _, rule := range rules {
			interceptRules = append(interceptRules, db.TaskInterceptRuleInput{
				Enabled: rule.Enabled,
				Action:  rule.Action,
				Kind:    rule.Kind,
				Pattern: rule.Pattern,
				Note:    rule.Note,
			})
		}
	}

	var creds []db.TaskCredentialInput
	if srcID > 0 {
		rows, err := s.m.PG().TaskCredentials(srcID)
		if err != nil {
			writeErr(w, 500, err.Error())
			return
		}
		for _, c := range rows {
			creds = append(creds, db.TaskCredentialInput{
				Domain: c.Domain, Username: c.Username, Password: c.Password, Source: "user", Note: c.Note,
			})
		}
	}

	t, err := s.m.CreateTaskWithOptions(src.Description, src.Goal, db.TaskCreateOptions{
		Name: name, CategoryID: life.CategoryID, EngagementID: life.EngagementID,
		CompanyIDs: append([]int64(nil), life.CompanyIDs...), LLMProfileIDs: append([]int64(nil), llm.ProfileIDs...),
		TimeoutSeconds: src.TimeoutSeconds, PlanHeartbeatSeconds: src.PlanHeartbeatSeconds,
		PlanQuietMode: &quiet, CoverageEnabled: &coverage, InterceptRules: interceptRules,
		LLMRotate: &rotate, TargetGate: &gate,
	})
	if err != nil {
		if errors.Is(err, db.ErrTaskCategoryInvalid) || errors.Is(err, db.ErrTaskCategoryNotFound) {
			writeErr(w, 400, "task category not found or invalid")
			return
		}
		if errors.Is(err, db.ErrTaskCompanyIDsInvalid) || errors.Is(err, db.ErrTaskCompanyNotFound) {
			writeErr(w, 400, "linked company not found or invalid")
			return
		}
		if errors.Is(err, db.ErrEngagementNotFound) {
			writeErr(w, 400, "engagement not found")
			return
		}
		if errors.Is(err, db.ErrEngagementInvalid) {
			writeErr(w, 400, "invalid engagement")
			return
		}
		writeErr(w, 500, err.Error())
		return
	}
	if len(creds) > 0 {
		taskNum, _ := strconv.ParseInt(t.ID, 10, 64)
		for _, c := range creds {
			if _, err := s.m.PG().UpsertTaskCredential(taskNum, c); err != nil {
				writeErr(w, 400, "invalid credential for "+c.Domain+": "+err.Error())
				return
			}
		}
	}
	log.Printf("[task] duplicated #%s → #%s", src.ID, t.ID)
	s.launchTask(t, src.Description+" "+src.Goal, false)
	writeJSON(w, 201, taskDTO(t, s.resolvedTaskStatus(t)))
}
