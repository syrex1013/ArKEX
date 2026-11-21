package db

import (
	"testing"
)

// TestEngagementIDRepairMigration: an install whose tasks.engagement_id was
// tightened to NOT NULL with a dead default rejects every task creation
// ("null value in column engagement_id"), though 0013/0015 define the column as
// nullable and the API creates tasks with no engagement. Migration 0016 must
// restore the repository contract on such a database and stay a no-op on a
// schema that already matches.
func TestEngagementIDRepairMigration(t *testing.T) {
	d, err := Open(testDSN(t))
	if err != nil {
		SkipPGUnavailable(t, err)
	}
	defer d.Close()

	// Simulate the drifted install.
	for _, q := range []string{
		`CREATE OR REPLACE FUNCTION legacy_engagement_id() RETURNS bigint LANGUAGE sql STABLE AS $$ SELECT NULL::bigint $$`,
		`ALTER TABLE tasks ALTER COLUMN engagement_id SET DEFAULT legacy_engagement_id()`,
		`ALTER TABLE tasks ALTER COLUMN engagement_id SET NOT NULL`,
	} {
		if _, err := d.Exec(q); err != nil {
			t.Fatalf("simulate drift: %v", err)
		}
	}
	t.Cleanup(func() {
		_, _ = d.Exec(`ALTER TABLE tasks ALTER COLUMN engagement_id DROP DEFAULT`)
		_, _ = d.Exec(`ALTER TABLE tasks ALTER COLUMN engagement_id DROP NOT NULL`)
		_, _ = d.Exec(`DROP FUNCTION IF EXISTS legacy_engagement_id()`)
	})

	// A task with no engagement must be refused while the drift is present.
	if _, err := d.CreateTask("drift probe", "goal", nil, 0, 0); err == nil {
		t.Fatal("task creation succeeded on the drifted schema; the drift should refuse it")
	}

	// The repair migration is idempotent; run it twice to prove it.
	for i := range 2 {
		if err := d.execEmbeddedMigration("0016_engagement_id_nullable.sql"); err != nil {
			t.Fatalf("repair run %d: %v", i+1, err)
		}
	}

	task, err := d.CreateTask("drift probe", "goal", nil, 0, 0)
	if err != nil {
		t.Fatalf("task creation after the repair: %v", err)
	}
	defer d.DeleteTask(task.ID)
	if task.EngagementID != nil {
		t.Fatalf("unengaged task carries engagement_id %v", *task.EngagementID)
	}
}
