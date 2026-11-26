package server

import (
	"context"
	"encoding/json"
	"strconv"
	"testing"

	"github.com/Autumn-27/artex/agent"
	"github.com/Autumn-27/artex/db"
)

// agentToolRecorder adapts *db.DB to the toolset's finding recorder.
type agentToolRecorder struct{ db *db.DB }

func (r *agentToolRecorder) Record(ctx context.Context, in db.RecordFindingInput, refs []db.TrafficRef) (*db.RecordedFinding, error) {
	return r.db.Exploration(in.ExplorationID).RecordFinding(ctx, in)
}

// chainFixture builds a task with one finding and one linked asset and returns a
// chain toolset bound to it plus the propose_path tool.
func chainFixture(t *testing.T) (*db.DB, *agent.ToolSet, db.RecordedFinding, int64) {
	t.Helper()
	skipWithoutDSN(t)
	m, err := NewManager(t.TempDir(), "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { m.Close() })
	task, err := m.CreateTask("chain spawn", "spawn follow-ups", nil, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	tid, _ := strconv.ParseInt(task.ID, 10, 64)
	t.Cleanup(func() { m.pg.DeleteTask(tid) })
	assetID, err := m.pg.Assets().UpsertRootDomain(db.UpsertRootDomainReq{Domain: "chain.spawn.test", TaskID: tid})
	if err != nil {
		t.Fatal(err)
	}
	rec, err := task.Store.RecordFinding(context.Background(), db.RecordFindingInput{
		TaskID: tid, VulnClass: "SSRF", Name: "ssrf", Severity: "high",
		Summary: "s", Evidence: "e", Worker: "work#1", AssetIDs: []int64{assetID},
	})
	if err != nil {
		t.Fatal(err)
	}
	ts := agent.NewToolSet(task.Store, "chain")
	ts.SetTaskID(tid)
	ts.SetFindingRecorder(&agentToolRecorder{db: m.pg})
	return m.pg, ts, *rec, assetID
}

func proposeChain(t *testing.T, ctx context.Context, ts *agent.ToolSet, assetID int64, findingID int64) (ok bool, text string) {
	t.Helper()
	tool := workflowTool(t, ts.ChainTools(), "propose_path")
	in, _ := json.Marshal(map[string]any{
		"title": "ssrf to internal", "rationale": "ssrf reaches the metadata service",
		"steps": []any{
			map[string]any{"kind": "finding", "ref_id": findingID, "edge": "enables", "precondition": "ssrf", "postcondition": "metadata read"},
			map[string]any{"kind": "asset", "ref_id": assetID, "edge": "requires", "precondition": "metadata", "postcondition": "creds"},
		},
	})
	res, err := tool.Call(ctx, in, nil)
	if err != nil {
		t.Fatal(err)
	}
	return !res.IsError, res.Flatten()
}

// A proposal spawns exactly one high-priority intent for its first step, anchored
// and chained to the source finding.
func TestChainSpawn(t *testing.T) {
	d, ts, rec, assetID := chainFixture(t)
	ctx := context.Background()
	ok, text := proposeChain(t, ctx, ts, assetID, rec.FindingID)
	if !ok {
		t.Fatalf("propose_path failed: %s", text)
	}
	rows, err := d.Query(`SELECT id, priority, origin, state, payload->>'role', payload->>'path_id'
FROM exploration_nodes WHERE kind='intent' AND payload ? 'path_id'`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var (
		intentID            int64
		priority            int
		origin, state, role string
		pathIDStr           string
		seen                int
	)
	for rows.Next() {
		seen++
		if err := rows.Scan(&intentID, &priority, &origin, &state, &role, &pathIDStr); err != nil {
			t.Fatal(err)
		}
	}
	if seen != 1 {
		t.Fatalf("spawned %d intents, want 1", seen)
	}
	if priority != 9 || origin != "chain" || state != "open" || role != "exploit" {
		t.Fatalf("intent %d: priority=%d origin=%s state=%s role=%s", intentID, priority, origin, state, role)
	}
	var anchors int
	if err := d.QueryRow(`SELECT count(*) FROM exploration_anchors WHERE node_id=$1 AND asset_id=$2`, intentID, assetID).Scan(&anchors); err != nil {
		t.Fatal(err)
	}
	if anchors != 1 {
		t.Fatalf("intent anchors = %d, want the finding's asset", anchors)
	}
	var derived int
	if err := d.QueryRow(`SELECT count(*) FROM exploration_edges WHERE dst_id=$1 AND rel=$2`, intentID, db.RelDerivedFrom).Scan(&derived); err != nil {
		t.Fatal(err)
	}
	if derived != 1 {
		t.Fatalf("derived_from edges = %d, want 1", derived)
	}
}

// A second proposal spawns its own follow-up instead of silently reusing the
// first, so every proposed path starts with live work.
func TestChainSpawnSecondProposal(t *testing.T) {
	d, ts, rec, assetID := chainFixture(t)
	ctx := context.Background()
	for range 2 {
		if ok, text := proposeChain(t, ctx, ts, assetID, rec.FindingID); !ok {
			t.Fatalf("propose_path failed: %s", text)
		}
	}
	var spawned int
	if err := d.QueryRow(`SELECT count(*) FROM exploration_nodes WHERE kind='intent' AND payload ? 'path_id'`).Scan(&spawned); err != nil {
		t.Fatal(err)
	}
	if spawned != 2 {
		t.Fatalf("spawned %d intents across two proposals, want 2", spawned)
	}
}
