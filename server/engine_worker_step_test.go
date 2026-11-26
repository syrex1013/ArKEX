package server

import (
	"context"
	"strconv"
	"testing"

	"github.com/Autumn-27/artex/agent"
	"github.com/Autumn-27/artex/db"
)

// Empty polls must not consume the intents_done counter nor write claim/release
// events, and a real claim must link the intent into the agent roster before the
// run starts.
func TestWorkerStepAccounting(t *testing.T) {
	m, err := NewManager(t.TempDir(), "")
	if err != nil {
		t.Skipf("postgres unavailable: %v", err)
	}
	defer m.Close()

	task, err := m.CreateTaskWithOptions("accounting", "verify swarm accounting", db.TaskCreateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer m.DeleteTask(task.ID, DeleteTaskOptions{})
	e := NewEngine(m)
	taskID, _ := strconv.ParseInt(task.ID, 10, 64)

	roster, err := m.pg.EnsureTaskAgents(taskID, []string{"general"})
	if err != nil {
		t.Fatal(err)
	}
	ag := roster[0]

	// Empty frontier: nothing claimed, no events, no counter tick, no roster link.
	if claimed := e.runWorkerStep(context.Background(), task, "general#0", "general", &agent.Worker{}, ag.ID); claimed {
		t.Fatal("empty frontier claimed an intent")
	}
	var done, tick int
	var link any
	if err := m.pg.QueryRow(`SELECT intents_done, COALESCE(current_intent_id, 0), CASE WHEN current_intent_id IS NULL THEN 0 ELSE 1 END FROM task_agents WHERE id=$1`, ag.ID).Scan(&done, &link, &tick); err != nil {
		t.Fatal(err)
	}
	if done != 0 || tick != 0 || link.(int64) != 0 {
		t.Fatalf("empty poll mutated agent row: intents_done=%d link=%v tick=%d", done, link, tick)
	}
	events, err := m.pg.ListAgentEvents(taskID, 0, 50)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 0 {
		t.Fatalf("empty poll wrote %d events, want 0", len(events))
	}

	// Real claim: the claim event is emitted by the claim path itself and the
	// intent is linked into the roster for the duration of the run.
	intentID, err := task.Store.AddIntent(map[string]any{"summary": "verify accounting"}, 5, nil, "planner")
	if err != nil {
		t.Fatal(err)
	}
	p := &captureProvider{}
	w := agent.NewWorker(p, "model", m.dir, nil, 0, 2)
	if claimed := e.runWorkerStep(context.Background(), task, "general#0", "general", w, ag.ID); !claimed {
		t.Fatal("seeded intent was not claimed")
	}
	if err := m.pg.QueryRow(`SELECT current_intent_id FROM task_agents WHERE id=$1`, ag.ID).Scan(&link); err != nil {
		t.Fatal(err)
	}
	if link == nil || link.(int64) != intentID {
		t.Fatalf("current_intent_id=%v, want %d", link, intentID)
	}

	// Settling in the pool loop releases the agent: one intents_done tick and
	// the roster link cleared.
	if err := m.pg.FinishAgent(ag.ID, false); err != nil {
		t.Fatal(err)
	}
	if err := m.pg.QueryRow(`SELECT intents_done, CASE WHEN current_intent_id IS NULL THEN 0 ELSE 1 END FROM task_agents WHERE id=$1`, ag.ID).Scan(&done, &tick); err != nil {
		t.Fatal(err)
	}
	if done != 1 || tick != 0 {
		t.Fatalf("after settle: intents_done=%d link=%d, want 1/0", done, tick)
	}

	events, err = m.pg.ListAgentEvents(taskID, 0, 50)
	if err != nil {
		t.Fatal(err)
	}
	claims := 0
	for _, ev := range events {
		if ev.Kind == db.EventAgentClaim {
			claims++
		}
	}
	if claims != 1 {
		t.Fatalf("claim events=%d, want exactly 1", claims)
	}
}
