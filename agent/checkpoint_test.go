package agent

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/Autumn-27/artex/db"
	"github.com/Autumn-27/norma/llm"
)

// fakeCheckpointStore records AddCheckpoint calls; err fails every write.
type fakeCheckpointStore struct {
	ids  int
	rows []db.IntentCheckpoint
	err  error
}

func (f *fakeCheckpointStore) AddCheckpoint(cp db.IntentCheckpoint) (int64, error) {
	if f.err != nil {
		return 0, f.err
	}
	f.ids++
	f.rows = append(f.rows, cp)
	return int64(f.ids), nil
}

func TestCheckpointIntervalHonoured(t *testing.T) {
	store := &fakeCheckpointStore{}
	cp := NewCheckpointer(store, 7, 42, "work#1", 2)

	cp.Observe("Bash", `{"command":"nmap -sV 10.0.0.1","description":"port scan"}`)
	cp.Finish("", false) // first flush: new calls, no previous write → writes
	if len(store.rows) != 1 {
		t.Fatalf("first flush wrote %d rows, want 1", len(store.rows))
	}
	row := store.rows[0]
	if row.TaskID != 7 || row.IntentID != 42 || row.AgentName != "work#1" || row.Attempt != 2 {
		t.Fatalf("identity fields wrong: %+v", row)
	}
	if row.ToolCalls != 1 || row.Phase != "Bash" {
		t.Fatalf("calls/phase wrong: %+v", row)
	}
	if len(row.Open) != 1 || row.Open[0] != "port scan" {
		t.Fatalf("open summary wrong: %v", row.Open)
	}

	cp.Observe("Bash", `{"command":"curl http://10.0.0.1"}`)
	cp.Finish("", false) // within the 5s interval → skipped
	if len(store.rows) != 1 {
		t.Fatalf("flush inside the interval wrote again: %d rows", len(store.rows))
	}

	cp.interval = time.Nanosecond // elapsed → the next flush writes
	time.Sleep(time.Millisecond)
	cp.Finish("", false)
	if len(store.rows) != 2 {
		t.Fatalf("flush after the interval did not write: %d rows", len(store.rows))
	}
	if store.rows[1].ToolCalls != 2 {
		t.Fatalf("tool_calls not carried: %+v", store.rows[1])
	}
}

func TestCheckpointAttemptDefaultsToOne(t *testing.T) {
	store := &fakeCheckpointStore{}
	cp := NewCheckpointer(store, 1, 1, "work#1", 0)
	cp.Finish("next", true) // force always writes
	if len(store.rows) != 1 || store.rows[0].Attempt != 1 {
		t.Fatalf("attempt default: %+v", store.rows)
	}
}

func TestCheckpointForceAlwaysWrites(t *testing.T) {
	store := &fakeCheckpointStore{}
	cp := NewCheckpointer(store, 1, 1, "work#1", 1)

	cp.Finish("step one", true)
	cp.Finish("step two", true) // back-to-back forced writes, no observe between
	if len(store.rows) != 2 {
		t.Fatalf("force wrote %d rows, want 2", len(store.rows))
	}
	if store.rows[0].NextStep != "step one" || store.rows[1].NextStep != "step two" {
		t.Fatalf("next steps not carried: %+v", store.rows)
	}
}

func TestCheckpointWriteArtifacts(t *testing.T) {
	store := &fakeCheckpointStore{}
	cp := NewCheckpointer(store, 1, 1, "work#1", 1)

	cp.CompleteWrite("Write", fmt.Sprintf(`{"file_path":%q,"content":"#!/bin/sh"}`, "/tmp/task/run.sh"), false)
	cp.CompleteWrite("Edit", `{"target_path":"/tmp/task/notes.txt"}`, false)
	cp.CompleteWrite("Write", fmt.Sprintf(`{"file_path":%q,"content":"again"}`, "/tmp/task/run.sh"), false) // duplicate path
	cp.Finish("", true)
	if len(store.rows) != 1 {
		t.Fatalf("wrote %d rows, want 1", len(store.rows))
	}
	arts := store.rows[0].Artifacts
	if len(arts) != 2 || arts[0] != "/tmp/task/run.sh" || arts[1] != "/tmp/task/notes.txt" {
		t.Fatalf("artifacts wrong: %v", arts)
	}
	if len(store.rows[0].Done) != 3 {
		t.Fatalf("done summaries: %v", store.rows[0].Done)
	}
}

func TestCheckpointFailedWriteNotRecorded(t *testing.T) {
	store := &fakeCheckpointStore{}
	cp := NewCheckpointer(store, 1, 1, "work#1", 1)

	cp.Observe("Write", `{"file_path":"/tmp/task/run.sh","content":"#!/bin/sh"}`) // call observed pre-execution
	cp.CompleteWrite("Write", `{"file_path":"/tmp/task/run.sh"}`, true)           // but the write failed
	cp.CompleteWrite("Write", "", true)                                           // errored result without input
	cp.CompleteWrite("Bash", `{"command":"ls"}`, false)                           // non-write tool: not completion
	cp.Finish("", true)
	if len(store.rows) != 1 {
		t.Fatalf("wrote %d rows, want 1", len(store.rows))
	}
	row := store.rows[0]
	if len(row.Done) != 0 || len(row.Artifacts) != 0 {
		t.Fatalf("failed write recorded as work: done=%v artifacts=%v", row.Done, row.Artifacts)
	}
	if row.ToolCalls != 1 || row.Phase != "Write" {
		t.Fatalf("call still counted: %+v", row)
	}

	cp.CompleteWrite("Write", `{"file_path":"/tmp/task/run.sh","content":"ok"}`, false) // retry succeeds
	cp.Finish("", true)
	if len(store.rows[1].Artifacts) != 1 || store.rows[1].Artifacts[0] != "/tmp/task/run.sh" {
		t.Fatalf("successful retry not recorded: %+v", store.rows[1])
	}
}

func TestCheckpointStoreErrorDoesNotFailRun(t *testing.T) {
	store := &fakeCheckpointStore{err: errors.New("db down")}
	cp := NewCheckpointer(store, 1, 1, "work#1", 1)
	cp.Observe("Bash", `{"command":"ls"}`)
	cp.Finish("continue", true) // must not panic and must swallow the error
	cp.Finish("continue", true)
}

func TestWithCheckpointHooks(t *testing.T) {
	store := &fakeCheckpointStore{}
	cp := NewCheckpointer(store, 1, 1, "work#1", 1)
	h := WithCheckpoint(nil, cp)

	prevent, msg, updated := h.PreToolUse(t.Context(), "Write", []byte(`{"file_path":"/x","content":"y"}`))
	if prevent || msg != "" || updated != nil {
		t.Fatalf("PreToolUse interfered: %v %q %v", prevent, msg, updated)
	}
	h.PostToolUse(t.Context(), "Write", []byte(`{"file_path":"/x","content":"y"}`), []byte("ok"), false)
	if len(store.rows) != 1 || store.rows[0].Artifacts[0] != "/x" {
		t.Fatalf("hook flow did not checkpoint: %+v", store.rows)
	}
	// a failed write must not claim completion: no /y artifact, no z summary —
	// while the earlier successful write stays cumulative
	h.PreToolUse(t.Context(), "Write", []byte(`{"file_path":"/y","content":"z"}`))
	h.PostToolUse(t.Context(), "Write", []byte(`{"file_path":"/y"}`), []byte("boom"), true)
	cp.Finish("", true)
	if len(store.rows) != 2 {
		t.Fatalf("post-tool flush skipped: %d rows", len(store.rows))
	}
	row := store.rows[1]
	if len(row.Artifacts) != 1 || row.Artifacts[0] != "/x" {
		t.Fatalf("artifacts after failed write wrong: %v", row.Artifacts)
	}
	if len(row.Done) != 1 || row.Done[0] != "y" {
		t.Fatalf("done after failed write wrong: %v", row.Done)
	}
	// inner hooks still run
	innerCalls := 0
	inner := hookCounter{count: &innerCalls}
	h2 := WithCheckpoint(inner, cp)
	h2.PreToolUse(t.Context(), "Bash", []byte(`{}`))
	h2.PostToolUse(t.Context(), "Bash", []byte(`{}`), []byte("r"), false)
	if innerCalls != 2 {
		t.Fatalf("inner hooks ran %d times, want 2", innerCalls)
	}
}

// hookCounter counts hook invocations (harness.HookRunner shape).
type hookCounter struct {
	count *int
}

func (h hookCounter) PreToolUse(context.Context, string, []byte) (bool, string, []byte) {
	*h.count++
	return false, "", nil
}
func (h hookCounter) PostToolUse(context.Context, string, []byte, []byte, bool) { *h.count++ }
func (h hookCounter) Stop(context.Context, []llm.Message) (bool, []string, string) {
	*h.count++
	return false, nil, ""
}
