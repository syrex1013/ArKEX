package db

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"
)

// briefSeed lays out one record of each shape the brief reads.
func briefSeed(t *testing.T, f *recordsFixture) (scopeID, decisionID, milestoneDatedID, milestonePlainID, briefID int64) {
	t.Helper()

	mk := func(kind, title string, mut func(*EngagementRecord)) int64 {
		rec := &EngagementRecord{Kind: kind, Title: title, Actor: "seed", Payload: json.RawMessage(`{}`)}
		if mut != nil {
			mut(rec)
		}
		created, err := f.d.CreateEngagementRecord(f.eng.ID, rec)
		if err != nil {
			t.Fatal(err)
		}
		return created.ID
	}

	scopeID = mk(RecordKindScopeRevision, "scope v1", func(r *EngagementRecord) {
		r.Status = RecordStatusAccepted
		r.Body = "assets: host.example added"
	})
	decisionID = mk(RecordKindOperatorDecision, "stop re-probing ustaw", nil)
	milestoneDatedID = mk(RecordKindMilestone, "shell by friday", func(r *EngagementRecord) {
		due := time.Now().UTC().Add(72 * time.Hour).Truncate(time.Second)
		r.DueAt = &due
		r.Criteria = "reproducible shell captured"
	})
	milestonePlainID = mk(RecordKindMilestone, "close coverage gaps", nil)
	briefID = mk(RecordKindBrief, "week 1 brief", func(r *EngagementRecord) {
		r.Status = RecordStatusDone
		r.Body = "deterministic summary"
	})

	if _, err := f.d.AddFinding(f.task1.ID, 0, "brief_vulnclass", "crit finding", SeverityCritical, "s", "", "seed", nil); err != nil {
		t.Fatal(err)
	}
	if _, err := f.d.AddFinding(f.task1.ID, 0, "brief_vulnclass", "open finding", SeverityHigh, "s", "", "seed", nil); err != nil {
		t.Fatal(err)
	}
	if _, err := f.d.Exec(`UPDATE findings SET status='confirmed' WHERE task_id=$1 AND name='crit finding'`, f.task1.ID); err != nil {
		t.Fatal(err)
	}
	if err := f.assets.RecordCoverage(f.task1.ID, CoverageEntry{
		Surface: "/admin", RiskArea: "auth", Outcome: OutcomeNeedsFollowUp,
		Evidence: "", Reason: "untested", Worker: "seed",
	}); err != nil {
		t.Fatal(err)
	}
	return scopeID, decisionID, milestoneDatedID, milestonePlainID, briefID
}

func TestEngagementBriefAssembles(t *testing.T) {
	f := newRecordsFixture(t)
	scopeID, decisionID, milestoneDatedID, milestonePlainID, _ := briefSeed(t, f)

	b, err := f.d.EngagementBrief(context.Background(), f.eng.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	if b.ActiveScope == nil || b.ActiveScope.ID != scopeID {
		t.Fatalf("active scope = %+v, want record %d", b.ActiveScope, scopeID)
	}
	if len(b.OpenDecisions) != 1 || b.OpenDecisions[0].ID != decisionID {
		t.Fatalf("open decisions = %+v", b.OpenDecisions)
	}
	if len(b.OpenMilestones) != 2 || b.OpenMilestones[0].ID != milestoneDatedID || b.OpenMilestones[1].ID != milestonePlainID {
		t.Fatalf("milestones not due-first: %+v", b.OpenMilestones)
	}
	if len(b.Tasks) != 2 {
		t.Fatalf("tasks = %d, want 2", len(b.Tasks))
	}
	out := b.Tasks[0].Outcome
	if out.Findings.Total != 2 || out.Findings.Confirmed != 1 || out.Findings.Open != 1 ||
		out.Findings.Critical != 1 || out.Findings.High != 0 {
		t.Fatalf("task1 outcome wrong: %+v", out.Findings)
	}
	if b.Severity.Total != 2 || b.Severity.Confirmed != 1 || b.Severity.Critical != 1 || b.Severity.High != 0 {
		t.Fatalf("engagement severity wrong: %+v", b.Severity)
	}
	if len(b.CoverageGaps) != 1 || b.CoverageGaps[0].RiskArea != "auth" {
		t.Fatalf("coverage gaps = %+v", b.CoverageGaps)
	}
	if !strings.Contains(b.CoverageGaps[0].Link, fmt.Sprintf("id=%d", f.task1.ID)) {
		t.Fatalf("gap link missing task: %s", b.CoverageGaps[0].Link)
	}
	// No review cursor: RecentRecords holds all five records.
	if len(b.RecentRecords) != 5 {
		t.Fatalf("recent records = %d, want 5", len(b.RecentRecords))
	}
	if b.RecentRecords[0].ID < b.RecentRecords[len(b.RecentRecords)-1].ID {
		t.Fatalf("recent records not newest-first")
	}
}

func TestEngagementBriefDeterministic(t *testing.T) {
	f := newRecordsFixture(t)
	briefSeed(t, f)

	marshal := func() []byte {
		b, err := f.d.EngagementBrief(context.Background(), f.eng.ID, "")
		if err != nil {
			t.Fatal(err)
		}
		raw, err := json.Marshal(b)
		if err != nil {
			t.Fatal(err)
		}
		return raw
	}
	a1, a2 := marshal(), marshal()
	g1, g2 := extractGeneratedAt(a1), extractGeneratedAt(a2)
	if g1 == "" || g1 == g2 {
		t.Fatalf("generated_at not distinct: %q vs %q", g1, g2)
	}
	n1 := strings.ReplaceAll(string(a1), g1, "")
	n2 := strings.ReplaceAll(string(a2), g2, "")
	if n1 != n2 {
		t.Fatalf("brief not byte-identical modulo generated_at:\n%s\n%s", n1, n2)
	}
}

func extractGeneratedAt(raw []byte) string {
	var probe struct {
		GeneratedAt string `json:"generated_at"`
	}
	if err := json.Unmarshal(raw, &probe); err != nil {
		return ""
	}
	return probe.GeneratedAt
}

func TestEngagementBriefReviewDelta(t *testing.T) {
	f := newRecordsFixture(t)
	_, _, _, _, briefID := briefSeed(t, f)

	maxID, err := f.d.MaxRecordID(f.eng.ID)
	if err != nil {
		t.Fatal(err)
	}
	if maxID < briefID {
		t.Fatalf("max record id %d < brief %d", maxID, briefID)
	}
	if _, err := f.d.AddEngagementReview(f.eng.ID, "alice", maxID); err != nil {
		t.Fatal(err)
	}

	// Record added after the review cursor.
	late, err := f.d.CreateEngagementRecord(f.eng.ID, &EngagementRecord{
		Kind: RecordKindNote, Title: "post-review note", Actor: "seed", Payload: json.RawMessage(`{}`),
	})
	if err != nil {
		t.Fatal(err)
	}

	b, err := f.d.EngagementBrief(context.Background(), f.eng.ID, "alice")
	if err != nil {
		t.Fatal(err)
	}
	if b.Review == nil || b.Review.Operator != "alice" || b.Review.UpToRecordID != maxID {
		t.Fatalf("review cursor wrong: %+v", b.Review)
	}
	if len(b.RecentRecords) != 1 || b.RecentRecords[0].ID != late.ID {
		t.Fatalf("delta records = %+v, want only %d", b.RecentRecords, late.ID)
	}

	// Without operator: full record list, no review section.
	bAll, err := f.d.EngagementBrief(context.Background(), f.eng.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	if bAll.Review != nil {
		t.Fatalf("operator-less brief carries review cursor")
	}
	if len(bAll.RecentRecords) != 6 {
		t.Fatalf("all records = %d, want 6", len(bAll.RecentRecords))
	}
}

func TestSortBriefMilestones(t *testing.T) {
	datedA := time.Date(2026, 1, 5, 0, 0, 0, 0, time.UTC)
	datedB := time.Date(2026, 1, 10, 0, 0, 0, 0, time.UTC)
	ms := []EngagementRecord{
		{ID: 3, DueAt: nil},
		{ID: 1, DueAt: &datedB},
		{ID: 2, DueAt: &datedA},
		{ID: 4, DueAt: nil},
	}
	sortBriefMilestones(ms)
	got := []int64{ms[0].ID, ms[1].ID, ms[2].ID, ms[3].ID}
	if got[0] != 2 || got[1] != 1 || got[2] != 3 || got[3] != 4 {
		t.Fatalf("milestone order = %v, want [2 1 3 4]", got)
	}
}
