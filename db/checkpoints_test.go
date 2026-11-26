package db

// Distinct attempts, not checkpoint rows, are the execution count: multiple
// saves from one run must not consume extra resume budget.

import "testing"

func TestCountCheckpointAttempts(t *testing.T) {
	d, err := Open(testDSN(t))
	if err != nil {
		SkipPGUnavailable(t, err)
	}
	defer d.Close()
	task, err := d.CreateTaskWithOptions("checkpoint attempts", "attempt counting", TaskCreateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = d.DeleteTask(task.ID) }()
	store := d.Exploration(task.ExplorationID)
	intentID, err := store.AddNode(KindIntent, map[string]any{"summary": "attempted intent"}, 1, "open", "planner", nil)
	if err != nil {
		t.Fatal(err)
	}
	first := IntentCheckpoint{TaskID: task.ID, IntentID: intentID, AgentName: "worker", Attempt: 1, NextStep: "step"}
	if _, err := d.AddCheckpoint(first); err != nil {
		t.Fatal(err)
	}
	if _, err := d.AddCheckpoint(first); err != nil {
		t.Fatal(err)
	}
	if _, err := d.AddCheckpoint(IntentCheckpoint{TaskID: task.ID, IntentID: intentID, AgentName: "worker", Attempt: 2, NextStep: "next"}); err != nil {
		t.Fatal(err)
	}
	got, err := d.CountCheckpointAttempts(intentID)
	if err != nil {
		t.Fatal(err)
	}
	if got != 2 {
		t.Fatalf("distinct attempts = %d, want 2 (three saves, two executions)", got)
	}
}
