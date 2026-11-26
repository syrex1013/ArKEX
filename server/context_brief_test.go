package server

import (
	"context"
	"strings"
	"testing"

	"github.com/Autumn-27/artex/db"
)

func TestEngagementBriefBlockFor(t *testing.T) {
	m, err := NewManager(t.TempDir(), "")
	if err != nil {
		db.SkipPGUnavailable(t, err)
	}
	defer m.Close()

	eng, err := m.pg.CreateEngagement("ctx brief eng test", "brief block test")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _, _ = m.pg.DeleteEngagement(eng.ID) }()
	task, err := m.pg.CreateTaskWithOptions("brief task", "goal", db.TaskCreateOptions{EngagementID: &eng.ID})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = m.pg.DeleteTask(task.ID) }()
	plain, err := m.pg.CreateTaskWithOptions("plain task", "goal", db.TaskCreateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = m.pg.DeleteTask(plain.ID) }()

	if _, err := m.pg.CreateEngagementRecord(eng.ID, &db.EngagementRecord{
		Kind:   db.RecordKindScopeRevision,
		Title:  "Scope v1",
		Body:   "assets: host-a added",
		Status: db.RecordStatusAccepted,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := m.pg.CreateEngagementRecord(eng.ID, &db.EngagementRecord{
		Kind:   db.RecordKindOperatorDecision,
		Title:  "stop re-probing ustaw",
		Body:   "TCP wedge confirmed from third-party vantage",
		Status: db.RecordStatusAccepted,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := m.pg.CreateEngagementRecord(eng.ID, &db.EngagementRecord{
		Kind:  db.RecordKindOperatorDecision,
		Title: "focus on cc360 escalation",
	}); err != nil {
		t.Fatal(err)
	}

	store := m.pg.Exploration(task.ExplorationID)
	nodeID, err := store.AddNode(db.KindIntent, map[string]any{"summary": "probe admin"}, 1, "open", "worker", nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.pg.AddCheckpoint(db.IntentCheckpoint{
		TaskID:    task.ID,
		IntentID:  nodeID,
		AgentName: "worker#0",
		Attempt:   1,
		Phase:     "recon",
		NextStep:  "enumerate /admin",
	}); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	s := New(ctx, m, t.TempDir(), t.TempDir(), t.TempDir())
	defer stopTestServer(t, s, cancel)
	serverTask := taskFromPG(task, store, m.interceptor)

	block := s.engagementBriefBlockFor(serverTask)
	if block == "" {
		t.Fatal("expected non-empty brief block")
	}
	for _, want := range []string{
		"Active scope:",
		"Scope v1",
		"host-a added",
		"Latest durable result",
		"recon",
		"enumerate /admin",
	} {
		if !strings.Contains(block, want) {
			t.Fatalf("brief block missing %q:\n%s", want, block)
		}
	}
	if !strings.Contains(block, "focus on cc360 escalation") {
		t.Fatalf("brief block should list open operator questions:\n%s", block)
	}
	if !strings.Contains(block, "Decision (accepted): stop re-probing ustaw") {
		t.Fatalf("brief block should carry accepted decisions:\n%s", block)
	}

	if empty := s.engagementBriefBlockFor(taskFromPG(plain, m.pg.Exploration(plain.ExplorationID), m.interceptor)); empty != "" {
		t.Fatalf("task without engagement should produce empty block, got:\n%s", empty)
	}
}
