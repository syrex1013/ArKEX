package db

import (
	"context"
	"database/sql"
	"testing"
)

// recordFinding is the test wrapper around RecordFindingTx (no traffic).
func recordFinding(t *testing.T, d *DB, es *ExplorationStore, in RecordFindingInput) *RecordedFinding {
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

// A second report of the same defect on the same asset merges into the first row
// instead of creating one.
func TestFindingDedupe(t *testing.T) {
	d, err := Open(testDSN(t))
	if err != nil {
		SkipPGUnavailable(t, err)
	}
	defer d.Close()

	tk, err := d.CreateTask("dedupe", "goal", nil, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer d.DeleteTask(tk.ID)
	assetID, err := d.Assets().UpsertRootDomain(UpsertRootDomainReq{Domain: "dedupe.test", TaskID: tk.ID})
	if err != nil {
		t.Fatal(err)
	}
	es := d.Exploration(tk.ExplorationID)
	in := RecordFindingInput{
		TaskID: tk.ID, VulnClass: "XSS", Name: "reflected xss", Severity: SeverityHigh,
		Summary: "s", Evidence: "e", Worker: "work#1", AssetIDs: []int64{assetID},
	}
	first := recordFinding(t, d, es, in)
	if first.Merged {
		t.Fatal("first report must not be a merge")
	}
	var version int64
	if err := d.QueryRow(`SELECT evidence_version FROM findings WHERE id=$1`, first.FindingID).Scan(&version); err != nil {
		t.Fatal(err)
	}

	in.Worker = "work#2"
	in.Counterevidence = "payload B blocked"
	second := recordFinding(t, d, es, in)
	if !second.Merged {
		t.Fatal("second report of the same defect must merge")
	}
	if second.FindingID != first.FindingID {
		t.Fatalf("merged into %d, want %d", second.FindingID, first.FindingID)
	}
	var rows int
	var after int64
	var counter string
	if err := d.QueryRow(`SELECT count(*), max(evidence_version), max(counterevidence) FROM findings WHERE task_id=$1`, tk.ID).
		Scan(&rows, &after, &counter); err != nil {
		t.Fatal(err)
	}
	if rows != 1 {
		t.Fatalf("want 1 finding row, got %d", rows)
	}
	if after <= version {
		t.Fatalf("evidence_version must increase on merge: %d -> %d", version, after)
	}
	if counter != "payload B blocked" {
		t.Fatalf("counterevidence not merged: %q", counter)
	}

	// A different asset is a different defect.
	otherID, err := d.Assets().UpsertRootDomain(UpsertRootDomainReq{Domain: "other.dedupe.test", TaskID: tk.ID})
	if err != nil {
		t.Fatal(err)
	}
	in.AssetIDs = []int64{otherID}
	in.Worker = "work#3"
	if third := recordFinding(t, d, es, in); third.Merged {
		t.Fatal("another asset must not merge")
	}
}
