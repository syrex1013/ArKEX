package db

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

type archiveJSONTestRows struct {
	values [][]byte
	next   int
}

func (r *archiveJSONTestRows) Next() bool {
	if r.next >= len(r.values) {
		return false
	}
	r.next++
	return true
}

func (r *archiveJSONTestRows) Scan(dest ...any) error {
	if len(dest) != 1 || r.next == 0 || r.next > len(r.values) {
		return fmt.Errorf("invalid archive test row scan")
	}
	target, ok := dest[0].(*[]byte)
	if !ok {
		return fmt.Errorf("archive test row destination is %T", dest[0])
	}
	*target = append((*target)[:0], r.values[r.next-1]...)
	return nil
}

func (r *archiveJSONTestRows) Err() error { return nil }

func TestQueryArchiveRowsStreamsJSON(t *testing.T) {
	payload := strings.Repeat("large request/response payload ", 64*1024)
	values := make([][]byte, 2)
	var err error
	values[0], err = json.Marshal(map[string]any{"id": int64(1), "body": payload})
	if err != nil {
		t.Fatal(err)
	}
	values[1], err = json.Marshal(map[string]any{"id": int64(2), "body": "quoted: \"value\"\nline"})
	if err != nil {
		t.Fatal(err)
	}
	raw, count, err := encodeArchiveRows(&archiveJSONTestRows{values: values})
	if err != nil {
		t.Fatal(err)
	}
	if count != 2 {
		t.Fatalf("row count=%d, want 2", count)
	}
	var rows []struct {
		ID   int64  `json:"id"`
		Body string `json:"body"`
	}
	if err := json.Unmarshal(raw, &rows); err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 {
		t.Fatalf("decoded row count=%d, want 2", len(rows))
	}
	if rows[0].ID != 1 || rows[0].Body != payload || rows[1].ID != 2 || rows[1].Body != "quoted: \"value\"\nline" {
		t.Fatal("streamed rows were reordered or truncated")
	}
}

func TestWriteArchiveRowsProducesJSONSequence(t *testing.T) {
	values := [][]byte{
		json.RawMessage(`{"id":1,"body":"first"}`),
		json.RawMessage(`{"id":2,"body":"second\\nline"}`),
	}
	var output bytes.Buffer
	count, err := writeArchiveRows(&archiveJSONTestRows{values: values}, &output)
	if err != nil {
		t.Fatal(err)
	}
	if count != 2 {
		t.Fatalf("streamed row count=%d, want 2", count)
	}
	decoder := json.NewDecoder(&output)
	for wantID := int64(1); wantID <= 2; wantID++ {
		var row struct {
			ID int64 `json:"id"`
		}
		if err := decoder.Decode(&row); err != nil {
			t.Fatal(err)
		}
		if row.ID != wantID {
			t.Fatalf("streamed row id=%d, want %d", row.ID, wantID)
		}
	}
}

func TestTaskArchiveFormatCompatibility(t *testing.T) {
	for _, version := range []int{TaskArchiveLegacyFormatVersion, TaskArchiveFormatVersion} {
		if !IsTaskArchiveFormatSupported(version) {
			t.Fatalf("archive format %d should be supported", version)
		}
	}
	for _, version := range []int{0, TaskArchiveFormatVersion + 1} {
		if IsTaskArchiveFormatSupported(version) {
			t.Fatalf("archive format %d should be rejected", version)
		}
	}
	invalidSnapshots := []*TaskArchiveSnapshot{
		{FormatVersion: TaskArchiveLegacyFormatVersion, StreamedTables: map[string]string{"llm_records": TaskArchiveLLMRecordsPath}},
		{FormatVersion: TaskArchiveFormatVersion, StreamedTables: map[string]string{"unknown": "database/unknown.ndjson"}},
	}
	for _, snapshot := range invalidSnapshots {
		if _, err := (&DB{}).RestoreTaskArchive(1, snapshot, 0); !errors.Is(err, ErrTaskArchiveFormatMismatch) {
			t.Fatalf("invalid streamed table metadata returned %v", err)
		}
	}
}

func TestTaskArchiveDatabaseRoundTrip(t *testing.T) {
	d, err := Open(testDSN(t))
	if err != nil {
		SkipPGUnavailable(t, err)
	}
	defer d.Close()
	if err := d.EnsureLLMRecordsTable(); err != nil {
		t.Fatal(err)
	}
	if err := d.EnsureLLMUsageTable(); err != nil {
		t.Fatal(err)
	}
	task, err := d.CreateTaskWithOptions("archive database roundtrip", "restore exact graph", TaskCreateOptions{Name: "cold task"})
	if err != nil {
		t.Fatal(err)
	}
	var companyID, llmProfileID, engagementID int64
	engagementKey := fmt.Sprintf("archive-engagement-%d", task.ID)
	if err := d.QueryRow(`INSERT INTO engagements(name,nkey) VALUES($1,$1) RETURNING id`, engagementKey).Scan(&engagementID); err != nil {
		t.Fatal(err)
	}
	defer d.Exec(`DELETE FROM engagements WHERE id=$1`, engagementID)
	if err := d.QueryRow(`INSERT INTO companies(name,nkey) VALUES($1,$2) RETURNING id`,
		fmt.Sprintf("archive-company-%d", task.ID), fmt.Sprintf("archive-company-%d", task.ID)).Scan(&companyID); err != nil {
		t.Fatal(err)
	}
	if err := d.QueryRow(`INSERT INTO llm_profiles(name,format,model) VALUES($1,'openai','archive-model') RETURNING id`,
		fmt.Sprintf("archive-chain-%d", task.ID)).Scan(&llmProfileID); err != nil {
		t.Fatal(err)
	}
	exhaustedAt := time.Now().UTC().Add(-time.Hour).Truncate(time.Microsecond)
	chainCreatedAt := exhaustedAt.Add(-time.Hour)
	if _, err := d.Exec(`UPDATE tasks SET company_id=$2,llm_profile_id=$3,active_llm_profile_id=$3,engagement_id=$4 WHERE id=$1`, task.ID, companyID, llmProfileID, engagementID); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Exec(`INSERT INTO task_llm_profiles(task_id,profile_id,position,status,last_error,exhausted_at,created_at,updated_at)
VALUES($1,$2,0,'quota_exhausted','balance exhausted',$3,$4,$3)`, task.ID, llmProfileID, exhaustedAt, chainCreatedAt); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_, _ = d.Exec(`DELETE FROM llm_usage WHERE task_id=$1 OR exploration_id=$2`, fmt.Sprint(task.ID), task.ExplorationID)
		_, _ = d.Exec(`DELETE FROM skill_usage WHERE task_id=$1 OR exploration_id=$2`, task.ID, task.ExplorationID)
		_, _ = d.Exec(`DELETE FROM tool_usage WHERE task_id=$1 OR exploration_id=$2`, task.ID, task.ExplorationID)
		_, _ = d.Exec(`DELETE FROM task_archives WHERE task_id=$1`, task.ID)
		_ = d.DeleteTask(task.ID)
		_, _ = d.Exec(`DELETE FROM llm_profiles WHERE id=$1`, llmProfileID)
		_, _ = d.Exec(`DELETE FROM companies WHERE id=$1`, companyID)
	}()
	if err := d.SetPaused(task.ID, true); err != nil {
		t.Fatal(err)
	}
	assetID, err := d.Assets().UpsertRootDomain(UpsertRootDomainReq{Domain: fmt.Sprintf("archive-%d.example", task.ID), TaskID: task.ID})
	if err != nil {
		t.Fatal(err)
	}
	store := d.Exploration(task.ExplorationID)
	nodeID, err := store.AddNode(KindFact, map[string]any{"summary": "archived fact", "asset_ids": []int64{assetID}}, 1, "confirmed", "worker", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Anchor(nodeID, assetID); err != nil {
		t.Fatal(err)
	}
	profileName := fmt.Sprintf("archive-profile-%d", task.ID)
	skillName := fmt.Sprintf("archive-skill-%d", task.ID)
	toolName := fmt.Sprintf("archive-tool-%d", task.ID)
	vulnclass := fmt.Sprintf("archive-vuln-%d", task.ID)
	if err := d.InsertLLMUsage(&LLMUsage{TaskID: fmt.Sprint(task.ID), ExplorationID: task.ExplorationID, Worker: "worker", Model: "test", ProfileName: profileName, InputTokens: 11, OutputTokens: 7}); err != nil {
		t.Fatal(err)
	}
	if err := d.InsertSkillUsage(&SkillUsage{Skill: skillName, AgentKey: "worker", TaskID: task.ID, ExplorationID: task.ExplorationID, Found: true}); err != nil {
		t.Fatal(err)
	}
	if err := d.InsertToolUsage(&ToolUsage{ToolKey: toolName, AgentKey: "worker", TaskID: task.ID, ExplorationID: task.ExplorationID}); err != nil {
		t.Fatal(err)
	}
	if err := d.InsertLLMRecord(&LLMRecord{
		TaskID: fmt.Sprint(task.ID), Model: "archive-model", SessionID: "archive-session",
		Status: "ok", RawRequest: strings.Repeat("request", 1024), RawResponse: strings.Repeat("response", 1024),
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := d.AddFinding(task.ID, nodeID, vulnclass, "archive finding", SeverityCritical, "summary", "evidence", "worker", []int64{assetID}); err != nil {
		t.Fatal(err)
	}
	archive, err := d.QueueTaskArchive(task.ID)
	if err != nil {
		t.Fatal(err)
	}
	claimed, err := d.ClaimTaskArchiveJob(t.Context())
	if err != nil || claimed == nil || claimed.ID != archive.ID || claimed.State != Archiving {
		t.Fatalf("claim = %+v, %v", claimed, err)
	}
	var llmRecords bytes.Buffer
	snapshot, err := d.SnapshotTaskArchiveWithLLMRecords(task.ID, &llmRecords)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.StreamedTables["llm_records"] != TaskArchiveLLMRecordsPath || snapshot.DataCounts["llm_records"] != 1 {
		t.Fatalf("unexpected streamed LLM metadata: paths=%v counts=%v", snapshot.StreamedTables, snapshot.DataCounts)
	}
	if rawRowCount(snapshot.Tables["llm_records"]) != 0 {
		t.Fatal("streamed LLM records were also retained in manifest memory")
	}
	if snapshot.DataCounts["assets"] != 1 || snapshot.DataCounts["exploration_nodes"] < 2 {
		t.Fatalf("unexpected snapshot counts: %#v", snapshot.DataCounts)
	}
	if err := d.CompleteTaskArchive(archive.ID, snapshot, "/tmp/test-task.tar.zst", "abc", 100, 50); err != nil {
		t.Fatal(err)
	}
	if live, err := d.GetTask(task.ID); err != nil || live != nil {
		t.Fatalf("archived task must be hidden, got %+v, %v", live, err)
	}
	matching, err := d.ListTaskArchives("", ArchiveReady, engagementID, 1, 1)
	if err != nil || matching.Total != 1 || len(matching.Items) != 1 || matching.Items[0].ID != archive.ID {
		t.Fatalf("matching engagement archive page = %+v, %v", matching, err)
	}
	missing, err := d.ListTaskArchives("", ArchiveReady, -1, 1, 1)
	if err != nil || missing.Total != 0 || len(missing.Items) != 0 {
		t.Fatalf("nonmatching engagement archive page = %+v, %v", missing, err)
	}
	var nodes, assets, usage int
	if err := d.QueryRow(`SELECT count(*) FROM exploration_nodes WHERE exploration_id=$1`, task.ExplorationID).Scan(&nodes); err != nil {
		t.Fatal(err)
	}
	if err := d.QueryRow(`SELECT count(*) FROM assets WHERE id=$1`, assetID).Scan(&assets); err != nil {
		t.Fatal(err)
	}
	if err := d.QueryRow(`SELECT count(*) FROM llm_usage WHERE task_id=$1`, fmt.Sprint(task.ID)).Scan(&usage); err != nil {
		t.Fatal(err)
	}
	if nodes != 0 || assets != 0 || usage != 0 {
		t.Fatalf("hot compaction left nodes=%d assets=%d usage=%d", nodes, assets, usage)
	}
	ready, err := d.GetTaskArchive(archive.ID)
	if err != nil || ready == nil || ready.State != ArchiveReady {
		t.Fatalf("ready archive = %+v, %v", ready, err)
	}
	var stats map[string]any
	if err := json.Unmarshal(ready.AggregateStats, &stats); err != nil {
		t.Fatal(err)
	}
	if _, ok := stats["tokens"]; !ok {
		t.Fatalf("archive token summary missing: %#v", stats)
	}
	assertArchiveGlobalStats(t, d, profileName, skillName, toolName, vulnclass)
	if _, err := d.Exec(`DELETE FROM companies WHERE id=$1`, companyID); err != nil {
		t.Fatal(err)
	}
	if _, err := d.QueueTaskArchiveRestore(archive.ID); err != nil {
		t.Fatal(err)
	}
	claimed, err = d.ClaimTaskArchiveJob(t.Context())
	if err != nil || claimed == nil || claimed.State != Restoring {
		t.Fatalf("restore claim = %+v, %v", claimed, err)
	}
	warnings, err := d.RestoreTaskArchiveWithLLMRecords(
		archive.ID, snapshot, int64((10 * time.Minute).Seconds()), bytes.NewReader(llmRecords.Bytes()),
	)
	if err != nil {
		t.Fatal(err)
	}
	warningFound := false
	for _, warning := range warnings {
		if strings.Contains(warning, fmt.Sprintf("task company %d has been deleted", companyID)) {
			warningFound = true
		}
	}
	if !warningFound {
		t.Fatalf("missing deleted company warning: %v", warnings)
	}
	live, err := d.GetTask(task.ID)
	if err != nil || live == nil {
		t.Fatalf("restored task = %+v, %v", live, err)
	}
	if live.Name != "cold task" || !live.Paused {
		t.Fatalf("restored task metadata mismatch: %+v", live)
	}
	if err := d.QueryRow(`SELECT count(*) FROM exploration_nodes WHERE exploration_id=$1`, task.ExplorationID).Scan(&nodes); err != nil {
		t.Fatal(err)
	}
	if err := d.QueryRow(`SELECT count(*) FROM assets WHERE id=$1 AND $2=ANY(task_ids)`, assetID, task.ID).Scan(&assets); err != nil {
		t.Fatal(err)
	}
	if err := d.QueryRow(`SELECT count(*) FROM llm_usage WHERE task_id=$1`, fmt.Sprint(task.ID)).Scan(&usage); err != nil {
		t.Fatal(err)
	}
	if nodes < 2 || assets != 1 || usage != 1 {
		t.Fatalf("restore incomplete nodes=%d assets=%d usage=%d", nodes, assets, usage)
	}
	var restoredRawRequest, restoredRawResponse string
	if err := d.QueryRow(`SELECT COALESCE(raw_request,''),COALESCE(raw_response,'') FROM llm_records WHERE task_id=$1`, fmt.Sprint(task.ID)).
		Scan(&restoredRawRequest, &restoredRawResponse); err != nil {
		t.Fatal(err)
	}
	if restoredRawRequest != strings.Repeat("request", 1024) || restoredRawResponse != strings.Repeat("response", 1024) {
		t.Fatal("restored streamed LLM record body was truncated")
	}
	var restoredCompanyID *int64
	var restoredStatus, restoredError string
	var restoredExhaustedAt, restoredCreatedAt time.Time
	if err := d.QueryRow(`SELECT company_id FROM tasks WHERE id=$1`, task.ID).Scan(&restoredCompanyID); err != nil {
		t.Fatal(err)
	}
	if restoredCompanyID != nil {
		t.Fatalf("deleted legacy company restored as %v", *restoredCompanyID)
	}
	if err := d.QueryRow(`SELECT status,COALESCE(last_error,''),exhausted_at,created_at FROM task_llm_profiles WHERE task_id=$1 AND profile_id=$2`,
		task.ID, llmProfileID).Scan(&restoredStatus, &restoredError, &restoredExhaustedAt, &restoredCreatedAt); err != nil {
		t.Fatal(err)
	}
	if restoredStatus != "quota_exhausted" || restoredError != "balance exhausted" ||
		!restoredExhaustedAt.Equal(exhaustedAt) || !restoredCreatedAt.Equal(chainCreatedAt) {
		t.Fatalf("LLM chain state/time mismatch: status=%s error=%q exhausted=%s created=%s",
			restoredStatus, restoredError, restoredExhaustedAt, restoredCreatedAt)
	}
	assertArchiveGlobalStats(t, d, profileName, skillName, toolName, vulnclass)
	if err := d.CompleteTaskArchiveRestore(archive.ID); err != nil {
		t.Fatal(err)
	}
	if item, err := d.GetTaskArchive(archive.ID); err != nil || item != nil {
		t.Fatalf("archive metadata must be consumed: %+v, %v", item, err)
	}
}

// Pre-0020 archives carry findings rows without the confidence/fingerprint
// columns migration 0020 made NOT NULL. Restore must apply defaults instead of
// failing with SQLSTATE 23502.
func TestTaskArchiveRestorePre0020FindingDefaults(t *testing.T) {
	d, err := Open(testDSN(t))
	if err != nil {
		SkipPGUnavailable(t, err)
	}
	defer d.Close()
	task, err := d.CreateTaskWithOptions("pre-0020 restore", "legacy fixture", TaskCreateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		_, _ = d.Exec(`DELETE FROM findings WHERE task_id=$1`, task.ID)
		_, _ = d.Exec(`DELETE FROM task_archives WHERE task_id=$1`, task.ID)
		_ = d.DeleteTask(task.ID)
	}()
	if err := d.SetPaused(task.ID, true); err != nil {
		t.Fatal(err)
	}
	store := d.Exploration(task.ExplorationID)
	nodeID, err := store.AddNode(KindFact, map[string]any{"summary": "legacy fact"}, 1, "confirmed", "worker", nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := d.AddFinding(task.ID, nodeID, "idor", "legacy finding", SeverityHigh, "summary", "evidence", "worker", nil); err != nil {
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
	// Simulate a pre-0020 snapshot: findings rows predate the columns.
	stripped, err := stripFindingColumns(snapshot.Tables["findings"], "confidence", "fingerprint")
	if err != nil {
		t.Fatal(err)
	}
	snapshot.Tables["findings"] = stripped
	if err := d.CompleteTaskArchive(archive.ID, snapshot, "/tmp/test-task.tar.zst", "abc", 100, 50); err != nil {
		t.Fatal(err)
	}
	if _, err := d.QueueTaskArchiveRestore(archive.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := d.ClaimTaskArchiveJob(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err := d.RestoreTaskArchive(archive.ID, snapshot, int64((10 * time.Minute).Seconds())); err != nil {
		t.Fatalf("restore pre-0020 findings: %v", err)
	}
	var confidence int
	var fingerprint string
	if err := d.QueryRow(`SELECT confidence,COALESCE(fingerprint,'') FROM findings WHERE task_id=$1`, task.ID).Scan(&confidence, &fingerprint); err != nil {
		t.Fatal(err)
	}
	if confidence != 0 || fingerprint != "" {
		t.Fatalf("expected NOT NULL defaults, got confidence=%d fingerprint=%q", confidence, fingerprint)
	}
	if err := d.CompleteTaskArchiveRestore(archive.ID); err != nil {
		t.Fatal(err)
	}
}

func stripFindingColumns(raw json.RawMessage, keys ...string) (json.RawMessage, error) {
	rows := []json.RawMessage{}
	if err := json.Unmarshal(raw, &rows); err != nil {
		return nil, err
	}
	for i, row := range rows {
		fields := map[string]json.RawMessage{}
		if err := json.Unmarshal(row, &fields); err != nil {
			return nil, err
		}
		for _, key := range keys {
			delete(fields, key)
		}
		rewritten, err := json.Marshal(fields)
		if err != nil {
			return nil, err
		}
		rows[i] = rewritten
	}
	return json.Marshal(rows)
}

func TestTaskArchiveRoundTripKeepsIntentCheckpoints(t *testing.T) {
	d, err := Open(testDSN(t))
	if err != nil {
		SkipPGUnavailable(t, err)
	}
	defer d.Close()
	task, err := d.CreateTaskWithOptions("checkpoint roundtrip", "restore checkpoints", TaskCreateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		_, _ = d.Exec(`DELETE FROM findings WHERE task_id=$1`, task.ID)
		_, _ = d.Exec(`DELETE FROM task_archives WHERE task_id=$1`, task.ID)
		_ = d.DeleteTask(task.ID)
	}()
	if err := d.SetPaused(task.ID, true); err != nil {
		t.Fatal(err)
	}
	store := d.Exploration(task.ExplorationID)
	intentID, err := store.AddNode(KindIntent, map[string]any{"summary": "archived intent"}, 1, "running", "worker", nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := d.AddCheckpoint(IntentCheckpoint{
		TaskID: task.ID, IntentID: intentID, AgentName: "worker", Attempt: 2, Phase: "exploit",
		NextStep: "retry upload", Done: []string{"fingerprint"}, Open: []string{"bypass"}, Artifacts: []string{"i1"}, ToolCalls: 9,
	}); err != nil {
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
	if snapshot.DataCounts["intent_checkpoints"] != 1 || rawRowCount(snapshot.Tables["intent_checkpoints"]) != 1 {
		t.Fatalf("snapshot must retain checkpoints: counts=%v rows=%d", snapshot.DataCounts["intent_checkpoints"], rawRowCount(snapshot.Tables["intent_checkpoints"]))
	}
	if err := d.CompleteTaskArchive(archive.ID, snapshot, "/tmp/test-task.tar.zst", "abc", 100, 50); err != nil {
		t.Fatal(err)
	}
	// Hot compaction deletes the intent node, FK-cascading the live checkpoint.
	var liveCheckpoints int
	if err := d.QueryRow(`SELECT count(*) FROM intent_checkpoints WHERE intent_id=$1`, intentID).Scan(&liveCheckpoints); err != nil {
		t.Fatal(err)
	}
	if liveCheckpoints != 0 {
		t.Fatalf("hot compaction must cascade checkpoints, found %d", liveCheckpoints)
	}
	if _, err := d.QueueTaskArchiveRestore(archive.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := d.ClaimTaskArchiveJob(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err := d.RestoreTaskArchive(archive.ID, snapshot, int64((10 * time.Minute).Seconds())); err != nil {
		t.Fatalf("restore checkpoints: %v", err)
	}
	restored, err := d.LatestCheckpoint(intentID)
	if err != nil || restored == nil {
		t.Fatalf("restored checkpoint = %+v, %v", restored, err)
	}
	if restored.AgentName != "worker" || restored.Attempt != 2 || restored.Phase != "exploit" ||
		len(restored.Done) != 1 || restored.Done[0] != "fingerprint" ||
		len(restored.Open) != 1 || restored.Open[0] != "bypass" || restored.ToolCalls != 9 {
		t.Fatalf("restored checkpoint payload mismatch: %+v", restored)
	}
	if err := d.CompleteTaskArchiveRestore(archive.ID); err != nil {
		t.Fatal(err)
	}
}

func TestTaskArchiveBlockersIgnoreQueuedDependents(t *testing.T) {
	d, err := Open(testDSN(t))
	if err != nil {
		SkipPGUnavailable(t, err)
	}
	defer d.Close()
	source, err := d.CreateTaskWithOptions("archive blocker source", "source", TaskCreateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	dependent, err := d.CreateTaskWithOptions("archive blocker dependent", "dependent", TaskCreateOptions{
		SourceTaskIDs: []int64{source.ID},
	})
	if err != nil {
		_ = d.DeleteTask(source.ID)
		t.Fatal(err)
	}
	defer func() {
		_, _ = d.Exec(`DELETE FROM task_archives WHERE task_id IN ($1,$2)`, source.ID, dependent.ID)
		_ = d.DeleteTask(dependent.ID)
		_ = d.DeleteTask(source.ID)
	}()

	blockers, err := d.TaskArchiveBlockers()
	if err != nil {
		t.Fatal(err)
	}
	if blockers[source.ID] != dependent.ID {
		t.Fatalf("source blocker=%d, want dependent %d", blockers[source.ID], dependent.ID)
	}
	assertPerTaskBlockers(t, d, blockers, map[int64]int64{source.ID: dependent.ID, dependent.ID: 0})
	if err := d.SetPaused(dependent.ID, true); err != nil {
		t.Fatal(err)
	}
	if _, err := d.QueueTaskArchive(dependent.ID); err != nil {
		t.Fatal(err)
	}
	blockers, err = d.TaskArchiveBlockers()
	if err != nil {
		t.Fatal(err)
	}
	if blocker := blockers[source.ID]; blocker != 0 {
		t.Fatalf("queued dependent must not block source, got %d", blocker)
	}
	assertPerTaskBlockers(t, d, blockers, map[int64]int64{source.ID: 0, dependent.ID: 0})
}

// assertPerTaskBlockers checks TaskArchiveBlocker against the expected blocker
// of each task and against the global TaskArchiveBlockers map.
func assertPerTaskBlockers(t *testing.T, d *DB, global map[int64]int64, want map[int64]int64) {
	t.Helper()
	for taskID, wantBlocker := range want {
		got, err := d.TaskArchiveBlocker(taskID)
		if err != nil {
			t.Fatal(err)
		}
		if got != wantBlocker {
			t.Fatalf("TaskArchiveBlocker(%d)=%d, want %d", taskID, got, wantBlocker)
		}
		if got != global[taskID] {
			t.Fatalf("TaskArchiveBlocker(%d)=%d, TaskArchiveBlockers()[%d]=%d", taskID, got, taskID, global[taskID])
		}
	}
}

func TestRecoverInterruptedArchiveRequiresManualRetry(t *testing.T) {
	d, err := Open(testDSN(t))
	if err != nil {
		SkipPGUnavailable(t, err)
	}
	defer d.Close()
	task, err := d.CreateTaskWithOptions("interrupted archive", "must not restart automatically", TaskCreateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		_, _ = d.Exec(`DELETE FROM task_archives WHERE task_id=$1`, task.ID)
		_ = d.DeleteTask(task.ID)
	}()
	if err := d.SetPaused(task.ID, true); err != nil {
		t.Fatal(err)
	}
	queued, err := d.QueueTaskArchive(task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := d.Exec(`UPDATE task_archives SET state=$2,phase='snapshot_database',progress=10 WHERE id=$1`, queued.ID, Archiving); err != nil {
		t.Fatal(err)
	}
	if err := d.RecoverTaskArchiveJobs(); err != nil {
		t.Fatal(err)
	}
	recovered, err := d.GetTaskArchive(queued.ID)
	if err != nil {
		t.Fatal(err)
	}
	if recovered == nil || recovered.State != ArchiveFailed || recovered.Phase != "interrupted" || recovered.Error == "" {
		t.Fatalf("recovered archive=%+v, want explicit-retry failure", recovered)
	}
}

func assertArchiveGlobalStats(t *testing.T, d *DB, profileName, skillName, toolName, vulnclass string) {
	t.Helper()
	profiles, err := d.UsageByProfile()
	if err != nil {
		t.Fatal(err)
	}
	matchedProfile := false
	for _, profile := range profiles {
		if profile.ProfileName != profileName {
			continue
		}
		matchedProfile = true
		if profile.Calls != 1 || profile.Tasks != 1 || profile.InputTokens != 11 || profile.OutputTokens != 7 {
			t.Fatalf("archive profile aggregate double-counted or missing: %+v", profile)
		}
	}
	if !matchedProfile {
		t.Fatalf("archive profile aggregate %q missing", profileName)
	}
	skills, err := d.SkillStats()
	if err != nil {
		t.Fatal(err)
	}
	matchedSkill := false
	for _, skill := range skills {
		if skill.Skill == skillName {
			matchedSkill = skill.Calls == 1 && skill.Tasks == 1
		}
	}
	if !matchedSkill {
		t.Fatalf("archive skill aggregate %q missing or double-counted: %+v", skillName, skills)
	}
	tools, err := d.ToolUsageCounts()
	if err != nil {
		t.Fatal(err)
	}
	if tools[toolName] != 1 {
		t.Fatalf("archive tool aggregate %q=%d, want 1", toolName, tools[toolName])
	}
	findings, err := d.FindingStats(0)
	if err != nil {
		t.Fatal(err)
	}
	foundClass := false
	for _, item := range findings.VulnClasses {
		if item == vulnclass {
			foundClass = true
		}
	}
	if !foundClass {
		t.Fatalf("archive finding class %q missing", vulnclass)
	}
}

// An archive written before report_state existed restores with the column's
// default instead of failing on the NOT NULL constraint.
func TestFillArchiveDefaults(t *testing.T) {
	raw := json.RawMessage(`[{"id":1,"summary":"old"},{"id":2,"report_state":null},{"id":3,"report_state":"submitted"}]`)
	out, err := fillArchiveDefaults("findings", raw)
	if err != nil {
		t.Fatal(err)
	}
	var rows []map[string]json.RawMessage
	if err := json.Unmarshal(out, &rows); err != nil {
		t.Fatal(err)
	}
	want := []string{`"none"`, `"none"`, `"submitted"`}
	for i, row := range rows {
		if string(row["report_state"]) != want[i] {
			t.Fatalf("row %d report_state=%s, want %s", i, row["report_state"], want[i])
		}
	}

	// Side memory keeps its own default, and other tables pass through.
	out, err = fillArchiveDefaults("side_question_sessions", json.RawMessage(`[{"id":1}]`))
	if err != nil || !strings.Contains(string(out), `"memory":{}`) {
		t.Fatalf("side memory default missing: %s err=%v", out, err)
	}
	in := json.RawMessage(`[{"id":1}]`)
	out, err = fillArchiveDefaults("activity", in)
	if err != nil || string(out) != string(in) {
		t.Fatalf("activity changed: %s err=%v", out, err)
	}
}

// TestTaskArchiveRestoresSwarmTables proves a months-old task comes back with its
// swarm state intact: agent events (streamed), discoveries, attack paths and steps,
// the coverage ledger, and the project knowledge it contributed. Knowledge is
// merged by identity, never duplicated, and a second restore is a no-op.
func TestTaskArchiveRestoresSwarmTables(t *testing.T) {
	d, err := Open(testDSN(t))
	if err != nil {
		SkipPGUnavailable(t, err)
	}
	defer d.Close()
	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	task, err := d.CreateTaskWithOptions("swarm archive "+suffix, "restore swarm state", TaskCreateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	knowledgeTitle := "swarm archive fact " + suffix
	defer func() {
		_, _ = d.Exec(`DELETE FROM knowledge_injections WHERE task_id=$1`, task.ID)
		_, _ = d.Exec(`DELETE FROM knowledge_items WHERE title=$1`, knowledgeTitle)
		_, _ = d.Exec(`DELETE FROM task_archives WHERE task_id=$1`, task.ID)
		_ = d.DeleteTask(task.ID)
	}()
	store := d.Exploration(task.ExplorationID)
	factID, err := store.AddNode(KindFact, map[string]any{"summary": "archived swarm fact"}, 1, "confirmed", "worker", nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := d.AddDiscovery(DiscoveryEvent{TaskID: task.ID, AgentName: "worker#0", Kind: "fact", Summary: "found /admin", NodeID: factID}); err != nil {
		t.Fatal(err)
	}
	const eventCount = 300
	for i := 0; i < eventCount; i++ {
		if _, err := d.AddAgentEvent(AgentEvent{TaskID: task.ID, AgentName: "worker#0", Kind: EventPathStep,
			Detail: json.RawMessage(fmt.Sprintf(`{"step":%d}`, i))}); err != nil {
			t.Fatal(err)
		}
	}
	var pathID int64
	if err := d.QueryRow(`INSERT INTO attack_paths (task_id,title,rationale,state,score,created_by)
VALUES ($1,'archive path','chain','validating',7.5,'chain') RETURNING id`, task.ID).Scan(&pathID); err != nil {
		t.Fatal(err)
	}
	for position, kind := range []string{"asset", "finding"} {
		if _, err := d.Exec(`INSERT INTO attack_path_steps (path_id,position,kind,ref_id,precondition,postcondition)
VALUES ($1,$2,$3,$4,'login','shell')`, pathID, position, kind, factID); err != nil {
			t.Fatal(err)
		}
	}
	assets := d.Assets()
	if err := assets.RecordCoverage(task.ID, CoverageEntry{Surface: "/admin", RiskArea: "auth", Outcome: OutcomeNeedsFollowUp, Reason: "not enough evidence", Worker: "worker#0"}); err != nil {
		t.Fatal(err)
	}
	if err := assets.RecordCoverage(task.ID, CoverageEntry{Surface: "/login", RiskArea: "sqli", Outcome: OutcomeNoIssueFound, Evidence: "parameterised", Worker: "worker#0"}); err != nil {
		t.Fatal(err)
	}
	itemID, err := d.UpsertKnowledge(t.Context(), KnowledgeItem{
		Kind: "asset_fact", Title: knowledgeTitle, Body: "restores from the archive", ScopeAssetKey: "swarm.example" + suffix,
		SourceTaskID: &task.ID,
		Source:       &KnowledgeSource{Kind: "task", ID: task.ID, ContentHash: KnowledgeContentHash("asset_fact", knowledgeTitle, "restores from the archive")},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := d.LogKnowledgeInjection(itemID, task.ID, nil, "worker#0", "inject"); err != nil {
		t.Fatal(err)
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
	var events bytes.Buffer
	snapshot, err := d.SnapshotTaskArchiveWithStreams(task.ID, TaskArchiveStreamWriters{AgentEvents: &events})
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.StreamedTables["agent_events"] == "" {
		t.Fatal("agent_events must stream, not ride in the manifest")
	}
	if snapshot.DataCounts["attack_paths"] != 1 || snapshot.DataCounts["coverage_ledger"] != 2 || snapshot.DataCounts["knowledge_items"] < 1 {
		t.Fatalf("snapshot counts missing swarm tables: %v", snapshot.DataCounts)
	}
	if err := d.CompleteTaskArchive(archive.ID, snapshot, "/tmp/swarm-task.tar.zst", "abc", 100, 50); err != nil {
		t.Fatal(err)
	}
	for _, table := range []struct {
		name  string
		query string
	}{
		{"agent_events", `SELECT count(*) FROM agent_events WHERE task_id=$1`},
		{"discovery_events", `SELECT count(*) FROM discovery_events WHERE task_id=$1`},
		{"attack_paths", `SELECT count(*) FROM attack_paths WHERE task_id=$1`},
		{"coverage_ledger", `SELECT count(*) FROM coverage_ledger WHERE task_id=$1`},
		{"knowledge_injections", `SELECT count(*) FROM knowledge_injections WHERE task_id=$1`},
	} {
		var live int
		if err := d.QueryRow(table.query, task.ID).Scan(&live); err != nil {
			t.Fatal(err)
		}
		if live != 0 {
			t.Fatalf("hot compaction must move %s into the package, found %d live rows", table.name, live)
		}
	}
	if _, err := d.QueueTaskArchiveRestore(archive.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := d.ClaimTaskArchiveJob(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err := d.RestoreTaskArchiveWithStreams(archive.ID, snapshot, int64((10 * time.Minute).Seconds()),
		TaskArchiveStreamReaders{AgentEvents: bytes.NewReader(events.Bytes())}); err != nil {
		t.Fatalf("restore swarm tables: %v", err)
	}
	var restoredEvents, restoredDiscoveries, restoredPaths, restoredSteps, restoredLedger, restoredHistory, restoredInjections int
	if err := d.QueryRow(`SELECT count(*) FROM agent_events WHERE task_id=$1`, task.ID).Scan(&restoredEvents); err != nil {
		t.Fatal(err)
	}
	if err := d.QueryRow(`SELECT count(*) FROM discovery_events WHERE task_id=$1`, task.ID).Scan(&restoredDiscoveries); err != nil {
		t.Fatal(err)
	}
	if err := d.QueryRow(`SELECT count(*) FROM attack_paths WHERE task_id=$1`, task.ID).Scan(&restoredPaths); err != nil {
		t.Fatal(err)
	}
	if err := d.QueryRow(`SELECT count(*) FROM attack_path_steps step JOIN attack_paths path ON path.id=step.path_id WHERE path.task_id=$1`, task.ID).Scan(&restoredSteps); err != nil {
		t.Fatal(err)
	}
	if err := d.QueryRow(`SELECT count(*) FROM coverage_ledger WHERE task_id=$1`, task.ID).Scan(&restoredLedger); err != nil {
		t.Fatal(err)
	}
	if err := d.QueryRow(`SELECT count(*) FROM coverage_ledger_history WHERE task_id=$1`, task.ID).Scan(&restoredHistory); err != nil {
		t.Fatal(err)
	}
	if err := d.QueryRow(`SELECT count(*) FROM knowledge_injections WHERE task_id=$1`, task.ID).Scan(&restoredInjections); err != nil {
		t.Fatal(err)
	}
	if restoredEvents != eventCount || restoredDiscoveries != 1 || restoredPaths != 1 || restoredSteps != 2 {
		t.Fatalf("restored swarm rows = events %d discoveries %d paths %d steps %d", restoredEvents, restoredDiscoveries, restoredPaths, restoredSteps)
	}
	if restoredLedger != 2 || restoredHistory < 2 || restoredInjections != 1 {
		t.Fatalf("restored coverage/injections = ledger %d history %d injections %d", restoredLedger, restoredHistory, restoredInjections)
	}
	// Knowledge is shared, so the archived item merges back into the surviving row.
	var mergedItems int
	var mergedSupport int
	if err := d.QueryRow(`SELECT count(*),COALESCE(max(support),0) FROM knowledge_items WHERE title=$1`, knowledgeTitle).Scan(&mergedItems, &mergedSupport); err != nil {
		t.Fatal(err)
	}
	if mergedItems != 1 || mergedSupport < 1 {
		t.Fatalf("knowledge merge produced %d items with support %d", mergedItems, mergedSupport)
	}
	var injectionItemID int64
	if err := d.QueryRow(`SELECT item_id FROM knowledge_injections WHERE task_id=$1`, task.ID).Scan(&injectionItemID); err != nil {
		t.Fatal(err)
	}
	if injectionItemID != itemID {
		t.Fatalf("injection points at item %d, want the merged live item %d", injectionItemID, itemID)
	}
	// A retry before the archive is marked complete (the crash-after-commit case)
	// must return the recorded warnings instead of restoring the rows twice.
	if _, err := d.RestoreTaskArchiveWithStreams(archive.ID, snapshot, 0,
		TaskArchiveStreamReaders{AgentEvents: bytes.NewReader(events.Bytes())}); err != nil {
		t.Fatalf("idempotent restore: %v", err)
	}
	var afterRetry int
	if err := d.QueryRow(`SELECT count(*) FROM agent_events WHERE task_id=$1`, task.ID).Scan(&afterRetry); err != nil {
		t.Fatal(err)
	}
	if afterRetry != eventCount {
		t.Fatalf("second restore duplicated events: %d", afterRetry)
	}
	if err := d.CompleteTaskArchiveRestore(archive.ID); err != nil {
		t.Fatal(err)
	}
}
