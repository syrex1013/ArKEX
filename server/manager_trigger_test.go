package server

import (
	"testing"

	"github.com/Autumn-27/artex/offensive/oast"
)

// A planning round that fails after drainTriggers must not lose the events it
// was woken for: requeueTriggers puts them back, in order, ahead of anything
// that accumulated since.
func TestRequeueTriggersRestoresDrainedEvents(t *testing.T) {
	task := &Task{ID: "t1", notify: make(chan struct{}, 1)}
	task.NotifyDone(3)
	task.NotifyFinding(3, "sqli in /api/login")
	drained := task.drainTriggers()
	if len(drained) != 2 {
		t.Fatalf("drained %d events, want 2", len(drained))
	}
	task.requeueTriggers(drained)
	// New events arriving after the failed round must come after the requeued ones.
	task.NotifyDone(5)
	rest := task.drainTriggers()
	if len(rest) != 3 {
		t.Fatalf("re-drained %d events, want 3", len(rest))
	}
	if rest[0].Kind != "done" || rest[0].IntentID != 3 ||
		rest[1].Kind != "finding" || rest[1].IntentID != 3 || rest[1].Detail != "sqli in /api/login" ||
		rest[2].Kind != "done" || rest[2].IntentID != 5 {
		t.Fatalf("trigger order lost after requeue: %+v", rest)
	}
	// Empty requeue is a no-op (never appends a nil element).
	task.requeueTriggers(nil)
	if ev := task.drainTriggers(); len(ev) != 0 {
		t.Fatalf("empty requeue must be a no-op, got %+v", ev)
	}
}

// NotifyOASTHit appends an oast_hit trigger and never blocks: nil and full
// notify channels are both safe.
func TestNotifyOASTHitQueuesTrigger(t *testing.T) {
	task := &Task{ID: "t1"} // notify channel nil: Notify must not block
	task.NotifyOASTHit(&oast.Callback{PayloadTag: "sqli-oob-abc.oast.local", Protocol: "http", RemoteAddr: "1.2.3.4"})
	ev := task.drainTriggers()
	if len(ev) != 1 || ev[0].Kind != "oast_hit" ||
		ev[0].Detail != "OAST callback hit: payload=sqli-oob-abc.oast.local protocol=http remote=1.2.3.4" {
		t.Fatalf("unexpected trigger: %+v", ev)
	}
	// nil callback is ignored.
	task.NotifyOASTHit(nil)
	if ev := task.drainTriggers(); len(ev) != 0 {
		t.Fatalf("nil callback must not queue a trigger: %+v", ev)
	}
}
