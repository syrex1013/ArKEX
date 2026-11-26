package db

// Discovery events: what one worker just found, published for its live peers and
// replayed to a restarted run. Purely additive context; never a source of truth.

import "time"

// DiscoveryEvent is one peer-visible discovery.
type DiscoveryEvent struct {
	ID        int64     `json:"id"`
	TaskID    int64     `json:"task_id"`
	IntentID  int64     `json:"intent_id,omitempty"`
	AgentName string    `json:"agent_name"`
	Kind      string    `json:"kind"`
	Summary   string    `json:"summary"`
	NodeID    int64     `json:"node_id,omitempty"`
	CreatedAt time.Time `json:"created_at"`
}

// AddDiscovery stores one discovery event and returns its id.
func (d *DB) AddDiscovery(ev DiscoveryEvent) (int64, error) {
	var id int64
	err := d.QueryRow(`INSERT INTO discovery_events(task_id, intent_id, agent_name, kind, summary, node_id)
VALUES ($1, NULLIF($2,0), $3, $4, $5, NULLIF($6,0)) RETURNING id`,
		ev.TaskID, ev.IntentID, ev.AgentName, ev.Kind, ev.Summary, ev.NodeID).Scan(&id)
	return id, err
}

// RecentDiscoveries returns up to limit newest events after afterID (0 = from the
// end), oldest first so a prompt block reads chronologically.
func (d *DB) RecentDiscoveries(taskID, afterID int64, limit int) ([]DiscoveryEvent, error) {
	if limit <= 0 {
		limit = 20
	}
	rows, err := d.Query(`SELECT id, task_id, COALESCE(intent_id,0), agent_name, kind, summary,
COALESCE(node_id,0), created_at FROM discovery_events
WHERE task_id=$1 AND ($2 = 0 OR id > $2) ORDER BY id DESC LIMIT $3`, taskID, afterID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []DiscoveryEvent
	for rows.Next() {
		var ev DiscoveryEvent
		if err := rows.Scan(&ev.ID, &ev.TaskID, &ev.IntentID, &ev.AgentName, &ev.Kind, &ev.Summary, &ev.NodeID, &ev.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, ev)
	}
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out, rows.Err()
}
