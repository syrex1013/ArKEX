package server

import (
	"context"
	"iter"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Autumn-27/artex/agent"
	"github.com/Autumn-27/artex/db"
	"github.com/Autumn-27/norma/llm"
)

// A planning wake-up that did not plan must not re-arm the heartbeat.
//
// The loop re-armed the heartbeat after EVERY wake-up, so a busy task whose
// edges all land in a quiet-skip (open-goal set unchanged + work still in
// flight) pushed the safety-net round out by a full interval on every intent
// completion. With continuable work the safety net then never fired at all —
// the one round that would notice a stalled worker or a drained frontier.
//
// This test shrinks the heartbeat floor to a second and drives edges faster than
// that. Under the old code the heartbeat round never runs; now it must.
func TestPlannerHeartbeatSurvivesSkippedRounds(t *testing.T) {
	m, err := NewManager(t.TempDir(), "")
	db.SkipPGUnavailable(t, err)
	t.Cleanup(func() { _ = m.Close() })
	if m.pg == nil {
		db.SkipNoPG(t)
	}

	restore := minPlanHeartbeat
	minPlanHeartbeat = 1
	t.Cleanup(func() { minPlanHeartbeat = restore })

	created, err := m.CreateTaskWithOptions("heartbeat probe", "probe goal", db.TaskCreateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	task, ok := m.Task(created.ID)
	if !ok {
		t.Fatal("task not found after creation")
	}
	task.PlanHeartbeatSeconds = 1
	task.PlanQuietMode = true

	store := task.Store
	if _, err := store.AddGoal(map[string]any{"summary": "g1"}, "seed"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.AddIntent(map[string]any{"summary": "i1"}, 0, nil, "seed"); err != nil {
		t.Fatal(err)
	}
	fp, err := store.OpenGoalFingerprint()
	if err != nil {
		t.Fatal(err)
	}

	// The probe counts planning rounds and records the trigger source; it never
	// calls a tool, so each round is one model call that ends immediately.
	probe := &roundCounterProvider{}
	e := NewEngine(m)
	e.debounce = time.Millisecond
	e.SetAuthoritativeAgentResolver(func(*Task) (*agent.Planner, *agent.Worker) {
		return agent.NewPlanner(probe, "test-model", m.dir, nil, 0, 3), nil
	})
	// Quiet-skip needs a completed round's fingerprint and active work: record the
	// fingerprint and leave the intent open, so every edge is skipped, not planned.
	e.goalFP.Store(task.ID, fp)

	ctx, stop := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		e.plannerLoop(ctx, task)
	}()
	defer func() { stop(); <-done }()

	// Drive edges every 200ms for 2.5s. Each one quiet-skips; the 1s heartbeat
	// must still fire at least one round in that window.
	deadline := time.Now().Add(2500 * time.Millisecond)
	for time.Now().Before(deadline) {
		task.Notify()
		time.Sleep(200 * time.Millisecond)
	}
	if n := probe.rounds.Load(); n == 0 {
		t.Fatal("no planning round ran: every skipped edge pushed the heartbeat out (the starvation bug)")
	}
}

// roundCounterProvider answers every completion immediately and counts rounds.
type roundCounterProvider struct{ rounds atomic.Int64 }

func (p *roundCounterProvider) Stream(context.Context, llm.CompletionRequest) iter.Seq2[llm.StreamEvent, error] {
	p.rounds.Add(1)
	return func(yield func(llm.StreamEvent, error) bool) {
		yield(llm.StreamEvent{Type: llm.SETextDelta, Text: "no action"}, nil)
		yield(llm.StreamEvent{Type: llm.SEMessageDelta, StopReason: "end_turn"}, nil)
	}
}

func (p *roundCounterProvider) Complete(ctx context.Context, req llm.CompletionRequest) (llm.Message, string, llm.Usage, error) {
	return accumulateStreamForTest(ctx, p.Stream, req)
}
