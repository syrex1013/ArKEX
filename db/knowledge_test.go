package db

import (
	"context"
	"fmt"
	"testing"
	"time"
)

// Project knowledge retrieval is company-scoped: a task only sees items scoped
// to its own company (or its own asset keys), never another client's.
func TestKnowledgeScopeRetrieval(t *testing.T) {
	d, err := Open(testDSN(t))
	if err != nil {
		SkipPGUnavailable(t, err)
	}
	t.Cleanup(func() { d.Close() })

	stamp := time.Now().UnixNano()
	cs := d.Companies()
	companyA, _, err := cs.UpsertCompany(fmt.Sprintf("KnowledgeA %d", stamp), "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cleanupCompany(d, companyA) })
	companyB, _, err := cs.UpsertCompany(fmt.Sprintf("KnowledgeB %d", stamp), "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cleanupCompany(d, companyB) })

	taskA, err := d.CreateTaskWithOptions(fmt.Sprintf("knowledge-a-%d", stamp), "goal", TaskCreateOptions{CompanyIDs: []int64{companyA}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cleanupErr(t, "knowledge task A", d.DeleteTask(taskA.ID)) })
	taskB, err := d.CreateTaskWithOptions(fmt.Sprintf("knowledge-b-%d", stamp), "goal", TaskCreateOptions{CompanyIDs: []int64{companyB}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cleanupErr(t, "knowledge task B", d.DeleteTask(taskB.ID)) })

	// One company-scoped item and one asset-key item, both sourced from task A's world.
	if _, err := d.Assets().UpsertRootDomain(UpsertRootDomainReq{
		Domain: fmt.Sprintf("api-%d.staging.example", stamp), TaskID: taskA.ID}); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	comp := companyA
	src := taskA.ID
	if _, err := d.UpsertKnowledge(ctx, KnowledgeItem{Kind: KnowledgeAssetFact,
		Title: fmt.Sprintf("vpn gate %d", stamp), Body: "vpn.corp.example routes to 10.0.0.0/8",
		ScopeCompanyID: &comp, SourceTaskID: &src, Confidence: 80}); err != nil {
		t.Fatal(err)
	}
	if _, err := d.UpsertKnowledge(ctx, KnowledgeItem{Kind: KnowledgeDependency,
		Title: fmt.Sprintf("api dep %d", stamp), Body: "api.staging relies on the legacy auth service",
		ScopeAssetKey: fmt.Sprintf("api-%d.staging.example", stamp), SourceTaskID: &src, Confidence: 60}); err != nil {
		t.Fatal(err)
	}

	gotA, err := d.SearchKnowledgeCandidates(taskA.ID, 50)
	if err != nil {
		t.Fatal(err)
	}
	if len(gotA) != 2 {
		t.Fatalf("task A retrieved %d items, want 2: %+v", len(gotA), gotA)
	}
	for _, k := range gotA {
		if k.SourceTaskID == nil || *k.SourceTaskID != taskA.ID {
			t.Fatalf("scope columns not preserved: %+v", k)
		}
		if k.Status != KnowledgeActive {
			t.Fatalf("item must be active: %+v", k)
		}
	}
	// Company B sees neither A's company item nor A's asset key.
	gotB, err := d.SearchKnowledgeCandidates(taskB.ID, 50)
	if err != nil {
		t.Fatal(err)
	}
	if len(gotB) != 0 {
		t.Fatalf("company B leaked %d items: %+v", len(gotB), gotB)
	}
}
