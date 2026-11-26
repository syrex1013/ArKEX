package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Autumn-27/artex/db"
)

// Records/review/brief endpoints against a live manager: create revision v1,
// append v2 and accept it (auto-superseding v1), list records, walk the
// timeline, stamp a review cursor and diff it, then read the brief.
func TestEngagementRecordsAPI(t *testing.T) {
	m, err := NewManager(t.TempDir(), "")
	if err != nil {
		db.SkipPGUnavailable(t, err)
	}
	defer m.Close()
	eng, err := m.pg.CreateEngagement(fmt.Sprintf("__test_eng_api_%d__", time.Now().UnixNano()), "records api")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _, _ = m.pg.DeleteEngagement(eng.ID) }()
	task, err := m.pg.CreateTaskWithOptions("records api task", "goal", db.TaskCreateOptions{EngagementID: &eng.ID})
	if err != nil {
		t.Fatal(err)
	}
	taskID := task.ID
	defer func() { _ = m.pg.DeleteTask(taskID) }()

	ctx, cancel := context.WithCancel(context.Background())
	s := New(ctx, m, t.TempDir(), t.TempDir(), t.TempDir())
	defer stopTestServer(t, s, cancel)
	token, err := signJWT(s.jwtKey)
	if err != nil {
		t.Fatal(err)
	}
	call := func(method, path, body string) (int, map[string]any) {
		var req *http.Request
		if body == "" {
			req = httptest.NewRequest(method, path, nil)
		} else {
			req = httptest.NewRequest(method, path, strings.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
		}
		req.Header.Set("Authorization", "Bearer "+token)
		rec := httptest.NewRecorder()
		s.Handler().ServeHTTP(rec, req)
		var out map[string]any
		_ = json.Unmarshal(rec.Body.Bytes(), &out)
		return rec.Code, out
	}
	base := fmt.Sprintf("/api/engagements/%d", eng.ID)

	// Validation: bad kind is rejected before anything is written.
	if code, out := call(http.MethodPost, base+"/records", `{"kind":"nope","title":"x"}`); code != http.StatusBadRequest {
		t.Fatalf("bad kind status=%d body=%v", code, out)
	}

	code, out := call(http.MethodPost, base+"/records",
		`{"kind":"scope_revision","title":"scope v1","body":"assets: a.example added","actor":"alice"}`)
	if code != http.StatusCreated {
		t.Fatalf("create v1 status=%d body=%v", code, out)
	}
	v1 := int64(out["id"].(float64))

	code, out = call(http.MethodPost, base+"/records",
		`{"kind":"scope_revision","title":"scope v2","body":"assets: b.example added","actor":"alice"}`)
	if code != http.StatusCreated {
		t.Fatalf("create v2 status=%d body=%v", code, out)
	}
	v2 := int64(out["id"].(float64))
	if v2 <= v1 {
		t.Fatalf("v2 id %d not after v1 id %d", v2, v1)
	}

	// Accepting v2 must auto-supersede the previously accepted revision —
	// but v1 is still open, so accept it first, then v2.
	if code, out := call(http.MethodPatch, fmt.Sprintf("/api/engagements/records/%d", v1), `{"status":"accepted"}`); code != http.StatusOK {
		t.Fatalf("accept v1 status=%d body=%v", code, out)
	}
	if code, out := call(http.MethodPatch, fmt.Sprintf("/api/engagements/records/%d", v2), `{"status":"accepted"}`); code != http.StatusOK {
		t.Fatalf("accept v2 status=%d body=%v", code, out)
	}
	rec, err := m.pg.GetEngagementRecord(v1)
	if err != nil {
		t.Fatal(err)
	}
	if rec.Status != db.RecordStatusSuperseded || rec.SupersedesID != nil {
		t.Fatalf("v1 status=%s supersedes=%v, want superseded/nil (supersedes points forward)", rec.Status, rec.SupersedesID)
	}
	rec, err = m.pg.GetEngagementRecord(v2)
	if err != nil {
		t.Fatal(err)
	}
	if rec.Status != db.RecordStatusAccepted || rec.SupersedesID == nil || *rec.SupersedesID != v1 {
		t.Fatalf("v2 status=%s supersedes=%v, want accepted pointing at v1", rec.Status, rec.SupersedesID)
	}

	// Superseded rows are immutable.
	if code, _ := call(http.MethodPatch, fmt.Sprintf("/api/engagements/records/%d", v1), `{"status":"open"}`); code != http.StatusBadRequest {
		t.Fatalf("revive superseded v1 status=%d, want 400", code)
	}

	// List: newest first.
	code, out = call(http.MethodGet, base+"/records", "")
	if code != http.StatusOK {
		t.Fatalf("list status=%d body=%v", code, out)
	}
	list, _ := out["records"].([]any)
	if len(list) != 2 {
		t.Fatalf("records len=%d want 2", len(list))
	}
	first, _ := list[0].(map[string]any)
	if int64(first["id"].(float64)) != v2 {
		t.Fatalf("list newest=%v want v2", first["id"])
	}

	// Timeline: carries the records plus a task_outcome delta.
	code, out = call(http.MethodGet, base+"/timeline", "")
	if code != http.StatusOK {
		t.Fatalf("timeline status=%d body=%v", code, out)
	}
	entries, _ := out["entries"].([]any)
	kinds := map[string]int{}
	sawRecord := false
	for _, raw := range entries {
		e, _ := raw.(map[string]any)
		k := e["kind"].(string)
		kinds[k]++
		if k == db.RecordKindScopeRevision {
			sawRecord = true
			if link, _ := e["link"].(string); !strings.Contains(link, fmt.Sprintf("id=%d", taskID)) && e["link"] != "" {
				t.Fatalf("record entry link=%q", e["link"])
			}
		}
	}
	if !sawRecord || kinds["task_outcome"] < 1 {
		t.Fatalf("timeline missing scope_revision or task_outcome: %v (entries=%d)", kinds, len(entries))
	}

	// Review: first click returns the full diff set, second returns nothing.
	code, out = call(http.MethodPost, base+"/review", `{"operator":"bob"}`)
	if code != http.StatusOK {
		t.Fatalf("review status=%d body=%v", code, out)
	}
	rev, _ := out["review"].(map[string]any)
	if int64(rev["up_to_record_id"].(float64)) != v2 {
		t.Fatalf("review cursor=%v want %d", rev["up_to_record_id"], v2)
	}
	if diff, _ := out["records"].([]any); len(diff) != 2 {
		t.Fatalf("first review diff=%d want 2", len(diff))
	}
	code, out = call(http.MethodPost, base+"/review", `{"operator":"bob"}`)
	if code != http.StatusOK {
		t.Fatalf("second review status=%d body=%v", code, out)
	}
	if diff, _ := out["records"].([]any); len(diff) != 0 {
		t.Fatalf("second review diff=%d want 0", len(diff))
	}
	if changed, _ := out["entries"].([]any); len(changed) != 0 {
		t.Fatalf("second review timeline delta=%d want 0", len(changed))
	}

	// Brief: active scope = v2 (latest accepted), review delta for bob is
	// empty after the review above, full record set without operator.
	code, out = call(http.MethodGet, base+"/brief?operator=bob", "")
	if code != http.StatusOK {
		t.Fatalf("brief status=%d body=%v", code, out)
	}
	brief, _ := out["brief"].(map[string]any)
	scope, _ := brief["active_scope"].(map[string]any)
	if scope == nil || scope["title"] != "scope v2" {
		t.Fatalf("brief active_scope=%v want scope v2", brief["active_scope"])
	}
	if recent, _ := brief["recent_records"].([]any); len(recent) != 0 {
		t.Fatalf("bob brief recent_records=%d want 0 (reviewed)", len(recent))
	}
	if rev, _ := brief["review"].(map[string]any); rev == nil {
		t.Fatalf("bob brief missing review cursor")
	}

	code, out = call(http.MethodGet, base+"/brief", "")
	if code != http.StatusOK {
		t.Fatalf("brief no-operator status=%d body=%v", code, out)
	}
	brief = out["brief"].(map[string]any)
	if recent, _ := brief["recent_records"].([]any); len(recent) != 2 {
		t.Fatalf("no-operator recent_records=%d want 2", len(recent))
	}

	// Brief for a nonexistent engagement is 404, not a 500.
	code, _ = call(http.MethodGet, "/api/engagements/999999999/brief", "")
	if code != http.StatusNotFound {
		t.Fatalf("missing engagement brief status=%d, want 404", code)
	}
}
