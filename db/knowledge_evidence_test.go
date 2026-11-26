package db

// Evidence-backed project knowledge: support counts distinct sources (not
// extraction runs), retrieval gating never lets one client's scoped knowledge
// reach another client's task, review state decides the order agents see items
// in, and supersession retires a row instead of rewriting it.

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"
)

// knowledgeFixture is the minimum an evidence test needs: a live DB, one company
// and one task, all removed again on cleanup.
type knowledgeFixture struct {
	d       *DB
	company int64
	task    *Task
	stamp   int64
}

func newKnowledgeFixture(t *testing.T, tag string) *knowledgeFixture {
	t.Helper()
	d, err := Open(testDSN(t))
	if err != nil {
		SkipPGUnavailable(t, err)
	}
	// Registered FIRST so it runs LAST, after the row cleanup below.
	t.Cleanup(func() { d.Close() })

	stamp := time.Now().UnixNano()
	company, _, err := d.Companies().UpsertCompany(fmt.Sprintf("KnowledgeEvidence %s %d", tag, stamp), "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cleanupCompany(d, company) })
	task, err := d.CreateTaskWithOptions(fmt.Sprintf("knowledge-%s-%d", tag, stamp), "goal",
		TaskCreateOptions{CompanyIDs: []int64{company}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cleanupErr(t, "knowledge task", d.DeleteTask(task.ID)) })
	return &knowledgeFixture{d: d, company: company, task: task, stamp: stamp}
}

// cleanupTitles removes the knowledge rows this test created, matched exactly so
// a neighbouring test's rows are never touched.
func (f *knowledgeFixture) cleanupTitles(t *testing.T, titles ...string) {
	t.Helper()
	t.Cleanup(func() {
		cleanupErr(t, "knowledge items", execCleanup(f.d, `DELETE FROM knowledge_items WHERE title = ANY($1)`, titles))
	})
}

// supportOf reads the stored support count, which is what the merge semantics
// under test must move (or hold still).
func supportOf(t *testing.T, d *DB, id int64) int {
	t.Helper()
	var support int
	if err := d.QueryRow(`SELECT support FROM knowledge_items WHERE id=$1`, id).Scan(&support); err != nil {
		t.Fatal(err)
	}
	return support
}

// TestUpsertKnowledgeSourceIdempotency: support is the number of distinct
// evidence sources. Re-recording the identical source (same content hash) leaves
// it alone; a new source id or a new source kind is one more piece of evidence;
// confidence only ever rises.
func TestUpsertKnowledgeSourceIdempotency(t *testing.T) {
	f := newKnowledgeFixture(t, "source")
	f.cleanupTitles(t, fmt.Sprintf("source idempotency %d", f.stamp))

	ctx := context.Background()
	taskID := f.task.ID
	title := fmt.Sprintf("source idempotency %d", f.stamp)
	body := "the WAF rejects quote payloads on /search"
	item := KnowledgeItem{
		Kind: KnowledgeLesson, Title: title, Body: body, Confidence: 40, SourceTaskID: &taskID,
		Source: &KnowledgeSource{Kind: "task", ID: taskID, ContentHash: KnowledgeContentHash(KnowledgeLesson, title, body)},
	}

	id, err := f.d.UpsertKnowledge(ctx, item)
	if err != nil {
		t.Fatal(err)
	}
	if got := supportOf(t, f.d, id); got != 1 {
		t.Fatalf("first evidence support=%d, want 1", got)
	}

	// The same extraction run again: same identity, same source, same hash.
	again, err := f.d.UpsertKnowledge(ctx, item)
	if err != nil {
		t.Fatal(err)
	}
	if again != id {
		t.Fatalf("identical item got a second id %d, want merge into %d", again, id)
	}
	if got := supportOf(t, f.d, id); got != 1 {
		t.Fatalf("re-extracting the identical source raised support to %d, want 1", got)
	}

	// A different source record is genuinely new evidence.
	otherTask := item
	otherTask.Source = &KnowledgeSource{Kind: "task", ID: taskID + 1,
		ContentHash: KnowledgeContentHash(KnowledgeLesson, title, body)}
	if _, err := f.d.UpsertKnowledge(ctx, otherTask); err != nil {
		t.Fatal(err)
	}
	if got := supportOf(t, f.d, id); got != 2 {
		t.Fatalf("support after a second task source=%d, want 2", got)
	}

	// The same id under a different source kind is separate evidence too.
	otherKind := item
	otherKind.Source = &KnowledgeSource{Kind: "finding", ID: taskID,
		ContentHash: KnowledgeContentHash(KnowledgeLesson, title, body)}
	if _, err := f.d.UpsertKnowledge(ctx, otherKind); err != nil {
		t.Fatal(err)
	}
	if got := supportOf(t, f.d, id); got != 3 {
		t.Fatalf("support after a finding source=%d, want 3", got)
	}

	// Confidence is the greatest value ever seen, never the latest one.
	raised := item
	raised.Confidence = 90
	if _, err := f.d.UpsertKnowledge(ctx, raised); err != nil {
		t.Fatal(err)
	}
	lowered := item
	lowered.Confidence = 10
	if _, err := f.d.UpsertKnowledge(ctx, lowered); err != nil {
		t.Fatal(err)
	}
	stored, err := f.d.GetKnowledge(id)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Confidence != 90 {
		t.Fatalf("confidence=%d after a lower re-observation, want 90", stored.Confidence)
	}
	if stored.Support != 3 {
		t.Fatalf("support=%d after confidence-only merges, want 3", stored.Support)
	}
}

// TestKnowledgeRetrievalScopeGating: an item bound to a company or engagement can
// never be rescued by an asset-key match from another client's task, while an
// item scoped to the asset key alone reaches every task that links that asset.
func TestKnowledgeRetrievalScopeGating(t *testing.T) {
	d, err := Open(testDSN(t))
	if err != nil {
		SkipPGUnavailable(t, err)
	}
	t.Cleanup(func() { d.Close() })

	stamp := time.Now().UnixNano()
	engA, err := d.CreateEngagement(fmt.Sprintf("__test_knowledge_gate_a_%d__", stamp), "scope gating A")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cleanupErr(t, "engagement A", execCleanup(d, `DELETE FROM engagements WHERE id=$1`, engA.ID)) })
	engB, err := d.CreateEngagement(fmt.Sprintf("__test_knowledge_gate_b_%d__", stamp), "scope gating B")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cleanupErr(t, "engagement B", execCleanup(d, `DELETE FROM engagements WHERE id=$1`, engB.ID)) })

	engAID, engBID := engA.ID, engB.ID
	taskA, err := d.CreateTaskWithOptions(fmt.Sprintf("knowledge gate A %d", stamp), "goal",
		TaskCreateOptions{EngagementID: &engAID})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cleanupErr(t, "task A", d.DeleteTask(taskA.ID)) })
	taskB, err := d.CreateTaskWithOptions(fmt.Sprintf("knowledge gate B %d", stamp), "goal",
		TaskCreateOptions{EngagementID: &engBID})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cleanupErr(t, "task B", d.DeleteTask(taskB.ID)) })

	// Both engagements' tasks see the same shared host.
	domain := fmt.Sprintf("shared-%d.gate.example", stamp)
	assetID, err := d.Assets().UpsertRootDomain(UpsertRootDomainReq{Domain: domain, TaskID: taskA.ID})
	if err != nil {
		t.Fatal(err)
	}
	for _, taskID := range []int64{taskA.ID, taskB.ID} {
		if _, err := d.Exec(`INSERT INTO task_asset_links(task_id, asset_id) VALUES ($1,$2) ON CONFLICT DO NOTHING`,
			taskID, assetID); err != nil {
			t.Fatal(err)
		}
	}

	ctx := context.Background()
	pinnedTitle := fmt.Sprintf("engagement pinned fact %d", stamp)
	sharedTitle := fmt.Sprintf("asset only fact %d", stamp)
	t.Cleanup(func() {
		cleanupErr(t, "knowledge items", execCleanup(d, `DELETE FROM knowledge_items WHERE title = ANY($1)`,
			[]string{pinnedTitle, sharedTitle}))
	})

	// Bound to engagement A *and* the shared asset: the asset key match must not
	// carry it across to engagement B.
	pinned, err := d.UpsertKnowledge(ctx, KnowledgeItem{
		Kind: KnowledgeLesson, Title: pinnedTitle, Body: "staging admin panel is open on 8443",
		ScopeEngagement: &engAID, ScopeAssetKey: domain, Confidence: 70,
	})
	if err != nil {
		t.Fatal(err)
	}
	// Scoped to the asset key only: shared infrastructure knowledge, so every task
	// that links the asset gets it.
	shared, err := d.UpsertKnowledge(ctx, KnowledgeItem{
		Kind: KnowledgeDependency, Title: sharedTitle, Body: "reaches the legacy auth service",
		ScopeAssetKey: domain, Confidence: 60,
	})
	if err != nil {
		t.Fatal(err)
	}

	titlesFor := func(taskID int64, wantIDs ...int64) map[int64]string {
		t.Helper()
		got, err := d.SearchKnowledgeCandidates(taskID, 200)
		if err != nil {
			t.Fatal(err)
		}
		want := map[int64]bool{}
		for _, id := range wantIDs {
			want[id] = true
		}
		out := map[int64]string{}
		for _, k := range got {
			if want[k.ID] {
				out[k.ID] = k.Title
			}
		}
		return out
	}

	if got := titlesFor(taskA.ID, pinned, shared); len(got) != 2 {
		t.Fatalf("engagement A retrieved %d of its 2 items: %v", len(got), got)
	}
	gotB := titlesFor(taskB.ID, pinned, shared)
	if _, leaked := gotB[pinned]; leaked {
		t.Fatalf("engagement A's engagement+asset item leaked into engagement B: %v", gotB)
	}
	if gotB[shared] != sharedTitle {
		t.Fatalf("asset-key item missing for engagement B's task: %v", gotB)
	}
}

// TestKnowledgeReviewStateOrdering: current items outrank disputed ones,
// flipping the review state reorders retrieval, and a state outside the three the
// schema knows about is rejected.
func TestKnowledgeReviewStateOrdering(t *testing.T) {
	f := newKnowledgeFixture(t, "review")
	ctx := context.Background()
	titleCurrent := fmt.Sprintf("review current %d", f.stamp)
	titleDisputed := fmt.Sprintf("review disputed %d", f.stamp)
	f.cleanupTitles(t, titleCurrent, titleDisputed)

	company := f.company
	idCurrent, err := f.d.UpsertKnowledge(ctx, KnowledgeItem{Kind: KnowledgeLesson,
		Title: titleCurrent, Body: "supports TLS 1.3", ScopeCompanyID: &company, Confidence: 70})
	if err != nil {
		t.Fatal(err)
	}
	idDisputed, err := f.d.UpsertKnowledge(ctx, KnowledgeItem{Kind: KnowledgeLesson,
		Title: titleDisputed, Body: "login has no rate limit", ScopeCompanyID: &company, Confidence: 70})
	if err != nil {
		t.Fatal(err)
	}
	if err := f.d.SetKnowledgeReviewState(idDisputed, KnowledgeReviewDisputed); err != nil {
		t.Fatal(err)
	}

	// orderTitles returns this test's two items in retrieval order.
	orderTitles := func() []string {
		t.Helper()
		got, err := f.d.SearchKnowledgeCandidates(f.task.ID, 200)
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != 2 {
			t.Fatalf("retrieved %d items, want this test's 2: %+v", len(got), got)
		}
		out := make([]string, 0, len(got))
		for _, k := range got {
			out = append(out, k.Title)
		}
		return out
	}

	order := orderTitles()
	if order[0] != titleCurrent || order[1] != titleDisputed {
		t.Fatalf("order = %v, want current first", order)
	}
	// The ordering comes from review_state, and the column is exposed to the agent.
	if k, err := f.d.GetKnowledge(idDisputed); err != nil || k == nil || k.ReviewState != KnowledgeReviewDisputed {
		t.Fatalf("review state not stored: %+v err=%v", k, err)
	}

	// Flip both: retrieval follows the operator's judgement.
	if err := f.d.SetKnowledgeReviewState(idCurrent, KnowledgeReviewDisputed); err != nil {
		t.Fatal(err)
	}
	if err := f.d.SetKnowledgeReviewState(idDisputed, KnowledgeReviewCurrent); err != nil {
		t.Fatal(err)
	}
	order = orderTitles()
	if order[0] != titleDisputed || order[1] != titleCurrent {
		t.Fatalf("order after flip = %v, want the newly current item first", order)
	}

	// Unknown states are refused and leave the row as it was.
	if err := f.d.SetKnowledgeReviewState(idCurrent, "trusted"); !errors.Is(err, ErrKnowledgeReviewState) {
		t.Fatalf("unknown review state: err=%v, want ErrKnowledgeReviewState", err)
	}
	if k, err := f.d.GetKnowledge(idCurrent); err != nil || k == nil || k.ReviewState != KnowledgeReviewDisputed {
		t.Fatalf("rejected state leaked into the row: %+v err=%v", k, err)
	}
}

// TestSupersedeKnowledge: the replacement becomes the active row, the old row is
// retired with superseded_by pointing at it, evidence carries over, and the old
// body is left exactly as it was.
func TestSupersedeKnowledge(t *testing.T) {
	f := newKnowledgeFixture(t, "supersede")
	ctx := context.Background()
	oldTitle := fmt.Sprintf("supersede old %d", f.stamp)
	newTitle := fmt.Sprintf("supersede new %d", f.stamp)
	f.cleanupTitles(t, oldTitle, newTitle)

	company, taskID := f.company, f.task.ID
	oldBody := "the admin panel is reachable from the internet"
	oldID, err := f.d.UpsertKnowledge(ctx, KnowledgeItem{
		Kind: KnowledgeAssetFact, Title: oldTitle, Body: oldBody, ScopeCompanyID: &company,
		SourceTaskID: &taskID, Confidence: 50,
		Source: &KnowledgeSource{Kind: "task", ID: taskID,
			ContentHash: KnowledgeContentHash(KnowledgeAssetFact, oldTitle, oldBody)},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := supportOf(t, f.d, oldID); got != 1 {
		t.Fatalf("old item support=%d, want 1", got)
	}

	newID, err := f.d.SupersedeKnowledge(ctx, oldID, KnowledgeItem{
		Kind: KnowledgeAssetFact, Title: newTitle, Body: "the admin panel now requires VPN",
		ScopeCompanyID: &company, Confidence: 80})
	if err != nil {
		t.Fatal(err)
	}
	if newID == oldID {
		t.Fatalf("supersede reused the old row %d", oldID)
	}

	old, err := f.d.GetKnowledge(oldID)
	if err != nil {
		t.Fatal(err)
	}
	if old.Status != KnowledgeRetired {
		t.Fatalf("old status=%q, want retired", old.Status)
	}
	if old.SupersededBy == nil || *old.SupersededBy != newID {
		t.Fatalf("old superseded_by=%v, want %d", old.SupersededBy, newID)
	}
	if old.Body != oldBody {
		t.Fatalf("old body rewritten to %q, want %q", old.Body, oldBody)
	}

	replacement, err := f.d.GetKnowledge(newID)
	if err != nil {
		t.Fatal(err)
	}
	if replacement.Status != KnowledgeActive {
		t.Fatalf("replacement status=%q, want active", replacement.Status)
	}
	if replacement.Support != 1 {
		t.Fatalf("replacement support=%d, want the carried-over evidence of 1", replacement.Support)
	}
	if replacement.Confidence != 80 {
		t.Fatalf("replacement confidence=%d, want 80", replacement.Confidence)
	}

	// Evidence moved with the claim: the replacement is backed by the same source
	// row, and the retired item keeps its own copy so the history stays auditable.
	var carried, kept int
	if err := f.d.QueryRow(`SELECT count(*) FROM knowledge_item_sources WHERE item_id=$1`, newID).Scan(&carried); err != nil {
		t.Fatal(err)
	}
	if err := f.d.QueryRow(`SELECT count(*) FROM knowledge_item_sources WHERE item_id=$1`, oldID).Scan(&kept); err != nil {
		t.Fatal(err)
	}
	if carried != 1 || kept != 1 {
		t.Fatalf("evidence rows: replacement=%d old=%d, want 1 and 1", carried, kept)
	}

	// The retired item no longer reaches a task's retrieval.
	got, err := f.d.SearchKnowledgeCandidates(f.task.ID, 200)
	if err != nil {
		t.Fatal(err)
	}
	for _, k := range got {
		if k.ID == oldID {
			t.Fatalf("retired item still retrieved: %+v", k)
		}
		if k.ID == newID && k.Title != newTitle {
			t.Fatalf("replacement title=%q, want %q", k.Title, newTitle)
		}
	}

	// A replacement with the old row's own identity would rewrite history.
	if _, err := f.d.SupersedeKnowledge(ctx, oldID, KnowledgeItem{Kind: KnowledgeAssetFact,
		Title: oldTitle, Body: "silently rewritten", ScopeCompanyID: &company}); err == nil {
		t.Fatal("supersede with the replaced item's own identity accepted")
	}
}
