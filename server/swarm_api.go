package server

// Swarm HTTP surface: the long-horizon swarm observability endpoints (roster,
// attack paths, project knowledge, progress) and the live system-event stream.
// All reads go through the db stores; nothing here mutates swarm state.

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/Autumn-27/artex/db"
)

// swarmTaskID resolves the task path value to a numeric task id. Unknown ids 404.
func (s *Server) swarmTaskID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	t := s.m.ResolveTask(r.PathValue("id"))
	if t == nil {
		writeErr(w, 404, "task not found")
		return 0, false
	}
	id, err := strconv.ParseInt(t.ID, 10, 64)
	if err != nil || id <= 0 {
		writeErr(w, 500, "task id is not numeric")
		return 0, false
	}
	return id, true
}

// intentsByState counts the task's intents per state.
func (s *Server) intentsByState(expID int64) (map[string]int, error) {
	rows, err := s.m.PG().Query(`SELECT state, count(*) FROM exploration_nodes
WHERE kind='intent' AND exploration_id=$1 GROUP BY state`, expID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]int{}
	for rows.Next() {
		var st string
		var n int
		if err := rows.Scan(&st, &n); err != nil {
			return nil, err
		}
		out[st] = n
	}
	return out, rows.Err()
}

// confidenceBuckets buckets the task's findings by confidence (0-24/25-49/50-74/75-100).
func (s *Server) confidenceBuckets(taskID int64) (map[string]int, error) {
	rows, err := s.m.PG().Query(`SELECT CASE
  WHEN confidence < 25 THEN '0-24'
  WHEN confidence < 50 THEN '25-49'
  WHEN confidence < 75 THEN '50-74'
  ELSE '75-100' END, count(*)
FROM findings WHERE task_id=$1 GROUP BY 1`, taskID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]int{}
	for rows.Next() {
		var b string
		var n int
		if err := rows.Scan(&b, &n); err != nil {
			return nil, err
		}
		out[b] = n
	}
	return out, rows.Err()
}

// swarmSettledIntentStates are this repo's terminal intent states. The engine
// settles an intent to one of done/blocked/exhausted/stopped (db/exploration.go:
// ReopenTerminalIntents calls them "settled intents"); 'deleted' is the soft-delete
// state (exploration_nodes.delete_reason) and is not open work either. Everything
// else in the CHECK of exploration_nodes — open, running, paused — is still open.
var swarmSettledIntentStates = []string{"done", "blocked", "exhausted", "stopped", "deleted"}

// swarmVerifiedFindingStatuses are the findings.status values the repo already
// treats as a settled, positive result (db/lessons.go mirrors exactly this set into
// positive RSI evidence, db/engagement_brief.go into the confirmed severity split).
// There is no 'verified' finding status: verification lives in
// findings.verification_status, a different axis.
var swarmVerifiedFindingStatuses = []string{db.FindingConfirmed, db.FindingResolved, db.FindingFixed}

// swarmStaleHeartbeat is the age at which a working agent reads as stale in this
// operator view. The engine reclaims an abandoned claim much sooner
// (db.IntentClaimTTL, 90s); the explainability signal is deliberately coarser so a
// normal LLM round never flashes "stale".
const swarmStaleHeartbeat = 5 * time.Minute

// swarmIntentRef is the intent an agent currently holds.
type swarmIntentRef struct {
	ID        int64     `json:"id"`
	Title     string    `json:"title"`
	Kind      string    `json:"kind"`
	State     string    `json:"state"`
	CreatedAt time.Time `json:"created_at"`
}

// swarmCheckpointRef is the newest checkpoint of the agent's current intent.
type swarmCheckpointRef struct {
	Phase     string    `json:"phase"`
	NextStep  string    `json:"next_step"`
	CreatedAt time.Time `json:"created_at"`
}

// swarmAgentDetail explains one roster slot: what it holds, what it last saved,
// and — when it is not making progress — why.
type swarmAgentDetail struct {
	AgentID        int64               `json:"agent_id"`
	Name           string              `json:"name"`
	Role           string              `json:"role"`
	State          string              `json:"state"`
	CurrentIntent  *swarmIntentRef     `json:"current_intent"`
	LastCheckpoint *swarmCheckpointRef `json:"last_checkpoint"`
	WaitReason     string              `json:"wait_reason"`
	IntentsDone    int                 `json:"intents_done"`
	Findings       int                 `json:"findings"`
	LastEventAt    *time.Time          `json:"last_event_at"`
}

// swarmProgress is the task-level rollup the swarm view shows above the roster.
type swarmProgress struct {
	IntentsSettled   int `json:"intents_settled"`
	IntentsOpen      int `json:"intents_open"`
	FindingsVerified int `json:"findings_verified"`
	CoverageGaps     int `json:"coverage_gaps"`
	BlockedAgents    int `json:"blocked_agents"`
}

// taskBlockerTask returns the live dependent task that blocks taskID, or 0 when
// nothing blocks it. Same SQL shape as db's archiveBlockerScope (unexported there):
// a child task of the relation that is not deleted and not already being archived.
func (s *Server) taskBlockerTask(taskID int64) (int64, error) {
	var dependent sql.NullInt64
	err := s.m.PG().QueryRow(`
SELECT MIN(child.id)
FROM task_relations relation
JOIN tasks child ON child.id=relation.task_id AND child.deleted_at IS NULL
LEFT JOIN task_archives pending ON pending.task_id=child.id
WHERE (pending.id IS NULL OR pending.state NOT IN ('archive_queued','archiving'))
  AND relation.source_task_id=$1`, taskID).Scan(&dependent)
	if err != nil {
		return 0, err
	}
	return dependent.Int64, nil
}

// taskAwaitsApproval reports whether a task approval request is still waiting for
// an operator. 'pending' is the only unresolved intercept_pending status: rule and
// model verdicts are inserted already decided, so a pending row always means "no
// human has answered yet". task_id is TEXT in that table, hence the string compare.
func (s *Server) taskAwaitsApproval(taskID int64) (bool, error) {
	var waiting bool
	err := s.m.PG().QueryRow(`SELECT EXISTS (
SELECT 1 FROM intercept_pending WHERE COALESCE(task_id,'')=$1 AND status='pending')`,
		strconv.FormatInt(taskID, 10)).Scan(&waiting)
	return waiting, err
}

// roundedAge renders an elapsed time as an operator-readable span ("12m", "1h05m").
func roundedAge(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	d = d.Round(time.Minute)
	if h := int(d / time.Hour); h > 0 {
		return fmt.Sprintf("%dh%02dm", h, int(d.Minutes())%60)
	}
	return fmt.Sprintf("%dm", int(d.Minutes()))
}

// swarmWaitReason explains, in precedence order, why an agent is not currently
// making progress: a task-relation block, a pending operator approval, a stale
// heartbeat, or plain idleness. "" means the agent is actively working.
func swarmWaitReason(a *db.TaskAgent, blocker int64, awaitingApproval bool, now time.Time) string {
	if a.CurrentIntentID != nil && blocker > 0 {
		return fmt.Sprintf("blocked by task %d", blocker)
	}
	if awaitingApproval {
		return "waiting on operator approval"
	}
	if a.State == db.AgentStateWorking {
		if a.LastHeartbeatAt == nil {
			return "heartbeat stale unknown"
		}
		if age := now.Sub(*a.LastHeartbeatAt); age > swarmStaleHeartbeat {
			return "heartbeat stale " + roundedAge(age)
		}
	}
	if a.State == db.AgentStateIdle || a.CurrentIntentID == nil {
		return "idle"
	}
	return ""
}

// taskAgentsDetail builds the per-agent explanation for the roster.
func (s *Server) taskAgentsDetail(taskID int64, roster []*db.TaskAgent) ([]swarmAgentDetail, error) {
	pg := s.m.PG()
	out := make([]swarmAgentDetail, 0, len(roster))
	if len(roster) == 0 {
		return out, nil
	}

	// Current intents in one round trip. exploration_nodes has no name column: an
	// intent's human title is payload->>'summary' (same field the planners and the
	// page filters use).
	intentIDs := make([]int64, 0, len(roster))
	for _, a := range roster {
		if a.CurrentIntentID != nil {
			intentIDs = append(intentIDs, *a.CurrentIntentID)
		}
	}
	intents := map[int64]*swarmIntentRef{}
	if len(intentIDs) > 0 {
		rows, err := pg.Query(`SELECT id, COALESCE(payload->>'summary',''), kind, state, created_at
FROM exploration_nodes WHERE id = ANY($1)`, intentIDs)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		for rows.Next() {
			var it swarmIntentRef
			if err := rows.Scan(&it.ID, &it.Title, &it.Kind, &it.State, &it.CreatedAt); err != nil {
				rows.Close()
				return nil, err
			}
			intents[it.ID] = &it
		}
		if err := rows.Err(); err != nil {
			return nil, err
		}
	}

	// Newest checkpoint per current intent.
	checkpoints := map[int64]*swarmCheckpointRef{}
	if len(intentIDs) > 0 {
		rows, err := pg.Query(`SELECT DISTINCT ON (intent_id) intent_id, phase, next_step, created_at
FROM intent_checkpoints WHERE task_id=$1 AND intent_id = ANY($2)
ORDER BY intent_id, id DESC`, taskID, intentIDs)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		for rows.Next() {
			var intentID int64
			var cp swarmCheckpointRef
			if err := rows.Scan(&intentID, &cp.Phase, &cp.NextStep, &cp.CreatedAt); err != nil {
				rows.Close()
				return nil, err
			}
			checkpoints[intentID] = &cp
		}
		if err := rows.Err(); err != nil {
			return nil, err
		}
	}

	// Newest lifecycle event per agent name.
	lastEvents := map[string]time.Time{}
	rows, err := pg.Query(`SELECT agent_name, max(created_at) FROM agent_events
WHERE task_id=$1 GROUP BY agent_name`, taskID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var name string
		var at time.Time
		if err := rows.Scan(&name, &at); err != nil {
			rows.Close()
			return nil, err
		}
		lastEvents[name] = at
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	blocker, err := s.taskBlockerTask(taskID)
	if err != nil {
		return nil, err
	}
	awaiting, err := s.taskAwaitsApproval(taskID)
	if err != nil {
		return nil, err
	}
	now := time.Now()
	for _, a := range roster {
		d := swarmAgentDetail{
			AgentID:     a.ID,
			Name:        a.Name,
			Role:        a.Role,
			State:       a.State,
			IntentsDone: a.IntentsDone,
			Findings:    a.Findings,
			WaitReason:  swarmWaitReason(a, blocker, awaiting, now),
		}
		if a.CurrentIntentID != nil {
			if it, ok := intents[*a.CurrentIntentID]; ok {
				d.CurrentIntent = it
			} else {
				// The node is gone (deleted exploration): still show which id was held.
				d.CurrentIntent = &swarmIntentRef{ID: *a.CurrentIntentID}
			}
			if cp, ok := checkpoints[*a.CurrentIntentID]; ok {
				d.LastCheckpoint = cp
			}
		}
		if at, ok := lastEvents[a.Name]; ok {
			at := at
			d.LastEventAt = &at
		}
		out = append(out, d)
	}
	return out, nil
}

// taskSwarmProgress rolls the detail view up into the numbers the view headlines.
func (s *Server) taskSwarmProgress(taskID, expID int64, details []swarmAgentDetail) (swarmProgress, error) {
	var p swarmProgress
	pg := s.m.PG()
	if err := pg.QueryRow(`SELECT
  count(*) FILTER (WHERE state = ANY($2)),
  count(*) FILTER (WHERE NOT (state = ANY($2)))
FROM exploration_nodes WHERE kind='intent' AND exploration_id=$1`,
		expID, swarmSettledIntentStates).Scan(&p.IntentsSettled, &p.IntentsOpen); err != nil {
		return p, err
	}
	if err := pg.QueryRow(`SELECT count(*) FROM findings WHERE task_id=$1 AND status = ANY($2)`,
		taskID, swarmVerifiedFindingStatuses).Scan(&p.FindingsVerified); err != nil {
		return p, err
	}
	if err := pg.QueryRow(`SELECT count(*) FROM coverage_ledger WHERE task_id=$1 AND outcome=$2`,
		taskID, db.OutcomeNeedsFollowUp).Scan(&p.CoverageGaps); err != nil {
		return p, err
	}
	for _, d := range details {
		if strings.HasPrefix(d.WaitReason, "blocked by") {
			p.BlockedAgents++
		}
	}
	return p, nil
}

// taskSwarm serves GET /api/tasks/{id}/swarm: roster, live counts, last events,
// the per-agent explanation (current intent, last checkpoint, why it is waiting)
// and the task-level progress rollup.
func (s *Server) taskSwarm(w http.ResponseWriter, r *http.Request) {
	id, ok := s.swarmTaskID(w, r)
	if !ok {
		return
	}
	pg := s.m.PG()
	roster, err := pg.ListTaskAgents(id)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	if roster == nil {
		roster = []*db.TaskAgent{}
	}
	t := s.m.ResolveTask(r.PathValue("id"))
	intents, err := s.intentsByState(t.ExpID)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	conf, err := s.confidenceBuckets(id)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	events, err := pg.ListAgentEvents(id, 0, 100)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	if events == nil {
		events = []db.AgentEvent{}
	}
	details, err := s.taskAgentsDetail(id, roster)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	progress, err := s.taskSwarmProgress(id, t.ExpID, details)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{
		"agents":             roster,
		"intents_by_state":   intents,
		"finding_confidence": conf,
		"events":             events,
		"agents_detail":      details,
		"progress":           progress,
	})
}

// taskPaths serves GET /api/tasks/{id}/paths. Query `state` filters to one state;
// the default lists all non-refuted paths, best score first.
func (s *Server) taskPaths(w http.ResponseWriter, r *http.Request) {
	id, ok := s.swarmTaskID(w, r)
	if !ok {
		return
	}
	states := []string{db.PathStateProposed, db.PathStateValidating, db.PathStateValidated}
	if st := r.URL.Query().Get("state"); st != "" {
		states = []string{st}
	}
	paths, err := s.m.PG().ListAttackPaths(id, states, 100)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	if paths == nil {
		paths = []*db.AttackPath{}
	}
	writeJSON(w, 200, paths)
}

// taskPathDetail serves GET /api/tasks/{id}/paths/{pathID}: the path plus its steps.
func (s *Server) taskPathDetail(w http.ResponseWriter, r *http.Request) {
	id, ok := s.swarmTaskID(w, r)
	if !ok {
		return
	}
	pathID, err := strconv.ParseInt(r.PathValue("pathID"), 10, 64)
	if err != nil || pathID <= 0 {
		writeErr(w, 400, "bad path id")
		return
	}
	p, steps, err := s.m.PG().GetAttackPath(pathID)
	if err != nil {
		if err == sql.ErrNoRows {
			writeErr(w, 404, "path not found")
			return
		}
		writeErr(w, 500, err.Error())
		return
	}
	if p.TaskID != id {
		writeErr(w, 404, "path not found")
		return
	}
	if steps == nil {
		steps = []*db.AttackPathStep{}
	}
	writeJSON(w, 200, map[string]any{"path": p, "steps": steps})
}

// taskKnowledge serves GET /api/tasks/{id}/knowledge?q= — the task's scoped
// knowledge candidates, newest first, optionally narrowed by a case-insensitive
// substring match on title/body.
func (s *Server) taskKnowledge(w http.ResponseWriter, r *http.Request) {
	id, ok := s.swarmTaskID(w, r)
	if !ok {
		return
	}
	q := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("q")))
	limit := atoiDefault(r.URL.Query().Get("limit"), 20)
	items, err := s.m.PG().SearchKnowledgeCandidates(id, 400)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	if q != "" {
		filtered := items[:0]
		for _, it := range items {
			if strings.Contains(strings.ToLower(it.Title+" "+it.Body), q) {
				filtered = append(filtered, it)
			}
		}
		items = filtered
	}
	if len(items) > limit {
		items = items[:limit]
	}
	if items == nil {
		items = []db.KnowledgeItem{}
	}
	writeJSON(w, 200, items)
}

// taskProgress serves GET /api/tasks/{id}/progress: the swarm rollup (intents by
// state, confidence buckets, path counts by state, knowledge counts by kind, and
// how many intents carry checkpoints).
func (s *Server) taskProgress(w http.ResponseWriter, r *http.Request) {
	id, ok := s.swarmTaskID(w, r)
	if !ok {
		return
	}
	pg := s.m.PG()
	t := s.m.ResolveTask(r.PathValue("id"))
	intents, err := s.intentsByState(t.ExpID)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	conf, err := s.confidenceBuckets(id)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	paths, err := pg.PathCounts(ctx, id)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	knowledge, err := pg.KnowledgeCounts(ctx, id)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	var checkpointed int
	err = pg.QueryRow(`SELECT count(DISTINCT intent_id) FROM intent_checkpoints WHERE task_id=$1`, id).Scan(&checkpointed)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{
		"intents_by_state":     intents,
		"finding_confidence":   conf,
		"paths":                paths,
		"knowledge":            knowledge,
		"checkpointed_intents": checkpointed,
	})
}

// validatePathStep serves POST /api/tasks/{id}/paths/{pathID}/steps/{pos}/validate:
// a human verdict on one step (verified/refuted + optional evidence note), which
// rescores the path. The agent-side evidence gate lives in the chain tools; a
// human may overrule it.
func (s *Server) validatePathStep(w http.ResponseWriter, r *http.Request) {
	s.mutatePathStep(w, r, "validate")
}

// refutePathStep serves POST /api/tasks/{id}/paths/{pathID}/steps/{pos}/refute.
func (s *Server) refutePathStep(w http.ResponseWriter, r *http.Request) {
	s.mutatePathStep(w, r, "refute")
}

func (s *Server) mutatePathStep(w http.ResponseWriter, r *http.Request, action string) {
	id, ok := s.swarmTaskID(w, r)
	if !ok {
		return
	}
	pathID, err := strconv.ParseInt(r.PathValue("pathID"), 10, 64)
	if err != nil || pathID <= 0 {
		writeErr(w, 400, "bad path id")
		return
	}
	pos, err := strconv.Atoi(r.PathValue("pos"))
	if err != nil || pos <= 0 {
		writeErr(w, 400, "bad step position")
		return
	}
	var req struct {
		Note string `json:"note"`
	}
	if r.Body != nil {
		_ = json.NewDecoder(r.Body).Decode(&req)
	}
	p, _, err := s.m.PG().GetAttackPath(pathID)
	if err != nil {
		if err == sql.ErrNoRows {
			writeErr(w, 404, "path not found")
			return
		}
		writeErr(w, 500, err.Error())
		return
	}
	if p.TaskID != id {
		writeErr(w, 404, "path not found")
		return
	}
	status := db.StepStatusVerified
	if action == "refute" {
		status = db.StepStatusRefuted
	}
	ctx := r.Context()
	tx, err := s.m.PG().BeginTx(ctx, nil)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	defer tx.Rollback()
	if err := db.SetStepStatusTx(ctx, tx, pathID, pos, status, req.Note, "user"); err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	if err := tx.Commit(); err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	p, steps, err := s.m.PG().GetAttackPath(pathID)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"path": p, "steps": steps})
}

// querySince parses the reconnect cursor (the last seen event id) from the
// request; 0 means "no cursor".
func querySince(r *http.Request) int64 {
	raw := r.URL.Query().Get("since")
	v, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || v < 0 {
		return 0
	}
	return v
}

// streamSwarm serves GET /api/tasks/{id}/swarm/stream: SSE frames of agent
// lifecycle events, same id/data framing as the activity stream. A reconnecting
// client sends ?since=<lastEventId> (its cursor) and receives every event after
// it, so events missed while disconnected are not lost; without a cursor the
// last 50 events are replayed (oldest first). Live events follow either way.
func (s *Server) streamSwarm(w http.ResponseWriter, r *http.Request) {
	tok := extractToken(r)
	if !s.sessionOK(tok) {
		writeErr(w, 401, "token is invalid or expired")
		return
	}
	id, ok := s.swarmTaskID(w, r)
	if !ok {
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeErr(w, 500, "streaming unsupported")
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")

	// Subscribe BEFORE the replay so events in between aren't lost; the id<=last
	// skip drops any overlap.
	ch, unsub := s.engine.Broadcaster().SubscribeSystem(id)
	defer unsub()

	send := func(ev db.SystemEvent) {
		b, _ := json.Marshal(ev)
		fmt.Fprintf(w, "id: %d\ndata: %s\n\n", ev.ID, b)
		flusher.Flush()
	}

	ctx := r.Context()
	var last int64
	if since := querySince(r); since > 0 {
		// Cursor catch-up: page through everything after the client's cursor so
		// events missed while disconnected are not lost.
		const replayPage = 200
		after := since
		for {
			if ctx.Err() != nil {
				return // client gone mid-replay
			}
			page, pErr := s.m.PG().ListAgentEvents(id, after, replayPage)
			if pErr != nil {
				writeErr(w, 500, pErr.Error())
				return
			}
			for _, ev := range page {
				send(db.SystemEvent{ID: ev.ID, Kind: ev.Kind, Data: ev.Detail, Task: id, Agent: ev.AgentName, At: ev.CreatedAt})
				last = ev.ID
			}
			if len(page) < replayPage {
				break
			}
			after = page[len(page)-1].ID
		}
	} else {
		replay, err := s.m.PG().ListAgentEvents(id, 0, 50)
		if err != nil {
			writeErr(w, 500, err.Error())
			return
		}
		for _, ev := range replay {
			send(db.SystemEvent{ID: ev.ID, Kind: ev.Kind, Data: ev.Detail, Task: id, Agent: ev.AgentName, At: ev.CreatedAt})
			last = ev.ID
		}
	}

	ping := time.NewTicker(20 * time.Second)
	defer ping.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case ev, ok := <-ch:
			if !ok || !s.sessionOK(tok) {
				return
			}
			if ev.ID <= last {
				continue // already replayed
			}
			last = ev.ID
			send(ev)
		case <-ping.C:
			if !s.sessionOK(tok) {
				return
			}
			fmt.Fprint(w, ": ping\n\n")
			flusher.Flush()
		}
	}
}
