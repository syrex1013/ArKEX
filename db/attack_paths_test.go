package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"testing"
)

// proposePath is the test wrapper around ProposePathTx.
func proposePath(t *testing.T, d *DB, in PathInput) *AttackPath {
	t.Helper()
	var p *AttackPath
	err := d.WithEvidenceTx(context.Background(), func(tx *sql.Tx) error {
		var e error
		p, e = ProposePathTx(context.Background(), tx, in)
		return e
	})
	if err != nil {
		t.Fatal(err)
	}
	return p
}

// rescorePath runs RecomputePathScoreTx inside a caller-owned tx.
func rescorePath(t *testing.T, d *DB, pathID int64) {
	t.Helper()
	err := d.WithEvidenceTx(context.Background(), func(tx *sql.Tx) error {
		return RecomputePathScoreTx(context.Background(), tx, pathID)
	})
	if err != nil {
		t.Fatal(err)
	}
}

func pathRow(t *testing.T, d *DB, pathID int64) (float64, string) {
	t.Helper()
	var score float64
	var state string
	if err := d.QueryRow(`SELECT score, state FROM attack_paths WHERE id=$1`, pathID).Scan(&score, &state); err != nil {
		t.Fatal(err)
	}
	return score, state
}

func stepStatus(t *testing.T, d *DB, pathID int64, position int) string {
	t.Helper()
	var status string
	if err := d.QueryRow(`SELECT status FROM attack_path_steps WHERE path_id=$1 AND position=$2`, pathID, position).Scan(&status); err != nil {
		t.Fatal(err)
	}
	return status
}

// pathTestTask builds one task, one asset, one finding and one observed fact —
// the minimum ref set the score tests need.
func pathTestTask(t *testing.T, d *DB, tag string) (taskID int64, assetID int64, findingID int64, factNodeID int64) {
	t.Helper()
	tk, err := d.CreateTask("paths-"+tag, "goal", nil, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.DeleteTask(tk.ID) })
	assetID, err = d.Assets().UpsertRootDomain(UpsertRootDomainReq{Domain: tag + ".paths.test", TaskID: tk.ID})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := d.Exec(`INSERT INTO task_asset_links(task_id, asset_id) VALUES ($1,$2) ON CONFLICT DO NOTHING`, tk.ID, assetID); err != nil {
		t.Fatal(err)
	}
	es := d.Exploration(tk.ExplorationID)
	rec := recordFinding(t, d, es, RecordFindingInput{
		TaskID: tk.ID, VulnClass: "SQLi", Name: "sqli", Severity: SeverityHigh,
		Summary: "s", Evidence: "e", Worker: "work#1", AssetIDs: []int64{assetID},
	})
	// force a known confidence so the score table is exact
	if _, err := d.Exec(`UPDATE findings SET confidence=$2 WHERE id=$1`, rec.FindingID, 50); err != nil {
		t.Fatal(err)
	}
	observed := map[string]any{"summary": "admin panel reachable", "confidence": "observed"}
	raw, _ := json.Marshal(observed)
	var factID int64
	if err := d.QueryRow(`INSERT INTO exploration_nodes(exploration_id, kind, payload, priority, state, origin)
VALUES ($1,'fact',$2,5,'confirmed','work#1') RETURNING id`, tk.ExplorationID, string(raw)).Scan(&factID); err != nil {
		t.Fatal(err)
	}
	return tk.ID, assetID, rec.FindingID, factID
}

// Exact score table: Σ severityWeight × confidence/100 × status multiplier.
func TestPathScore(t *testing.T) {
	d, err := Open(testDSN(t))
	if err != nil {
		SkipPGUnavailable(t, err)
	}
	defer d.Close()

	taskID, assetID, findingID, factID := pathTestTask(t, d, "score")
	p := proposePath(t, d, PathInput{
		TaskID: taskID, Title: "chain", Rationale: "r", CreatedBy: "chain",
		Steps: []PathStepInput{
			{Kind: "finding", RefID: findingID, Edge: "enables", Precondition: "sqli", Postcondition: "db read"},
			{Kind: "asset", RefID: assetID, Edge: "requires", Precondition: "access", Postcondition: "panel"},
			{Kind: "fact", RefID: factID, Edge: "escalates", Precondition: "panel", Postcondition: "admin"},
		},
	})
	// finding: 6 (high) × 0.5 (conf 50) × 0.6 (proposed) = 1.8
	// asset:   1 × 0.6 = 0.6; fact: 2 × 0.6 = 1.2 → total 3.6
	if score, state := pathRow(t, d, p.ID); score != 3.6 || state != PathStateProposed {
		t.Fatalf("proposed score %v state %s, want 3.6/proposed", score, state)
	}

	// verify the finding step → its term becomes ×1.2: 6×0.5×1.2 + 0.6 + 1.2 = 5.4
	err = d.WithEvidenceTx(context.Background(), func(tx *sql.Tx) error {
		return SetStepStatusTx(context.Background(), tx, p.ID, 1, StepStatusVerified, "poe 4", "chain")
	})
	if err != nil {
		t.Fatal(err)
	}
	if score, state := pathRow(t, d, p.ID); score != 5.4 || state != PathStateValidating {
		t.Fatalf("validating score %v state %s, want 5.4/validating", score, state)
	}

	// verify the rest → validated, same multiplier everywhere: 3.6+3.6 = 7.2
	for _, pos := range []int{2, 3} {
		if err := d.WithEvidenceTx(context.Background(), func(tx *sql.Tx) error {
			return SetStepStatusTx(context.Background(), tx, p.ID, pos, StepStatusVerified, "observed", "chain")
		}); err != nil {
			t.Fatal(err)
		}
	}
	if score, state := pathRow(t, d, p.ID); score != 7.2 || state != PathStateValidated {
		t.Fatalf("validated score %v state %s, want 7.2/validated", score, state)
	}
}

// One refuted step refutes the whole path and zeroes the score.
func TestPathScoreRefutedStep(t *testing.T) {
	d, err := Open(testDSN(t))
	if err != nil {
		SkipPGUnavailable(t, err)
	}
	defer d.Close()

	taskID, _, findingID, factID := pathTestTask(t, d, "refute")
	p := proposePath(t, d, PathInput{
		TaskID: taskID, Title: "chain", CreatedBy: "chain",
		Steps: []PathStepInput{
			{Kind: "finding", RefID: findingID, Edge: "enables", Precondition: "a", Postcondition: "b"},
			{Kind: "fact", RefID: factID, Edge: "escalates", Precondition: "b", Postcondition: "c"},
		},
	})
	err = d.WithEvidenceTx(context.Background(), func(tx *sql.Tx) error {
		return SetStepStatusTx(context.Background(), tx, p.ID, 2, StepStatusRefuted, "not reachable", "chain")
	})
	if err != nil {
		t.Fatal(err)
	}
	if score, state := pathRow(t, d, p.ID); score != 0 || state != PathStateRefuted {
		t.Fatalf("refuted score %v state %s, want 0/refuted", score, state)
	}
	if st := stepStatus(t, d, p.ID, 2); st != StepStatusRefuted {
		t.Fatalf("step status %s, want refuted", st)
	}
}

// A step whose referenced row disappeared contributes no weight and renders as
// missing, but the rest of the chain still scores.
func TestPathScoreMissingRef(t *testing.T) {
	d, err := Open(testDSN(t))
	if err != nil {
		SkipPGUnavailable(t, err)
	}
	defer d.Close()

	taskID, _, findingID, factID := pathTestTask(t, d, "missing")
	p := proposePath(t, d, PathInput{
		TaskID: taskID, Title: "chain", CreatedBy: "chain",
		Steps: []PathStepInput{
			{Kind: "finding", RefID: findingID, Edge: "enables", Precondition: "a", Postcondition: "b"},
			{Kind: "fact", RefID: factID, Edge: "escalates", Precondition: "b", Postcondition: "c"},
		},
	})
	if score, _ := pathRow(t, d, p.ID); score != 1.8+1.2 {
		t.Fatalf("baseline score %v, want 3.0", score)
	}
	if _, err := d.Exec(`DELETE FROM findings WHERE id=$1`, findingID); err != nil {
		t.Fatal(err)
	}
	rescorePath(t, d, p.ID)
	if score, _ := pathRow(t, d, p.ID); score != 1.2 {
		t.Fatalf("missing-ref score %v, want 1.2 (finding term dropped)", score)
	}
	_, steps, err := d.GetAttackPath(p.ID)
	if err != nil {
		t.Fatal(err)
	}
	if steps[0].RefExists {
		t.Fatal("deleted finding ref must render as missing")
	}
	if steps[1].RefExists != true {
		t.Fatal("live fact ref must render as existing")
	}
}
