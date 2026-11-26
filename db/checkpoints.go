package db

// Intent checkpoints: the durable progress record of one worker run. A resumed
// run reads the latest checkpoint instead of repeating finished work, and the
// attempt counter bounds resume churn.

import (
	"database/sql"
	"encoding/json"
	"time"
)

// IntentCheckpoint is one saved progress record of an intent run.
type IntentCheckpoint struct {
	ID        int64     `json:"id"`
	TaskID    int64     `json:"task_id"`
	IntentID  int64     `json:"intent_id"`
	AgentName string    `json:"agent_name"`
	Attempt   int       `json:"attempt"`
	Phase     string    `json:"phase"`
	NextStep  string    `json:"next_step"`
	Done      []string  `json:"done_items"`
	Open      []string  `json:"open_items"`
	Artifacts []string  `json:"artifacts"`
	ToolCalls int       `json:"tool_calls"`
	CreatedAt time.Time `json:"created_at"`
}

func jsonList(v []string) []byte {
	if v == nil {
		return []byte("[]")
	}
	b, err := json.Marshal(v)
	if err != nil {
		return []byte("[]")
	}
	return b
}

// AddCheckpoint stores one checkpoint and returns its id.
func (d *DB) AddCheckpoint(cp IntentCheckpoint) (int64, error) {
	var id int64
	err := d.QueryRow(`INSERT INTO intent_checkpoints
(task_id, intent_id, agent_name, attempt, phase, next_step, done_items, open_items, artifacts, tool_calls)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10) RETURNING id`,
		cp.TaskID, cp.IntentID, cp.AgentName, cp.Attempt, cp.Phase, cp.NextStep,
		jsonList(cp.Done), jsonList(cp.Open), jsonList(cp.Artifacts), cp.ToolCalls).Scan(&id)
	return id, err
}

func scanCheckpoint(row interface{ Scan(...any) error }) (*IntentCheckpoint, error) {
	var cp IntentCheckpoint
	var done, open, arts []byte
	if err := row.Scan(&cp.ID, &cp.TaskID, &cp.IntentID, &cp.AgentName, &cp.Attempt, &cp.Phase,
		&cp.NextStep, &done, &open, &arts, &cp.ToolCalls, &cp.CreatedAt); err != nil {
		return nil, err
	}
	_ = json.Unmarshal(done, &cp.Done)
	_ = json.Unmarshal(open, &cp.Open)
	_ = json.Unmarshal(arts, &cp.Artifacts)
	return &cp, nil
}

// LatestCheckpoint returns the newest checkpoint of an intent (nil when none).
func (d *DB) LatestCheckpoint(intentID int64) (*IntentCheckpoint, error) {
	cp, err := scanCheckpoint(d.QueryRow(`SELECT id, task_id, intent_id, agent_name, attempt, phase, next_step,
done_items, open_items, artifacts, tool_calls, created_at
FROM intent_checkpoints WHERE intent_id=$1 ORDER BY id DESC LIMIT 1`, intentID))
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return cp, err
}

// LatestTaskCheckpoint returns the newest checkpoint across the task's intents
// (nil when none). Used by the session-start brief to surface the last durable
// result without knowing the intent.
func (d *DB) LatestTaskCheckpoint(taskID int64) (*IntentCheckpoint, error) {
	cp, err := scanCheckpoint(d.QueryRow(`SELECT id, task_id, intent_id, agent_name, attempt, phase, next_step,
done_items, open_items, artifacts, tool_calls, created_at
FROM intent_checkpoints WHERE task_id=$1 ORDER BY id DESC LIMIT 1`, taskID))
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return cp, err
}

// CountCheckpointAttempts counts how many distinct attempts an intent has
// written checkpoints for — the number of executions, not flushes. Every
// settled run force-writes a final checkpoint stamped with its attempt, so
// distinct attempts track executions; multiple saves from one run must not
// consume extra resume budget.
func (d *DB) CountCheckpointAttempts(intentID int64) (int, error) {
	var n int
	err := d.QueryRow(`SELECT count(DISTINCT attempt) FROM intent_checkpoints WHERE intent_id=$1`, intentID).Scan(&n)
	return n, err
}
