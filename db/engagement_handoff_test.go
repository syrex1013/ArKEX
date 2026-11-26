package db

import (
	"fmt"
	"testing"
	"time"
)

// TestEngagementHandoffAcrossArchive proves a month-old engagement can be picked
// up again after its only task was archived: operator records and review cursors
// live at engagement level and survive the task disappearing, the brief keeps
// answering while the task is gone, and restoring the archive puts the task,
// its coverage gaps and its retest history back where the brief expects them.
// A later scope revision then supersedes the accepted one, and the brief follows.
func TestEngagementHandoffAcrossArchive(t *testing.T) {
	d, err := Open(testDSN(t))
	if err != nil {
		SkipPGUnavailable(t, err)
	}
	defer d.Close()
	suffix := fmt.Sprintf("%d", time.Now().UnixNano())

	eng, err := d.CreateEngagement("handoff eng "+suffix, "archive and resume")
	if err != nil {
		t.Fatal(err)
	}
	task, err := d.CreateTaskWithOptions("handoff task "+suffix, "prove the handoff path", TaskCreateOptions{EngagementID: &eng.ID})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		_, _ = d.Exec(`DELETE FROM task_archives WHERE task_id=$1`, task.ID)
		_ = d.DeleteTask(task.ID)
		_, _ = d.DeleteEngagement(eng.ID)
	}()

	mkRecord := func(kind, title, body, status string) *EngagementRecord {
		rec, err := d.CreateEngagementRecord(eng.ID, &EngagementRecord{
			Kind: kind, Title: title, Body: body, Status: status, Actor: "operator",
		})
		if err != nil {
			t.Fatal(err)
		}
		return rec
	}
	scopeV1 := mkRecord(RecordKindScopeRevision, "Scope v1 "+suffix, "assets: handoff.example", RecordStatusAccepted)
	decision := mkRecord(RecordKindOperatorDecision, "stop re-probing handoff "+suffix, "wedge confirmed", "")
	milestone := mkRecord(RecordKindMilestone, "capture shell "+suffix, "", "")
	if _, err := d.UpdateEngagementRecordStatus(milestone.ID, RecordStatusDone); err == nil {
		t.Fatal("milestone without criteria must not complete")
	}

	store := d.Exploration(task.ExplorationID)
	intentID, err := store.AddNode(KindIntent, map[string]any{"summary": "enumerate /admin"}, 1, "open", "worker#0", nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := d.AddCheckpoint(IntentCheckpoint{
		TaskID: task.ID, IntentID: intentID, AgentName: "worker#0", Attempt: 1,
		Phase: "recon", NextStep: "enumerate /admin", Done: []string{"ported"}, Open: []string{"auth"},
	}); err != nil {
		t.Fatal(err)
	}
	findingID, err := d.AddFinding(task.ID, intentID, "auth_bypass", "handoff finding "+suffix, SeverityHigh,
		"login bypass", "curl ...", "worker#0", nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := d.Exec(`UPDATE findings SET status='confirmed', verification_status='verified' WHERE id=$1`, findingID); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Exec(`INSERT INTO finding_retests (finding_id,status,verdict,snapshot,summary,started_at,finished_at)
VALUES ($1,'completed','reproduced','{}','still bypasses',now(),now())`, findingID); err != nil {
		t.Fatal(err)
	}
	if err := d.Assets().RecordCoverage(task.ID, CoverageEntry{
		Surface: "/admin", RiskArea: "auth", Outcome: OutcomeNeedsFollowUp, Reason: "needs operator decision", Worker: "worker#0",
	}); err != nil {
		t.Fatal(err)
	}

	before, err := d.EngagementBrief(t.Context(), eng.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	if before.ActiveScope == nil || before.ActiveScope.ID != scopeV1.ID {
		t.Fatalf("brief active scope = %+v, want record %d", before.ActiveScope, scopeV1.ID)
	}
	if len(before.Tasks) != 1 || len(before.CoverageGaps) != 1 {
		t.Fatalf("brief before archive: %d tasks, %d gaps", len(before.Tasks), len(before.CoverageGaps))
	}
	if before.Tasks[0].Outcome.Retests.Reproduced != 1 {
		t.Fatalf("brief retests reproduced = %d, want 1", before.Tasks[0].Outcome.Retests.Reproduced)
	}

	if err := d.SetPaused(task.ID, true); err != nil {
		t.Fatal(err)
	}
	archive, err := d.QueueTaskArchive(task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := d.ClaimTaskArchiveJob(t.Context()); err != nil {
		t.Fatal(err)
	}
	snapshot, err := d.SnapshotTaskArchive(task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := d.CompleteTaskArchive(archive.ID, snapshot, "/tmp/handoff.tar.zst", "abc", 0, 0); err != nil {
		t.Fatal(err)
	}

	// Task gone: the engagement-level history must still answer.
	parked, err := d.EngagementBrief(t.Context(), eng.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(parked.Tasks) != 0 {
		t.Fatalf("brief after archive lists %d tasks, want 0", len(parked.Tasks))
	}
	if parked.ActiveScope == nil || parked.ActiveScope.ID != scopeV1.ID {
		t.Fatal("accepted scope revision must outlive the task")
	}
	records, err := d.ListEngagementRecords(eng.ID, nil, "", 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 3 {
		t.Fatalf("records after archive = %d, want 3 (scope, decision, milestone)", len(records))
	}
	if decision.ID == 0 {
		t.Fatal("decision record lost")
	}

	if _, err := d.QueueTaskArchiveRestore(archive.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := d.ClaimTaskArchiveJob(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err := d.RestoreTaskArchive(archive.ID, snapshot, int64((10 * time.Minute).Seconds())); err != nil {
		t.Fatalf("restore: %v", err)
	}
	if err := d.CompleteTaskArchiveRestore(archive.ID); err != nil {
		t.Fatal(err)
	}

	resumed, err := d.EngagementBrief(t.Context(), eng.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(resumed.Tasks) != 1 || resumed.Tasks[0].TaskID != task.ID {
		t.Fatalf("brief after restore tasks = %+v", resumed.Tasks)
	}
	if len(resumed.CoverageGaps) != 1 || resumed.CoverageGaps[0].RiskArea != "auth" {
		t.Fatalf("coverage gap not restored: %+v", resumed.CoverageGaps)
	}
	if resumed.Tasks[0].Outcome.Retests.Reproduced != 1 {
		t.Fatalf("retest history not restored: %+v", resumed.Tasks[0].Outcome.Retests)
	}
	var cpPhase string
	if err := d.QueryRow(`SELECT phase FROM intent_checkpoints WHERE task_id=$1 AND intent_id=$2`, task.ID, intentID).Scan(&cpPhase); err != nil {
		t.Fatalf("checkpoint not restored: %v", err)
	}
	if cpPhase != "recon" {
		t.Fatalf("restored checkpoint phase = %q", cpPhase)
	}

	// A new scope revision supersedes the accepted one; the brief follows.
	scopeV2 := mkRecord(RecordKindScopeRevision, "Scope v2 "+suffix, "assets: handoff.example, extra.example", RecordStatusAccepted)
	superseded, err := d.GetEngagementRecord(scopeV1.ID)
	if err != nil {
		t.Fatal(err)
	}
	if superseded.Status != RecordStatusSuperseded {
		t.Fatalf("scope v1 status = %q, want superseded", superseded.Status)
	}
	changed, err := d.EngagementBrief(t.Context(), eng.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	if changed.ActiveScope == nil || changed.ActiveScope.ID != scopeV2.ID {
		t.Fatalf("active scope = %+v, want %d", changed.ActiveScope, scopeV2.ID)
	}

	// Review cursor: only what landed after the operator's last review is new.
	upTo, err := d.MaxRecordID(eng.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := d.AddEngagementReview(eng.ID, "alice", upTo); err != nil {
		t.Fatal(err)
	}
	late := mkRecord(RecordKindNote, "post-review note "+suffix, "handoff resumed", "")
	delta, err := d.EngagementBrief(t.Context(), eng.ID, "alice")
	if err != nil {
		t.Fatal(err)
	}
	if delta.Review == nil || delta.Review.UpToRecordID != upTo {
		t.Fatalf("review cursor = %+v, want up_to %d", delta.Review, upTo)
	}
	if len(delta.RecentRecords) != 1 || delta.RecentRecords[0].ID != late.ID {
		t.Fatalf("changes since review = %+v, want only record %d", delta.RecentRecords, late.ID)
	}
}
