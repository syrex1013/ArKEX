package db

import (
	"context"
	"database/sql"
	"fmt"
	"testing"
	"time"
)

// recordKnowledgeFinding is this file's wrapper around RecordFindingTx (no
// traffic evidence); named apart from the dedupe test's helper on purpose.
func recordKnowledgeFinding(t *testing.T, d *DB, es *ExplorationStore, in RecordFindingInput) *RecordedFinding {
	t.Helper()
	in.ExplorationID = es.ID()
	var out *RecordedFinding
	err := d.WithEvidenceTx(context.Background(), func(tx *sql.Tx) error {
		var e error
		out, e = RecordFindingTx(context.Background(), tx, in, nil)
		return e
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// knowledgeRowsByTask loads a task's extracted knowledge rows for assertions.
func knowledgeRowsByTask(t *testing.T, d *DB, taskID int64) map[string]KnowledgeItem {
	t.Helper()
	rows, err := d.Query(`SELECT id, kind, title, body, scope_company_id, scope_engagement_id,
scope_asset_key, scope_vulnclass, confidence, support, source_task_id, status
FROM knowledge_items WHERE source_task_id=$1`, taskID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	out := map[string]KnowledgeItem{}
	for rows.Next() {
		var k KnowledgeItem
		var comp, eng, src sql.NullInt64
		if err := rows.Scan(&k.ID, &k.Kind, &k.Title, &k.Body, &comp, &eng,
			&k.ScopeAssetKey, &k.ScopeVulnclass, &k.Confidence, &k.Support, &src, &k.Status); err != nil {
			t.Fatal(err)
		}
		if comp.Valid {
			k.ScopeCompanyID = &comp.Int64
		}
		if eng.Valid {
			k.ScopeEngagement = &eng.Int64
		}
		if src.Valid {
			k.SourceTaskID = &src.Int64
		}
		out[k.Kind+"/"+k.Title] = k
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

// knowledgeItemCount counts the knowledge rows extracted from one task, by id
// rather than by map key so a title collision cannot hide a duplicate row.
func knowledgeItemCount(t *testing.T, d *DB, taskID int64) int {
	t.Helper()
	var n int
	if err := d.QueryRow(`SELECT count(*) FROM knowledge_items WHERE source_task_id=$1`, taskID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// knowledgeLastSeen reads one item's last_seen, which a merge moves forward.
func knowledgeLastSeen(t *testing.T, d *DB, id int64) time.Time {
	t.Helper()
	var seen time.Time
	if err := d.QueryRow(`SELECT last_seen FROM knowledge_items WHERE id=$1`, id).Scan(&seen); err != nil {
		t.Fatal(err)
	}
	return seen
}

// knowledgeSourceCount counts the distinct evidence rows behind one item.
func knowledgeSourceCount(t *testing.T, d *DB, id int64) int {
	t.Helper()
	var n int
	if err := d.QueryRow(`SELECT count(*) FROM knowledge_item_sources WHERE item_id=$1`, id).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// linkKnowledgeAsset attaches an asset to a task the way the agent layer does.
func linkKnowledgeAsset(t *testing.T, d *DB, taskID, assetID int64) {
	t.Helper()
	if _, err := d.Exec(`INSERT INTO task_asset_links(task_id, asset_id) VALUES ($1,$2) ON CONFLICT DO NOTHING`,
		taskID, assetID); err != nil {
		t.Fatal(err)
	}
}

// ExtractKnowledgeForTask turns a ruled-out ledger row and a counterevidence
// finding into failed hypotheses, a follow-up row into an attempt and a verified
// PoE>=3 finding into a successful approach — all scoped to the task's single
// company. A second run merges into the same rows: support counts distinct
// sources, so it stays at 1 while last_seen moves forward.
func TestExtractKnowledgeForTask(t *testing.T) {
	d, err := Open(testDSN(t))
	if err != nil {
		SkipPGUnavailable(t, err)
	}
	t.Cleanup(func() { d.Close() })

	stamp := time.Now().UnixNano()
	companyID, _, err := d.Companies().UpsertCompany(fmt.Sprintf("KnowledgeExtract %d", stamp), "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cleanupCompany(d, companyID) })
	task, err := d.CreateTaskWithOptions(fmt.Sprintf("knowledge-extract-%d", stamp), "goal", TaskCreateOptions{
		CompanyIDs: []int64{companyID},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cleanupErr(t, "knowledge extract task", d.DeleteTask(task.ID)) })
	as := d.Assets()
	es := d.Exploration(task.ExplorationID)

	if err := as.RecordCoverage(task.ID, CoverageEntry{Surface: "/admin", RiskArea: "sqli",
		Outcome: OutcomeRuledOut, Reason: "waf blocks quotes", Evidence: "payload ' or 1=1 rejected"}); err != nil {
		t.Fatal(err)
	}
	if err := as.RecordCoverage(task.ID, CoverageEntry{Surface: "/api", RiskArea: "idor",
		Outcome: OutcomeNeedsFollowUp, Reason: "needs two accounts"}); err != nil {
		t.Fatal(err)
	}
	ce := recordKnowledgeFinding(t, d, es, RecordFindingInput{TaskID: task.ID, VulnClass: "XSS",
		Name: "reflected xss", Severity: SeverityHigh, Summary: "s", Evidence: "e",
		Worker: "work#1", Counterevidence: "payload B blocked"})
	if ce.Merged {
		t.Fatal("counterevidence fixture must be its own finding")
	}
	verified := recordKnowledgeFinding(t, d, es, RecordFindingInput{TaskID: task.ID, VulnClass: "RCE",
		Name: "deser rce", Severity: SeverityCritical, Summary: "java deser on /api/export",
		Evidence: "e", Worker: "work#2"})
	if err := d.SetFindingVerification(verified.FindingID, VerificationVerified, 4, ""); err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()
	n, err := d.ExtractKnowledgeForTask(ctx, task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if n != 4 {
		t.Fatalf("extracted=%d, want 4", n)
	}
	rows := knowledgeRowsByTask(t, d, task.ID)
	key := func(kind, title string) string { return kind + "/" + title }

	rule, ok := rows[key(KnowledgeFailedHypothesis, "/admin / sqli")]
	if !ok {
		t.Fatalf("ruled_out ledger row missing: %+v", rows)
	}
	if rule.Confidence != 70 || rule.Support != 1 {
		t.Fatalf("ruled_out conf=%d support=%d, want 70/1", rule.Confidence, rule.Support)
	}
	if rule.Body != "waf blocks quotes\npayload ' or 1=1 rejected" {
		t.Fatalf("ruled_out body=%q", rule.Body)
	}
	if rule.ScopeCompanyID == nil || *rule.ScopeCompanyID != companyID {
		t.Fatalf("ruled_out scope=%v, want company %d", rule.ScopeCompanyID, companyID)
	}
	if rule.SourceTaskID == nil || *rule.SourceTaskID != task.ID {
		t.Fatal("ruled_out source_task_id not set")
	}
	if rule.ScopeAssetKey != "" || rule.ScopeEngagement != nil {
		t.Fatalf("ruled_out asset/engagement scope must be empty: %+v", rule)
	}
	// Provenance: the ledger row's task is the evidence behind the item.
	if got := knowledgeSourceCount(t, d, rule.ID); got != 1 {
		t.Fatalf("ruled_out evidence rows=%d, want 1", got)
	}

	if a := rows[key(KnowledgeAttempt, "/api / idor")]; a.ID == 0 || a.Confidence != 30 {
		t.Fatalf("attempt row wrong: %+v", a)
	}
	if h := rows[key(KnowledgeFailedHypothesis, "reflected xss")]; h.ID == 0 || h.Confidence != 50 || h.Body != "payload B blocked" {
		t.Fatalf("counterevidence hypothesis wrong: %+v", h)
	}
	if s := rows[key(KnowledgeSuccessfulApproach, "deser rce")]; s.ID == 0 || s.Body != "java deser on /api/export" {
		t.Fatalf("successful approach wrong: %+v", s)
	}

	firstSeen := knowledgeLastSeen(t, d, rule.ID)

	// Idempotent: a second run merges into the same rows. Support counts distinct
	// sources, so re-extracting the identical task and content leaves it at 1 —
	// only last_seen moves.
	if n2, err := d.ExtractKnowledgeForTask(ctx, task.ID); err != nil || n2 != 4 {
		t.Fatalf("second run n=%d err=%v, want 4/nil", n2, err)
	}
	rows2 := knowledgeRowsByTask(t, d, task.ID)
	if len(rows2) != len(rows) {
		t.Fatalf("second run added rows: %d -> %d", len(rows), len(rows2))
	}
	if got := knowledgeItemCount(t, d, task.ID); got != 4 {
		t.Fatalf("stored rows after second run=%d, want 4", got)
	}
	for k, row := range rows2 {
		if row.Support != 1 {
			t.Fatalf("%s support=%d after second run, want 1 (support counts sources, not runs)", k, row.Support)
		}
		if row.ID != rows[k].ID {
			t.Fatalf("%s got a second row: id %d -> %d", k, rows[k].ID, row.ID)
		}
	}
	if again := knowledgeLastSeen(t, d, rule.ID); !again.After(firstSeen) {
		t.Fatalf("last_seen not refreshed by the merge: %v -> %v", firstSeen, again)
	}
}

// TestExtractKnowledgeSkipsUnscopedTask: a task with no company, no engagement
// and no linked asset has no scope retrieval could ever match, so nothing is
// written and no error is returned — the logged-skip case.
func TestExtractKnowledgeSkipsUnscopedTask(t *testing.T) {
	d, err := Open(testDSN(t))
	if err != nil {
		SkipPGUnavailable(t, err)
	}
	t.Cleanup(func() { d.Close() })

	stamp := time.Now().UnixNano()
	task, err := d.CreateTaskWithOptions(fmt.Sprintf("knowledge-unscoped-%d", stamp), "goal", TaskCreateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cleanupErr(t, "unscoped task", d.DeleteTask(task.ID)) })

	// A ledger row that would otherwise produce an item: the skip must come from
	// the missing scope, not from having nothing to extract.
	if err := d.Assets().RecordCoverage(task.ID, CoverageEntry{Surface: "/login", RiskArea: "auth",
		Outcome: OutcomeRuledOut, Reason: "no local accounts"}); err != nil {
		t.Fatal(err)
	}
	companies, engagement, keys, err := d.TaskKnowledgeScope(task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(companies) != 0 || engagement != 0 || len(keys) != 0 {
		t.Fatalf("fixture must be unscoped, got companies=%v engagement=%d keys=%v", companies, engagement, keys)
	}

	n, err := d.ExtractKnowledgeForTask(context.Background(), task.ID)
	if err != nil {
		t.Fatalf("unscoped task must be a silent skip, got err=%v", err)
	}
	if n != 0 {
		t.Fatalf("unscoped task wrote %d items, want 0", n)
	}
	if got := knowledgeItemCount(t, d, task.ID); got != 0 {
		t.Fatalf("unscoped task stored %d items, want 0", got)
	}
}

// TestExtractKnowledgeUsesFindingAssets: a task with no company and no
// engagement but a finding recorded against an asset takes that asset's key, so
// its knowledge is retrievable by every other task linked to the same asset.
func TestExtractKnowledgeUsesFindingAssets(t *testing.T) {
	d, err := Open(testDSN(t))
	if err != nil {
		SkipPGUnavailable(t, err)
	}
	t.Cleanup(func() { d.Close() })

	stamp := time.Now().UnixNano()
	task, err := d.CreateTaskWithOptions(fmt.Sprintf("knowledge-finding-assets-%d", stamp), "goal", TaskCreateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cleanupErr(t, "finding-asset task", d.DeleteTask(task.ID)) })

	// The asset belongs to another task, so this task has no linked key of its
	// own: the finding's asset is the only thing that can scope it.
	domain := fmt.Sprintf("finding-asset-%d.example", stamp)
	owner, err := d.CreateTaskWithOptions(fmt.Sprintf("knowledge-asset-owner-%d", stamp), "goal", TaskCreateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cleanupErr(t, "asset owner task", d.DeleteTask(owner.ID)) })
	assetID, err := d.Assets().UpsertRootDomain(UpsertRootDomainReq{Domain: domain, TaskID: owner.ID})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cleanupErr(t, "asset", execCleanup(d, `DELETE FROM assets WHERE id=$1`, assetID)) })

	es := d.Exploration(task.ExplorationID)
	title := fmt.Sprintf("metadata reachable %d", stamp)
	t.Cleanup(func() {
		cleanupErr(t, "knowledge items", execCleanup(d, `DELETE FROM knowledge_items WHERE title=$1`, title))
	})
	ce := recordKnowledgeFinding(t, d, es, RecordFindingInput{TaskID: task.ID, VulnClass: "ssrf",
		Name: title, Severity: SeverityHigh, Summary: "s", Evidence: "e",
		Worker: "work#1", Counterevidence: "169.254.169.254 filtered", AssetIDs: []int64{assetID}})
	if err := d.SetFindingVerification(ce.FindingID, VerificationVerified, 4, ""); err != nil {
		t.Fatal(err)
	}

	n, err := d.ExtractKnowledgeForTask(context.Background(), task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if n == 0 {
		t.Fatal("finding-only scope wrote nothing")
	}
	rows := knowledgeRowsByTask(t, d, task.ID)
	if len(rows) == 0 {
		t.Fatal("no knowledge rows for the finding-only task")
	}
	for k, row := range rows {
		if row.ScopeAssetKey != domain {
			t.Fatalf("%s asset scope=%q, want %q", k, row.ScopeAssetKey, domain)
		}
		if row.ScopeCompanyID != nil || row.ScopeEngagement != nil {
			t.Fatalf("%s must stay unscoped by company/engagement: %+v", k, row)
		}
	}

	// A second task linked to the same asset retrieves the extracted items.
	reader, err := d.CreateTaskWithOptions(fmt.Sprintf("knowledge-asset-reader-%d", stamp), "goal", TaskCreateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cleanupErr(t, "asset reader task", d.DeleteTask(reader.ID)) })
	linkKnowledgeAsset(t, d, reader.ID, assetID)

	candidates, err := d.SearchKnowledgeCandidates(reader.ID, 200)
	if err != nil {
		t.Fatal(err)
	}
	found := map[int64]bool{}
	for _, k := range candidates {
		found[k.ID] = true
	}
	for k, row := range rows {
		if !found[row.ID] {
			t.Fatalf("%s (id %d) not retrievable through the shared asset key", k, row.ID)
		}
	}
}

// TestExtractKnowledgeSupportCountsFindings: two findings landing on the same
// kind+title in the same scope are two pieces of evidence, so the merged item's
// support is 2 and it keeps both finding source ids.
func TestExtractKnowledgeSupportCountsFindings(t *testing.T) {
	d, err := Open(testDSN(t))
	if err != nil {
		SkipPGUnavailable(t, err)
	}
	t.Cleanup(func() { d.Close() })

	stamp := time.Now().UnixNano()
	companyID, _, err := d.Companies().UpsertCompany(fmt.Sprintf("KnowledgeFindings %d", stamp), "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cleanupCompany(d, companyID) })
	task, err := d.CreateTaskWithOptions(fmt.Sprintf("knowledge-finding-support-%d", stamp), "goal", TaskCreateOptions{
		CompanyIDs: []int64{companyID},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cleanupErr(t, "finding support task", d.DeleteTask(task.ID)) })

	// Two assets: the findings differ only by asset, so they stay separate rows
	// (dedupe is per task+vulnclass+asset) while feeding one knowledge item.
	var assetIDs []int64
	for _, name := range []string{"a", "b"} {
		id, err := d.Assets().UpsertRootDomain(UpsertRootDomainReq{
			Domain: fmt.Sprintf("finding-support-%s-%d.example", name, stamp), TaskID: task.ID})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { cleanupErr(t, "asset", execCleanup(d, `DELETE FROM assets WHERE id=$1`, id)) })
		assetIDs = append(assetIDs, id)
	}

	es := d.Exploration(task.ExplorationID)
	title := fmt.Sprintf("cache key ignores cookie %d", stamp)
	t.Cleanup(func() {
		cleanupErr(t, "knowledge items", execCleanup(d, `DELETE FROM knowledge_items WHERE title=$1`, title))
	})
	var findingIDs []int64
	for i, assetID := range assetIDs {
		rec := recordKnowledgeFinding(t, d, es, RecordFindingInput{TaskID: task.ID, VulnClass: "cache_poison",
			Name: title, Severity: SeverityHigh, Summary: "s", Evidence: "e",
			Worker: fmt.Sprintf("work#%d", i+1), Counterevidence: fmt.Sprintf("variant %d blocked", i+1),
			AssetIDs: []int64{assetID}})
		if rec.Merged {
			t.Fatalf("finding %d merged into another row; fixtures must be distinct defects", i)
		}
		findingIDs = append(findingIDs, rec.FindingID)
	}

	n, err := d.ExtractKnowledgeForTask(context.Background(), task.ID)
	if err != nil {
		t.Fatal(err)
	}
	rows := knowledgeRowsByTask(t, d, task.ID)
	if n != 2 || len(rows) != 1 {
		t.Fatalf("extracted=%d stored=%d, want 2 writes merging into 1 row: %+v", n, len(rows), rows)
	}
	item := rows[KnowledgeFailedHypothesis+"/"+title]
	if item.ID == 0 {
		t.Fatalf("merged item missing: %+v", rows)
	}
	if item.Support != 2 {
		t.Fatalf("support=%d, want 2 (one per finding source)", item.Support)
	}
	if got := knowledgeSourceCount(t, d, item.ID); got != 2 {
		t.Fatalf("evidence rows=%d, want 2", got)
	}

	// The two evidence rows are the two findings, not one finding twice.
	rowsSrc, err := d.Query(`SELECT source_kind, source_id FROM knowledge_item_sources WHERE item_id=$1 ORDER BY source_id`, item.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer rowsSrc.Close()
	var gotIDs []int64
	for rowsSrc.Next() {
		var kind string
		var id int64
		if err := rowsSrc.Scan(&kind, &id); err != nil {
			t.Fatal(err)
		}
		if kind != "finding" {
			t.Fatalf("source kind=%q, want finding", kind)
		}
		gotIDs = append(gotIDs, id)
	}
	if err := rowsSrc.Err(); err != nil {
		t.Fatal(err)
	}
	if len(gotIDs) != 2 || gotIDs[0] != findingIDs[0] || gotIDs[1] != findingIDs[1] {
		t.Fatalf("source ids=%v, want %v", gotIDs, findingIDs)
	}

	// Re-running extraction over the same findings changes nothing.
	if n2, err := d.ExtractKnowledgeForTask(context.Background(), task.ID); err != nil || n2 != 2 {
		t.Fatalf("second run n=%d err=%v, want 2/nil", n2, err)
	}
	if got := knowledgeItemCount(t, d, task.ID); got != 1 {
		t.Fatalf("rows after second run=%d, want 1", got)
	}
	if got := supportOf(t, d, item.ID); got != 2 {
		t.Fatalf("support=%d after second run, want 2", got)
	}
}
