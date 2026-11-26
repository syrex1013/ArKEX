package db

// Engagement-level records: CRUD, status transitions, review cursors,
// the knowledge-injection log and the unioned timeline.

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"
)

// engagementRecordsFixture creates an isolated engagement + two tasks and
// registers cleanup (tasks first, then the engagement cascade).
type recordsFixture struct {
	d      *DB
	eng    *Engagement
	task1  *Task
	task2  *Task
	assets *AssetStore
}

func newRecordsFixture(t *testing.T) *recordsFixture {
	d, err := Open(testDSN(t))
	if err != nil {
		SkipPGUnavailable(t, err)
	}
	// Registered FIRST so it runs LAST, after the row cleanup below.
	t.Cleanup(func() { d.Close() })
	uniq := fmt.Sprintf("__test_eng_records_%d__", time.Now().UnixNano())
	eng, err := d.CreateEngagement(uniq, "records fixture")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, err := d.DeleteEngagement(eng.ID)
		cleanupErr(t, "engagement", err)
	})
	engID := eng.ID
	task1, err := d.CreateTaskWithOptions("records task one", "goal", TaskCreateOptions{EngagementID: &engID})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cleanupErr(t, "task1", d.DeleteTask(task1.ID)) })
	task2, err := d.CreateTaskWithOptions("records task two", "goal", TaskCreateOptions{EngagementID: &engID})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cleanupErr(t, "task2", d.DeleteTask(task2.ID)) })
	return &recordsFixture{d: d, eng: eng, task1: task1, task2: task2, assets: d.Assets()}
}

func TestEngagementRecordCRUD(t *testing.T) {
	f := newRecordsFixture(t)

	// Validation.
	if _, err := f.d.CreateEngagementRecord(f.eng.ID, &EngagementRecord{Kind: "nope", Title: "x"}); err == nil {
		t.Fatal("unknown kind accepted")
	}
	if _, err := f.d.CreateEngagementRecord(f.eng.ID, &EngagementRecord{Kind: RecordKindNote, Title: "   "}); err == nil {
		t.Fatal("empty title accepted")
	}
	if _, err := f.d.CreateEngagementRecord(f.eng.ID, &EngagementRecord{Kind: RecordKindNote, Title: "x", SupersedesID: int64Ptr(999999)}); err == nil {
		t.Fatal("dangling supersedes accepted")
	}

	// Create with payload + source link.
	payload := json.RawMessage(`{"added":["host.example"]}`)
	r1, err := f.d.CreateEngagementRecord(f.eng.ID, &EngagementRecord{
		Kind: RecordKindScopeRevision, Title: "Add staging host", Payload: payload,
		Actor: "operator", SourceKind: "task", SourceID: &f.task1.ID,
	})
	if err != nil {
		t.Fatal(err)
	}
	if r1.Status != RecordStatusOpen {
		t.Fatalf("default status = %q, want open", r1.Status)
	}
	var got map[string]any
	if err := json.Unmarshal(r1.Payload, &got); err != nil {
		t.Fatal(err)
	}
	added, ok := got["added"].([]any)
	if !ok || len(added) != 1 || added[0] != "host.example" {
		t.Fatalf("payload roundtrip = %v", got)
	}

	r2, err := f.d.CreateEngagementRecord(f.eng.ID, &EngagementRecord{Kind: RecordKindOperatorDecision, Title: "Focus on internal net"})
	if err != nil {
		t.Fatal(err)
	}

	// List newest first.
	got3, err := f.d.ListEngagementRecords(f.eng.ID, nil, "", 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(got3) != 2 || got3[0].ID != r2.ID || got3[1].ID != r1.ID {
		t.Fatalf("list order = %d entries, first %d", len(got3), firstID(got3))
	}

	// Kinds filter.
	gotRev, err := f.d.ListEngagementRecords(f.eng.ID, []string{RecordKindScopeRevision}, "", 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(gotRev) != 1 || gotRev[0].ID != r1.ID {
		t.Fatalf("kinds filter: %d entries", len(gotRev))
	}

	// Cursor pagination: only records below r1.
	gotOlder, err := f.d.ListEngagementRecords(f.eng.ID, nil, "", r1.ID, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(gotOlder) != 0 {
		t.Fatalf("sinceID cursor: %d entries past newest", len(gotOlder))
	}

	// Get semantics via RecordsSinceID.
	since, err := f.d.RecordsSinceID(f.eng.ID, r1.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(since) != 1 || since[0].ID != r2.ID {
		t.Fatalf("records since r1: %d entries", len(since))
	}
}

func firstID(recs []EngagementRecord) int64 {
	if len(recs) == 0 {
		return -1
	}
	return recs[0].ID
}

func int64Ptr(v int64) *int64 { return &v }

func TestEngagementRecordMilestoneCriteria(t *testing.T) {
	f := newRecordsFixture(t)

	m, err := f.d.CreateEngagementRecord(f.eng.ID, &EngagementRecord{Kind: RecordKindMilestone, Title: "Confirm one critical", Actor: "op"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.d.UpdateEngagementRecordStatus(m.ID, RecordStatusDone); err == nil {
		t.Fatal("milestone completed without criteria")
	}
	if _, err := f.d.Exec(`UPDATE engagement_records SET criteria='critical finding confirmed by retest' WHERE id=$1`, m.ID); err != nil {
		t.Fatal(err)
	}
	done, err := f.d.UpdateEngagementRecordStatus(m.ID, RecordStatusDone)
	if err != nil {
		t.Fatal(err)
	}
	if done.Status != RecordStatusDone || done.CompletedAt == nil {
		t.Fatalf("done transition: status=%q completed_at=%v", done.Status, done.CompletedAt)
	}

	// Unknown status rejected.
	if _, err := f.d.UpdateEngagementRecordStatus(m.ID, "wild"); err == nil {
		t.Fatal("unknown status accepted")
	}
}

func TestEngagementRecordAcceptSupersedes(t *testing.T) {
	f := newRecordsFixture(t)

	v1, err := f.d.CreateEngagementRecord(f.eng.ID, &EngagementRecord{Kind: RecordKindScopeRevision, Title: "v1", Payload: json.RawMessage(`{"version":1}`)})
	if err != nil {
		t.Fatal(err)
	}
	v2, err := f.d.CreateEngagementRecord(f.eng.ID, &EngagementRecord{Kind: RecordKindScopeRevision, Title: "v2", Payload: json.RawMessage(`{"version":2}`)})
	if err != nil {
		t.Fatal(err)
	}

	// Accept v1: nothing superseded yet.
	a1, err := f.d.UpdateEngagementRecordStatus(v1.ID, RecordStatusAccepted)
	if err != nil {
		t.Fatal(err)
	}
	if a1.Status != RecordStatusAccepted || a1.SupersedesID != nil {
		t.Fatalf("accept v1: status=%q supersedes=%v", a1.Status, a1.SupersedesID)
	}

	// Accept v2: v1 flips to superseded, v2 links back to it.
	a2, err := f.d.UpdateEngagementRecordStatus(v2.ID, RecordStatusAccepted)
	if err != nil {
		t.Fatal(err)
	}
	if a2.Status != RecordStatusAccepted || a2.SupersedesID == nil || *a2.SupersedesID != v1.ID {
		t.Fatalf("accept v2: status=%q supersedes=%v, want %d", a2.Status, a2.SupersedesID, v1.ID)
	}
	v1After, err := f.d.GetEngagementRecord(v1.ID)
	if err != nil {
		t.Fatal(err)
	}
	if v1After.Status != RecordStatusSuperseded {
		t.Fatalf("v1 status after v2 accepted = %q, want superseded", v1After.Status)
	}

	// Active revision = latest accepted.
	active, err := f.d.ListEngagementRecords(f.eng.ID, []string{RecordKindScopeRevision}, RecordStatusAccepted, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(active) != 1 || active[0].ID != v2.ID {
		t.Fatalf("active revisions = %d, want v2 only", len(active))
	}

	// Superseded records are immutable.
	if _, err := f.d.UpdateEngagementRecordStatus(v1.ID, RecordStatusAccepted); err == nil {
		t.Fatal("superseded record re-accepted")
	}

	// Same supersede rule for decisions.
	d1, err := f.d.CreateEngagementRecord(f.eng.ID, &EngagementRecord{Kind: RecordKindOperatorDecision, Title: "d1"})
	if err != nil {
		t.Fatal(err)
	}
	d2, err := f.d.CreateEngagementRecord(f.eng.ID, &EngagementRecord{Kind: RecordKindOperatorDecision, Title: "d2"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.d.UpdateEngagementRecordStatus(d1.ID, RecordStatusAccepted); err != nil {
		t.Fatal(err)
	}
	if _, err := f.d.UpdateEngagementRecordStatus(d2.ID, RecordStatusAccepted); err != nil {
		t.Fatal(err)
	}
	d1After, err := f.d.GetEngagementRecord(d1.ID)
	if err != nil {
		t.Fatal(err)
	}
	if d1After.Status != RecordStatusSuperseded {
		t.Fatalf("decision d1 = %q, want superseded", d1After.Status)
	}
}

func TestEngagementReviewsCursor(t *testing.T) {
	f := newRecordsFixture(t)

	none, err := f.d.LatestEngagementReview(f.eng.ID, "alice")
	if err != nil {
		t.Fatal(err)
	}
	if none != nil {
		t.Fatal("review returned before any review")
	}

	r1, err := f.d.CreateEngagementRecord(f.eng.ID, &EngagementRecord{Kind: RecordKindNote, Title: "before review"})
	if err != nil {
		t.Fatal(err)
	}
	maxID, err := f.d.MaxRecordID(f.eng.ID)
	if err != nil {
		t.Fatal(err)
	}
	if maxID != r1.ID {
		t.Fatalf("max record id = %d, want %d", maxID, r1.ID)
	}

	rv, err := f.d.AddEngagementReview(f.eng.ID, "alice", maxID)
	if err != nil {
		t.Fatal(err)
	}
	if rv.UpToRecordID != r1.ID {
		t.Fatalf("review cursor = %d, want %d", rv.UpToRecordID, r1.ID)
	}
	if _, err := f.d.AddEngagementReview(f.eng.ID, "  ", 0); err == nil {
		t.Fatal("review without operator accepted")
	}

	// Delta since the review cursor excludes the already-reviewed record.
	r2, err := f.d.CreateEngagementRecord(f.eng.ID, &EngagementRecord{Kind: RecordKindNote, Title: "after review"})
	if err != nil {
		t.Fatal(err)
	}
	delta, err := f.d.RecordsSinceID(f.eng.ID, rv.UpToRecordID)
	if err != nil {
		t.Fatal(err)
	}
	if len(delta) != 1 || delta[0].ID != r2.ID {
		t.Fatalf("delta = %d entries, want only r2", len(delta))
	}

	latest, err := f.d.LatestEngagementReview(f.eng.ID, "alice")
	if err != nil {
		t.Fatal(err)
	}
	if latest == nil || latest.ID != rv.ID {
		t.Fatalf("latest review id = %d", latest.ID)
	}
	// Per-operator isolation.
	other, err := f.d.LatestEngagementReview(f.eng.ID, "bob")
	if err != nil {
		t.Fatal(err)
	}
	if other != nil {
		t.Fatal("bob sees alice's review")
	}
}

func TestKnowledgeInjectionLog(t *testing.T) {
	f := newRecordsFixture(t)

	store := f.d.Exploration(f.task1.ExplorationID)
	intentID, err := store.AddNode(KindIntent, map[string]any{"summary": "injection intent"}, 1, "open", "planner", nil)
	if err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()
	item1, err := f.d.UpsertKnowledge(ctx, KnowledgeItem{Kind: "asset_fact", Title: fmt.Sprintf("injection fact %d", f.task1.ID), Body: "body", SourceTaskID: &f.task1.ID})
	if err != nil {
		t.Fatal(err)
	}
	item2, err := f.d.UpsertKnowledge(ctx, KnowledgeItem{Kind: "asset_fact", Title: fmt.Sprintf("injection fact 2 %d", f.task1.ID), Body: "body", SourceTaskID: &f.task1.ID})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cleanupErr(t, "knowledge", execCleanup(f.d, `DELETE FROM knowledge_items WHERE source_task_id=$1 AND kind='asset_fact' AND title LIKE 'injection fact%'`, f.task1.ID))
	})

	if err := f.d.LogKnowledgeInjection(item1, f.task1.ID, &intentID, "worker#0", "inject"); err != nil {
		t.Fatal(err)
	}
	if err := f.d.LogKnowledgeInjection(item2, f.task1.ID, nil, "planner", ""); err != nil {
		t.Fatal(err)
	}

	rows, err := f.d.ListKnowledgeInjections(f.task1.ID, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 {
		t.Fatalf("injections = %d rows, want 2", len(rows))
	}
	// Newest first: the second insert (planner, default shadow mode) comes first.
	if rows[0].Mode != "shadow" {
		t.Fatalf("empty mode default = %q, want shadow", rows[0].Mode)
	}
	if rows[0].AgentName != "planner" {
		t.Fatalf("agent = %q, want planner", rows[0].AgentName)
	}
	if rows[1].Mode != "inject" {
		t.Fatalf("explicit mode = %q, want inject", rows[1].Mode)
	}
	if rows[1].IntentID == nil || *rows[1].IntentID != intentID {
		t.Fatalf("intent link = %v, want %d", rows[1].IntentID, intentID)
	}
	if rows[1].AgentName != "worker#0" {
		t.Fatalf("agent = %q, want worker#0", rows[1].AgentName)
	}

	// Task-scoped: task2 sees nothing.
	empty, err := f.d.ListKnowledgeInjections(f.task2.ID, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(empty) != 0 {
		t.Fatalf("task2 injections = %d, want 0", len(empty))
	}
}

func TestEngagementTimeline(t *testing.T) {
	f := newRecordsFixture(t)

	// Batch 1: everything before the cursor.
	if _, err := f.d.CreateEngagementRecord(f.eng.ID, &EngagementRecord{Kind: RecordKindScopeRevision, Title: "old scope"}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.d.AddFinding(f.task1.ID, 0, "timeline_vulnclass", "old finding", SeverityLow, "old summary", "", "test", nil); err != nil {
		t.Fatal(err)
	}
	time.Sleep(1100 * time.Millisecond)
	since := time.Now().UTC()
	time.Sleep(1100 * time.Millisecond)

	// Batch 2: everything after the cursor.
	rev2, err := f.d.CreateEngagementRecord(f.eng.ID, &EngagementRecord{Kind: RecordKindOperatorDecision, Title: "new decision", Actor: "op", SourceKind: "task", SourceID: &f.task2.ID})
	if err != nil {
		t.Fatal(err)
	}
	newTask, err := f.d.CreateTaskWithOptions("timeline task", "goal", TaskCreateOptions{EngagementID: &f.eng.ID})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cleanupErr(t, "timeline task", f.d.DeleteTask(newTask.ID)) })
	if _, err := f.d.AddFinding(f.task1.ID, 0, "timeline_vulnclass", "new finding", SeverityHigh, "fresh summary", "", "test", nil); err != nil {
		t.Fatal(err)
	}
	// A completed retest verdict on the old finding (direct row; conversation nullable).
	var oldFindingID int64
	if err := f.d.QueryRow(`SELECT id FROM findings WHERE task_id=$1 AND name='old finding'`, f.task1.ID).Scan(&oldFindingID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.d.Exec(`INSERT INTO finding_retests (finding_id, status, verdict, finished_at, snapshot) VALUES ($1,'completed','reproduced',now(),'{}')`, oldFindingID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cleanupErr(t, "retest", execCleanup(f.d, `DELETE FROM finding_retests WHERE finding_id=$1`, oldFindingID))
	})

	store := f.d.Exploration(newTask.ExplorationID)
	intentID, err := store.AddNode(KindIntent, map[string]any{"summary": "timeline intent"}, 1, "open", "planner", nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.d.AddCheckpoint(IntentCheckpoint{TaskID: newTask.ID, IntentID: intentID, AgentName: "worker", Attempt: 1, NextStep: "probe /admin"}); err != nil {
		t.Fatal(err)
	}
	if err := f.assets.RecordCoverage(newTask.ID, CoverageEntry{Surface: "/admin", RiskArea: "auth", Outcome: OutcomeNeedsFollowUp}); err != nil {
		t.Fatal(err)
	}

	// Full timeline: only post-cursor entries, newest first.
	all, err := f.d.EngagementTimeline(f.eng.ID, since, nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	kindsSeen := map[string]bool{}
	for i, e := range all {
		if i > 0 && all[i-1].CreatedAt.Before(e.CreatedAt) {
			t.Fatalf("timeline not newest-first at %d: %v then %v", i, all[i-1].CreatedAt, e.CreatedAt)
		}
		kindsSeen[e.Kind] = true
	}
	for _, want := range []string{RecordKindOperatorDecision, EntryTaskOutcome, EntryFinding, EntryRetest, EntryCheckpoint, EntryCoverageGap} {
		if !kindsSeen[want] {
			t.Fatalf("timeline missing kind %q (saw %v)", want, kindsSeen)
		}
	}

	// Every entry carries a source and (for task-bound entries) a link.
	for _, e := range all {
		if e.SourceKind == "" || e.SourceID == 0 {
			t.Fatalf("entry %q has no source: %+v", e.Kind, e)
		}
		if e.TaskID != nil && e.Link == "" {
			t.Fatalf("task-bound entry %q has no link", e.Kind)
		}
	}

	// Deep-link sanity: the finding entry points at its task.
	var findingEntry *EngagementTimelineEntry
	for i := range all {
		if all[i].Kind == EntryFinding && all[i].Title == "new finding" {
			findingEntry = &all[i]
		}
	}
	if findingEntry == nil || findingEntry.TaskID == nil || *findingEntry.TaskID != f.task1.ID {
		t.Fatalf("finding entry: %+v", findingEntry)
	}

	// Kind filter.
	filtered, err := f.d.EngagementTimeline(f.eng.ID, since, []string{EntryFinding, EntryRetest}, 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range filtered {
		if e.Kind != EntryFinding && e.Kind != EntryRetest {
			t.Fatalf("filter leaked kind %q", e.Kind)
		}
	}

	// Limit.
	limited, err := f.d.EngagementTimeline(f.eng.ID, since, nil, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(limited) != 2 {
		t.Fatalf("limit: %d entries, want 2", len(limited))
	}
	if limited[0].CreatedAt.Before(limited[1].CreatedAt) {
		t.Fatal("limited page not newest-first")
	}

	// The record entry resolves to its row.
	_ = rev2
}

func execCleanup(d *DB, q string, args ...any) error {
	_, err := d.Exec(q, args...)
	return err
}
