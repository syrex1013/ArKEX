package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Autumn-27/artex/db"
)

// TestEngagementQueueUnion proves the operator queue unions every "needs a human"
// source of one engagement, counts them per kind, and 404s an unknown engagement.
func TestEngagementQueueUnion(t *testing.T) {
	m, err := NewManager(t.TempDir(), "")
	if err != nil {
		db.SkipPGUnavailable(t, err)
	}
	defer m.Close()
	ctx, cancel := context.WithCancel(context.Background())
	s := New(ctx, m, t.TempDir(), t.TempDir(), t.TempDir())
	defer stopTestServer(t, s, cancel)
	token, err := signJWT(s.jwtKey)
	if err != nil {
		t.Fatal(err)
	}
	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	eng, err := m.pg.CreateEngagement("queue eng "+suffix, "operator queue test")
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		_, _ = m.pg.Exec(`DELETE FROM engagement_records WHERE engagement_id=$1`, eng.ID)
		_, _ = m.pg.DeleteEngagement(eng.ID)
	}()
	engID := eng.ID
	task, err := m.pg.CreateTaskWithOptions("queue task "+suffix, "queue sources", db.TaskCreateOptions{EngagementID: &engID})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		_, _ = m.pg.Exec(`DELETE FROM intercept_pending WHERE task_id=$1`, fmt.Sprintf("%d", task.ID))
		_, _ = m.pg.Exec(`DELETE FROM coverage_ledger_history WHERE task_id=$1`, task.ID)
		_, _ = m.pg.Exec(`DELETE FROM coverage_ledger WHERE task_id=$1`, task.ID)
		_ = m.pg.DeleteTask(task.ID)
	}()

	// decision (accepted) + open decision + milestone without criteria
	if _, err := m.pg.CreateEngagementRecord(eng.ID, &db.EngagementRecord{
		Kind: db.RecordKindScopeRevision, Title: "Scope v1", Body: "host-a added", Status: db.RecordStatusAccepted,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := m.pg.CreateEngagementRecord(eng.ID, &db.EngagementRecord{
		Kind: db.RecordKindOperatorDecision, Title: "focus on cc360", Body: "escalate the upload path",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := m.pg.CreateEngagementRecord(eng.ID, &db.EngagementRecord{
		Kind: db.RecordKindMilestone, Title: "reproducible shell", Body: "get id output",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := m.pg.AddFinding(task.ID, 0, "queue_vulnclass", "pending finding", db.SeverityHigh,
		"needs verification", "", "worker#0", nil); err != nil {
		t.Fatal(err)
	}
	if err := m.pg.Assets().RecordCoverage(task.ID, db.CoverageEntry{
		Surface: "/admin", RiskArea: "auth", Outcome: db.OutcomeNeedsFollowUp, Reason: "no evidence yet", Worker: "worker#0",
	}); err != nil {
		t.Fatal(err)
	}
	var pathID int64
	if err := m.pg.QueryRow(`INSERT INTO attack_paths (task_id,title,rationale,state,score,created_by)
VALUES ($1,'queue path','chain','validating',3.5,'chain') RETURNING id`, task.ID).Scan(&pathID); err != nil {
		t.Fatal(err)
	}
	if _, err := m.pg.Exec(`INSERT INTO intercept_pending (task_id, agent_name, tool_name, tool_input, status)
VALUES ($1,'worker#0','bash','{}'::jsonb,'pending')`, fmt.Sprintf("%d", task.ID)); err != nil {
		t.Fatal(err)
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/engagements/%d/queue", eng.ID), nil)
	req.Header.Set("Authorization", "Bearer "+token)
	s.Handler().ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("queue status = %d body %s", rec.Code, rec.Body.String())
	}
	var body struct {
		Items []struct {
			Kind     string    `json:"kind"`
			Title    string    `json:"title"`
			TaskID   int64     `json:"task_id"`
			TaskName string    `json:"task_name"`
			Link     string    `json:"link"`
			Detail   string    `json:"detail"`
			Created  time.Time `json:"created_at"`
		} `json:"items"`
		Counts map[string]int `json:"counts"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	want := map[string]int{"decision": 2, "milestone": 1, "finding": 1, "path": 1, "coverage_gap": 1, "approval": 1}
	for kind, n := range want {
		if body.Counts[kind] != n {
			t.Fatalf("counts[%s] = %d want %d (all: %v)", kind, body.Counts[kind], n, body.Counts)
		}
	}
	if len(body.Items) != 7 {
		t.Fatalf("items = %d want 7: %+v", len(body.Items), body.Items)
	}
	seen := map[string]bool{}
	for i, item := range body.Items {
		seen[item.Kind] = true
		if i > 0 && item.Created.After(body.Items[i-1].Created) {
			t.Fatalf("queue not newest-first at %d: %v after %v", i, item.Created, body.Items[i-1].Created)
		}
		if item.Kind == "finding" || item.Kind == "path" || item.Kind == "coverage_gap" || item.Kind == "approval" {
			if item.TaskID != task.ID || item.TaskName == "" {
				t.Fatalf("%s item lost its task context: %+v", item.Kind, item)
			}
			if item.Link == "" {
				t.Fatalf("%s item has no link", item.Kind)
			}
		}
	}
	for kind := range want {
		if !seen[kind] {
			t.Fatalf("queue missing %s item", kind)
		}
	}

	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/api/engagements/99999999/queue", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	s.Handler().ServeHTTP(rec, req)
	if rec.Code != 404 {
		t.Fatalf("unknown engagement status = %d", rec.Code)
	}
}
