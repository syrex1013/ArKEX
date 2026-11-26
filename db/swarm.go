package db

// Long-horizon swarm: the durable per-task agent roster. An agent row outlives a
// single intent, so a restarted task reuses the same names and roles, and the
// roster is the accounting surface of what each slot did.

import (
	"database/sql"
	"strconv"
	"time"
)

// Agent roles. A role is a routing preference for open intents (payload->>'role'),
// never a hard restriction: a role-less intent is claimable by every agent.
const (
	AgentRoleRecon   = "recon"
	AgentRoleWeb     = "web"
	AgentRoleAPI     = "api"
	AgentRoleExploit = "exploit"
	AgentRoleVerify  = "verify"
	AgentRoleGeneral = "general"
	AgentRoleChain   = "chain"
)

// Agent states.
const (
	AgentStateIdle     = "idle"
	AgentStateWorking  = "working"
	AgentStateCooldown = "cooldown"
	AgentStateRetired  = "retired"
)

// ValidAgentRole reports whether r is a roster role.
func ValidAgentRole(r string) bool {
	switch r {
	case AgentRoleRecon, AgentRoleWeb, AgentRoleAPI, AgentRoleExploit, AgentRoleVerify, AgentRoleGeneral, AgentRoleChain:
		return true
	}
	return false
}

// TaskAgent is one roster slot of a task.
type TaskAgent struct {
	ID              int64      `json:"id"`
	TaskID          int64      `json:"task_id"`
	Slot            int        `json:"slot"`
	Name            string     `json:"name"`
	Role            string     `json:"role"`
	State           string     `json:"state"`
	CurrentIntentID *int64     `json:"current_intent_id,omitempty"`
	LastHeartbeatAt *time.Time `json:"last_heartbeat_at,omitempty"`
	IntentsDone     int        `json:"intents_done"`
	Findings        int        `json:"findings"`
	CreatedAt       time.Time  `json:"created_at"`
	UpdatedAt       time.Time  `json:"updated_at"`
}

const taskAgentCols = `id, task_id, slot, name, role, state, current_intent_id, last_heartbeat_at, intents_done, findings, created_at, updated_at`

func scanTaskAgent(sc interface{ Scan(...any) error }) (*TaskAgent, error) {
	var a TaskAgent
	var intent sql.NullInt64
	var hb sql.NullTime
	if err := sc.Scan(&a.ID, &a.TaskID, &a.Slot, &a.Name, &a.Role, &a.State, &intent, &hb, &a.IntentsDone, &a.Findings, &a.CreatedAt, &a.UpdatedAt); err != nil {
		return nil, err
	}
	if intent.Valid {
		a.CurrentIntentID = &intent.Int64
	}
	if hb.Valid {
		a.LastHeartbeatAt = &hb.Time
	}
	return &a, nil
}

// mixRoles is the cycled specialist mix; the first slot is always general so at
// least one agent can claim role-less work.
var mixRoles = [...]string{AgentRoleGeneral, AgentRoleRecon, AgentRoleWeb, AgentRoleAPI, AgentRoleExploit, AgentRoleVerify}

// AgentRoleMix returns n cycled roles from mixRoles plus a trailing chain role when
// chain is set. n=3 → general, recon, web.
func AgentRoleMix(n int, chain bool) []string {
	if n < 1 {
		n = 1
	}
	out := make([]string, 0, n+1)
	for i := range n {
		out = append(out, mixRoles[i%len(mixRoles)])
	}
	if chain {
		out = append(out, AgentRoleChain)
	}
	return out
}

// EnsureTaskAgents makes the roster match roles: missing slots are created (name
// role#slot), extra slots are retired, and the live roster is returned.
func (d *DB) EnsureTaskAgents(taskID int64, roles []string) ([]*TaskAgent, error) {
	if _, err := d.Exec(`UPDATE task_agents SET state='retired', updated_at=now()
WHERE task_id=$1 AND slot >= $2 AND state <> 'retired'`, taskID, len(roles)); err != nil {
		return nil, err
	}
	for i, role := range roles {
		if _, err := d.Exec(`INSERT INTO task_agents(task_id, slot, name, role) VALUES ($1,$2,$3,$4)
ON CONFLICT (task_id, slot) DO UPDATE SET role=EXCLUDED.role, state='idle', updated_at=now()`,
			taskID, i, role+"#"+strconv.Itoa(i), role); err != nil {
			return nil, err
		}
	}
	return d.ListTaskAgents(taskID)
}

// ListTaskAgents returns the live roster ordered by slot.
func (d *DB) ListTaskAgents(taskID int64) ([]*TaskAgent, error) {
	rows, err := d.Query(`SELECT `+taskAgentCols+` FROM task_agents WHERE task_id=$1 AND state <> 'retired' ORDER BY slot`, taskID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*TaskAgent
	for rows.Next() {
		a, err := scanTaskAgent(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// ClaimAgent claims the best idle agent for role: an exact role match first, then
// the lowest idle slot. Returns nil (no error) when every agent is busy.
func (d *DB) ClaimAgent(taskID int64, role string) (*TaskAgent, error) {
	a, err := scanTaskAgent(d.QueryRow(`UPDATE task_agents SET state='working', last_heartbeat_at=now(), updated_at=now()
WHERE id = (SELECT id FROM task_agents WHERE task_id=$1 AND state='idle'
           ORDER BY (role=$2) DESC, slot LIMIT 1)
RETURNING `+taskAgentCols, taskID, role))
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return a, err
}

// ClaimAgentByID flips one specific idle agent to working. ok=false when it is
// busy; each worker loop owns exactly one row, so this cannot hand out an agent
// twice.
func (d *DB) ClaimAgentByID(id int64) (*TaskAgent, bool, error) {
	a, err := scanTaskAgent(d.QueryRow(`UPDATE task_agents SET state='working', last_heartbeat_at=now(), updated_at=now()
WHERE id=$1 AND state='idle' RETURNING `+taskAgentCols, id))
	if err == sql.ErrNoRows {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	return a, true, nil
}

// CountTaskFindings counts a task's findings (agent productivity accounting).
func (d *DB) CountTaskFindings(taskID int64) (int, error) {
	var n int
	err := d.QueryRow(`SELECT count(*) FROM findings WHERE task_id=$1`, taskID).Scan(&n)
	return n, err
}

// HeartbeatAgent renews a working agent's lease.
func (d *DB) HeartbeatAgent(id int64) error {
	_, err := d.Exec(`UPDATE task_agents SET last_heartbeat_at=now() WHERE id=$1`, id)
	return err
}

// SetAgentIntent points a working agent at the intent it claimed (0 clears it).
func (d *DB) SetAgentIntent(id, intentID int64) error {
	_, err := d.Exec(`UPDATE task_agents SET current_intent_id=NULLIF($2,0), updated_at=now() WHERE id=$1`, id, intentID)
	return err
}

// ReleaseAgent returns an agent to state (idle or cooldown) and clears its intent.
func (d *DB) ReleaseAgent(id int64, state string) error {
	_, err := d.Exec(`UPDATE task_agents SET state=$2, current_intent_id=NULL, updated_at=now() WHERE id=$1`, id, state)
	return err
}

// FinishAgent returns an agent to idle after a finished intent and counts it.
func (d *DB) FinishAgent(id int64, producedFinding bool) error {
	_, err := d.Exec(`UPDATE task_agents SET state='idle', current_intent_id=NULL,
intents_done=intents_done+1, findings=findings+CASE WHEN $2 THEN 1 ELSE 0 END, updated_at=now()
WHERE id=$1`, id, producedFinding)
	return err
}

// RetireStaleAgents returns agents whose heartbeat expired to idle (boot recovery).
func (d *DB) RetireStaleAgents(taskID int64) error {
	_, err := d.Exec(`UPDATE task_agents SET state='idle', current_intent_id=NULL, updated_at=now()
WHERE task_id=$1 AND state='working'
  AND (last_heartbeat_at IS NULL OR last_heartbeat_at < now() - $2 * INTERVAL '1 second')`,
		taskID, IntentClaimTTL.Seconds())
	return err
}
