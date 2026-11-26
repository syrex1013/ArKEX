package db

// Agent lifecycle events: the append-only record behind the swarm view and the
// system event stream. Kinds are free text so a new kind needs no migration.

import (
	"context"
	"encoding/json"
	"time"
)

// Lifecycle kinds.
const (
	EventAgentSeeded      = "agent_seeded"
	EventAgentClaim       = "agent_claim"
	EventAgentRelease     = "agent_release"
	EventCheckpoint       = "checkpoint"
	EventResume           = "resume"
	EventResumeExhausted  = "resume_exhausted"
	EventDiscovery        = "discovery"
	EventPathProposed     = "path_proposed"
	EventPathStep         = "path_step"
	EventPathValidated    = "path_validated"
	EventPathRefuted      = "path_refuted"
	EventKnowledgeWrite   = "knowledge_write"
	EventKnowledgeInject  = "knowledge_inject"
	EventClaimLost        = "claim_lost"
)

// AgentEvent is one lifecycle record.
type AgentEvent struct {
	ID        int64           `json:"id"`
	TaskID    int64           `json:"task_id"`
	AgentName string          `json:"agent_name,omitempty"`
	IntentID  int64           `json:"intent_id,omitempty"`
	Kind      string          `json:"kind"`
	Detail    json.RawMessage `json:"detail,omitempty"`
	CreatedAt time.Time       `json:"created_at"`
}

// AddAgentEvent stores one event (0 taskID is a no-op).
func (d *DB) AddAgentEvent(ev AgentEvent) (int64, error) {
	if ev.TaskID == 0 {
		return 0, nil
	}
	var id int64
	err := d.QueryRow(`INSERT INTO agent_events(task_id, agent_name, intent_id, kind, detail)
VALUES ($1,$2,NULLIF($3,0),$4,$5) RETURNING id`,
		ev.TaskID, ev.AgentName, ev.IntentID, ev.Kind, jsonbBytes(ev.Detail)).Scan(&id)
	return id, err
}

// ListAgentEvents returns up to limit newest events after afterID (0 = from the
// end), oldest first.
func (d *DB) ListAgentEvents(taskID, afterID int64, limit int) ([]AgentEvent, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := d.Query(`SELECT id, task_id, agent_name, COALESCE(intent_id,0), kind, detail, created_at
FROM agent_events WHERE task_id=$1 AND ($2 = 0 OR id > $2) ORDER BY id DESC LIMIT $3`, taskID, afterID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []AgentEvent
	for rows.Next() {
		var ev AgentEvent
		var detail []byte
		if err := rows.Scan(&ev.ID, &ev.TaskID, &ev.AgentName, &ev.IntentID, &ev.Kind, &detail, &ev.CreatedAt); err != nil {
			return nil, err
		}
		ev.Detail = detail
		out = append(out, ev)
	}
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out, rows.Err()
}

// SystemEvent is one frame of the per-task system event stream.
type SystemEvent struct {
	ID    int64           `json:"id"`
	Kind  string          `json:"kind"`
	Data  json.RawMessage `json:"data"`
	Task  int64           `json:"task_id"`
	Agent string          `json:"agent_name,omitempty"`
	At    time.Time       `json:"at"`
}

// PathCounts returns the per-state path counts of a task.
func (d *DB) PathCounts(ctx context.Context, taskID int64) (map[string]int, error) {
	rows, err := d.QueryContext(ctx, `SELECT state, count(*) FROM attack_paths WHERE task_id=$1 GROUP BY state`, taskID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]int{}
	for rows.Next() {
		var s string
		var n int
		if err := rows.Scan(&s, &n); err != nil {
			return nil, err
		}
		out[s] = n
	}
	return out, rows.Err()
}

// KnowledgeCounts returns the per-kind active knowledge counts of a task's scope.
func (d *DB) KnowledgeCounts(ctx context.Context, taskID int64) (map[string]int, error) {
	companies, eng, keys, err := d.TaskKnowledgeScope(taskID)
	if err != nil {
		return nil, err
	}
	rows, err := d.QueryContext(ctx, `SELECT kind, count(*) FROM knowledge_items
WHERE status='active'
  AND ((scope_company_id IS NOT NULL AND scope_company_id = ANY($1))
    OR (scope_engagement_id IS NOT NULL AND scope_engagement_id = $2)
    OR (scope_asset_key <> '' AND scope_asset_key = ANY($3)))
GROUP BY kind`, int64Array(companies), eng, strArray(keys))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]int{}
	for rows.Next() {
		var k string
		var n int
		if err := rows.Scan(&k, &n); err != nil {
			return nil, err
		}
		out[k] = n
	}
	return out, rows.Err()
}
