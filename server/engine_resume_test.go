package server

import (
	"context"
	"errors"
	"fmt"
	"iter"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Autumn-27/artex/agent"
	"github.com/Autumn-27/artex/db"
	"github.com/Autumn-27/norma/llm"
)

// captureProvider records the request messages of every call and returns an empty
// assistant turn (or an error).
type captureProvider struct {
	msgs [][]llm.Message
	fail bool
}

func (p *captureProvider) Stream(_ context.Context, req llm.CompletionRequest) iter.Seq2[llm.StreamEvent, error] {
	p.msgs = append(p.msgs, req.Messages)
	return func(yield func(llm.StreamEvent, error) bool) {
		if p.fail {
			yield(llm.StreamEvent{}, errors.New("model down"))
			return
		}
		if !yield(llm.StreamEvent{Type: llm.SEMessageStart}, nil) {
			return
		}
		if !yield(llm.StreamEvent{Type: llm.SEMessageDelta, Text: "finished"}, nil) {
			return
		}
		yield(llm.StreamEvent{Type: llm.SEMessageStop}, nil)
	}
}

func (p *captureProvider) Complete(context.Context, llm.CompletionRequest) (llm.Message, string, llm.Usage, error) {
	return llm.Message{Role: llm.RoleAssistant, Content: []llm.ContentBlock{llm.TextBlock("finished")}}, "end_turn", llm.Usage{}, nil
}

// allText concatenates every message text of every recorded call.
func (p *captureProvider) allText() string {
	var b strings.Builder
	for _, call := range p.msgs {
		for _, m := range call {
			b.WriteString(m.Text())
			b.WriteString("\n")
		}
	}
	return b.String()
}

// A resumed run receives its checkpoint block with the attempt counter, and an
// intent past its resume budget settles blocked with the explicit cause.
func TestResumeBlockAndExhaustion(t *testing.T) {
	m, err := NewManager(t.TempDir(), "")
	if err != nil {
		t.Skipf("postgres unavailable: %v", err)
	}
	defer m.Close()
	if err := m.pg.SetSetting("intent_max_resumes", "1"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = m.pg.SetSetting("intent_max_resumes", "3") })

	task, err := m.CreateTaskWithOptions("resume", "resume goal", db.TaskCreateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer m.DeleteTask(task.ID, DeleteTaskOptions{})
	e := NewEngine(m)
	intentID, err := task.Store.AddIntent(map[string]any{"summary": "probe login"}, 5, nil, "planner")
	if err != nil {
		t.Fatal(err)
	}
	taskID, _ := strconv.ParseInt(task.ID, 10, 64)

	// Two checkpoint saves from the same attempt: one execution wrote twice, so
	// the next run is attempt 2 (not 3) against a budget of 1 — still exhausted.
	last := db.IntentCheckpoint{TaskID: taskID, IntentID: intentID, Attempt: 1,
		Phase: "Bash", NextStep: "verify cookie flags on /admin", Done: []string{"enumerated login form"}}
	if _, err := m.pg.AddCheckpoint(last); err != nil {
		t.Fatal(err)
	}
	if _, err := m.pg.AddCheckpoint(last); err != nil {
		t.Fatal(err)
	}

	// First run: capture the launch input. Attempt 2 exceeds the budget of 1,
	// so the provider error path must settle blocked with resume_exhausted.
	p := &captureProvider{fail: true}
	w := agent.NewWorker(p, "model", m.dir, nil, 0, 2)
	intent, token := e.claimNext(task, "general#0")
	if intent == nil || intent.ID != intentID {
		t.Fatalf("intent not claimed: %v", intent)
	}
	e.runIntent(context.Background(), task, "general#0", w, intent, token, "", "")

	if len(p.msgs) == 0 {
		t.Fatal("provider was never called")
	}
	launch := p.allText()
	if !strings.Contains(launch, "Resume attempt 2/1") {
		t.Fatalf("resume block missing from launch input: %q", launch)
	}
	if !strings.Contains(launch, "verify cookie flags on /admin") || !strings.Contains(launch, "enumerated login form") {
		t.Fatal("checkpoint content missing from launch input")
	}

	var state, reason string
	if err := m.pg.QueryRow(`SELECT state, COALESCE(blocked_reason,'') FROM exploration_nodes WHERE id=$1`, intentID).Scan(&state, &reason); err != nil {
		t.Fatal(err)
	}
	if state != "blocked" || reason != db.IntentBlockedResumeExhausted {
		t.Fatalf("state=%q reason=%q, want blocked/%s", state, reason, db.IntentBlockedResumeExhausted)
	}
	events, err := m.pg.ListAgentEvents(taskID, 0, 50)
	if err != nil {
		t.Fatal(err)
	}
	var sawExhausted, sawResume bool
	for _, ev := range events {
		switch ev.Kind {
		case db.EventResumeExhausted:
			sawExhausted = true
		case db.EventResume:
			sawResume = true
		}
	}
	if !sawExhausted || !sawResume {
		t.Fatalf("events missing (resume=%v exhausted=%v)", sawResume, sawExhausted)
	}
	_ = fmt.Sprint(time.Now())
}
