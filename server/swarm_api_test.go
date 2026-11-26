package server

// TestSwarmAPIPG exercises the swarm observability endpoints through the real
// HTTP mux on PostgreSQL: roster/paths/knowledge/progress shapes, the step
// verdict mutations, and 404 for unknown tasks. Skips when PG is unavailable.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Autumn-27/artex/db"
)

func TestSwarmAPIPG(t *testing.T) {
	m, err := NewManager(t.TempDir(), "")
	if err != nil {
		db.SkipPGUnavailable(t, err)
	}
	defer m.Close()
	td := t.TempDir()
	ctx, cancel := context.WithCancel(context.Background())
	s := New(ctx, m, td, td, td)
	defer stopTestServer(t, s, cancel)
	h := s.Handler()
	tok, err := signJWT(s.jwtKey)
	if err != nil {
		t.Fatalf("sign jwt: %v", err)
	}

	do := func(method, path string, body any) (int, map[string]any) {
		var r *http.Request
		if body != nil {
			b, _ := json.Marshal(body)
			r = httptest.NewRequest(method, path, strings.NewReader(string(b)))
		} else {
			r = httptest.NewRequest(method, path, nil)
		}
		r.Header.Set("Authorization", "Bearer "+tok)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, r)
		var out map[string]any
		_ = json.Unmarshal(rec.Body.Bytes(), &out)
		return rec.Code, out
	}
	doList := func(path string) (int, []any) {
		r := httptest.NewRequest("GET", path, nil)
		r.Header.Set("Authorization", "Bearer "+tok)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, r)
		var out []any
		_ = json.Unmarshal(rec.Body.Bytes(), &out)
		return rec.Code, out
	}

	// Unknown task ids 404 on every route (resolve before any store access).
	for _, p := range []string{"/api/tasks/99999999/swarm", "/api/tasks/99999999/paths", "/api/tasks/99999999/knowledge", "/api/tasks/99999999/progress"} {
		if code, _ := do("GET", p, nil); code != 404 {
			t.Fatalf("%s: want 404, got %d", p, code)
		}
	}
	if code, _ := do("GET", "/api/tasks/99999999/swarm/stream", nil); code != 404 {
		t.Fatalf("stream unknown task: want 404, got %d", code)
	}

	// Engagement → task, so the knowledge row has a scope to match.
	// Engagement names are globally unique — suffix with the run time.
	code, out := do("POST", "/api/engagements", map[string]string{"name": "swarm test " + time.Now().Format("150405.000000000"), "description": ""})
	if code != 201 {
		t.Fatalf("create engagement: %d (%v)", code, out)
	}
	engID := int64(out["id"].(float64))

	code, out = do("POST", "/api/tasks", map[string]any{
		"description":   "swarm api smoke",
		"goal":          "Probe the lab target at http://127.0.0.1:9/v1 for unauthenticated data exposure; record one record as evidence and stop.",
		"engagement_id": engID,
	})
	if code != 201 {
		t.Fatalf("create task: %d (%v)", code, out)
	}
	// the task id is the PG row id serialized as a string
	taskIDStr, _ := out["id"].(string)
	taskID, _ := strconv.ParseInt(taskIDStr, 10, 64)
	if taskIDStr == "" || taskID <= 0 {
		t.Fatalf("bad task payload: %v", out)
	}
	expID := int64(out["exploration_id"].(float64))
	pg := m.PG()

	// Seed: roster, agent events, a checkpoint, a proposed path, knowledge.
	roster, err := pg.EnsureTaskAgents(taskID, []string{"general", "recon"})
	if err != nil {
		t.Fatal(err)
	}
	if len(roster) < 2 {
		t.Fatalf("roster: want >=2 agents, got %d", len(roster))
	}
	if _, err := pg.AddAgentEvent(db.AgentEvent{TaskID: taskID, AgentName: "general#1", Kind: db.EventAgentClaim, Detail: json.RawMessage(`{"intent":7}`)}); err != nil {
		t.Fatal(err)
	}
	store := pg.Exploration(expID)
	f1, err := store.AddNode("fact", map[string]any{"text": "admin panel at /admin"}, 0, "confirmed", "test", nil)
	if err != nil {
		t.Fatal(err)
	}
	f2, err := store.AddNode("fact", map[string]any{"text": "default credentials work"}, 0, "confirmed", "test", nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pg.AddCheckpoint(db.IntentCheckpoint{TaskID: taskID, IntentID: f1, AgentName: "general#1", Phase: "probe", NextStep: "check /admin", Done: []string{"nmap"}, ToolCalls: 3}); err != nil {
		t.Fatal(err)
	}
	tx, err := pg.Begin()
	if err != nil {
		t.Fatal(err)
	}
	path, err := db.ProposePathTx(ctx, tx, db.PathInput{
		TaskID:    taskID,
		Title:     "Admin panel takeover",
		Rationale: "default credentials enable config change",
		Steps: []db.PathStepInput{
			{Kind: "fact", RefID: f1, Edge: "enables", Precondition: "panel reachable", Postcondition: "login page"},
			{Kind: "fact", RefID: f2, Edge: "escalates", Precondition: "login page", Postcondition: "admin session"},
		},
	})
	if err != nil {
		tx.Rollback()
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if _, err := pg.UpsertKnowledge(ctx, db.KnowledgeItem{
		Kind: "asset_fact", Title: "admin panel uses default creds", Body: "observed on the lab target",
		ScopeEngagement: &engID, Confidence: 80, SourceTaskID: &taskID,
	}); err != nil {
		t.Fatal(err)
	}

	// /swarm: roster + counts + events.
	taskPath := "/api/tasks/" + strconv.FormatInt(taskID, 10)
	pathIDStr := strconv.FormatInt(path.ID, 10)
	code, out = do("GET", taskPath+"/swarm", nil)
	if code != 200 {
		t.Fatalf("swarm: %d (%v)", code, out)
	}
	agents, _ := out["agents"].([]any)
	if len(agents) < 2 {
		t.Fatalf("swarm agents: want >=2, got %v", out["agents"])
	}
	first, _ := agents[0].(map[string]any)
	for _, k := range []string{"id", "slot", "name", "role", "state", "intents_done", "findings"} {
		if _, ok := first[k]; !ok {
			t.Fatalf("agent row missing %s: %v", k, first)
		}
	}
	events, _ := out["events"].([]any)
	if len(events) != 1 {
		t.Fatalf("swarm events: want 1, got %v", events)
	}
	if _, ok := out["intents_by_state"].(map[string]any); !ok {
		t.Fatalf("swarm intents_by_state missing: %v", out)
	}
	if _, ok := out["finding_confidence"].(map[string]any); !ok {
		t.Fatalf("swarm finding_confidence missing: %v", out)
	}

	// /paths: the proposed path is listed; detail returns path + steps.
	code, list := doList(taskPath + "/paths")
	if code != 200 {
		t.Fatalf("paths: %d (%v)", code, list)
	}
	if len(list) != 1 {
		t.Fatalf("paths: want 1, got %v", list)
	}
	code, out = do("GET", taskPath+"/paths/"+pathIDStr, nil)
	if code != 200 {
		t.Fatalf("path detail: %d (%v)", code, out)
	}
	steps, _ := out["steps"].([]any)
	if len(steps) != 2 {
		t.Fatalf("path steps: want 2, got %v", out)
	}

	// /knowledge: scoped retrieval + q filter.
	code, list = doList(taskPath + "/knowledge")
	if code != 200 || len(list) != 1 {
		t.Fatalf("knowledge: %d %v", code, list)
	}
	item, _ := list[0].(map[string]any)
	if item["title"] != "admin panel uses default creds" || item["confidence"].(float64) != 80 {
		t.Fatalf("knowledge item: %v", item)
	}
	code, list = doList(taskPath + "/knowledge?q=nomatch-xyz")
	if code != 200 || len(list) != 0 {
		t.Fatalf("knowledge q filter: %d %v", code, list)
	}

	// /progress: rollup shape.
	code, out = do("GET", taskPath+"/progress", nil)
	if code != 200 {
		t.Fatalf("progress: %d (%v)", code, out)
	}
	paths, _ := out["paths"].(map[string]any)
	if paths["proposed"].(float64) != 1 {
		t.Fatalf("progress paths: %v", out["paths"])
	}
	knowledge, _ := out["knowledge"].(map[string]any)
	if knowledge["asset_fact"].(float64) != 1 {
		t.Fatalf("progress knowledge: %v", out["knowledge"])
	}
	if out["checkpointed_intents"].(float64) != 1 {
		t.Fatalf("progress checkpointed_intents: %v", out)
	}

	// Human verdicts: verify step 1 → validating; refute step 2 → refuted and
	// hidden from the default list.
	code, out = do("POST", taskPath+"/paths/"+pathIDStr+"/steps/1/validate", map[string]string{"note": "confirmed manually"})
	if code != 200 {
		t.Fatalf("validate step: %d (%v)", code, out)
	}
	if out["path"].(map[string]any)["state"] != "validating" {
		t.Fatalf("after validate, state: %v", out["path"])
	}
	code, out = do("POST", taskPath+"/paths/"+pathIDStr+"/steps/2/refute", map[string]string{"note": "creds do not work"})
	if code != 200 {
		t.Fatalf("refute step: %d (%v)", code, out)
	}
	if out["path"].(map[string]any)["state"] != "refuted" {
		t.Fatalf("after refute, state: %v", out["path"])
	}
	if out["path"].(map[string]any)["score"].(float64) != 0 {
		t.Fatalf("refuted path score: %v", out["path"])
	}
	code, list = doList(taskPath + "/paths")
	if len(list) != 0 {
		t.Fatalf("default paths list should hide refuted, got %v", list)
	}
	code, list = doList(taskPath + "/paths?state=refuted")
	if len(list) != 1 {
		t.Fatalf("refuted filter: %d %v", code, list)
	}

	// Unknown path id 404s.
	if code, _ := do("GET", taskPath+"/paths/99999999", nil); code != 404 {
		t.Fatalf("unknown path: want 404, got %d", code)
	}

	// Stream: connects, replays the seeded event synchronously, then stays open
	// until the client cancels.
	streamCtx, streamCancel := context.WithCancel(context.Background())
	defer streamCancel()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", taskPath+"/swarm/stream", nil).WithContext(streamCtx)
	req.Header.Set("Authorization", "Bearer "+tok)
	streamDone := make(chan int, 1)
	go func() {
		h.ServeHTTP(rec, req)
		streamDone <- rec.Code
	}()
	// The replay happens before the handler blocks on its select loop; poll for it.
	for i := 0; i < 50 && !strings.Contains(rec.Body.String(), "data:"); i++ {
		time.Sleep(20 * time.Millisecond)
	}
	streamCancel()
	select {
	case code := <-streamDone:
		if code != 200 {
			t.Fatalf("stream: want 200, got %d", code)
		}
		body := rec.Body.String()
		if !strings.Contains(body, "agent_claim") || !strings.Contains(body, "general#1") {
			t.Fatalf("stream replay missing seeded event: %s", body)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("stream did not close after cancel")
	}
}

// TestSwarmStreamCursorCatchUp proves the ?since cursor replays every event the
// client missed: 55 seeded checkpoint events exceed the 50-event default tail,
// so a fresh subscriber cannot see them all while a cursor-based reconnect can.
// Assertions count checkpoint-kind frames only — the engine's background
// roster/claim events never use that kind, so they cannot flake the counts.
func TestSwarmStreamCursorCatchUp(t *testing.T) {
	m, err := NewManager(t.TempDir(), "")
	if err != nil {
		db.SkipPGUnavailable(t, err)
	}
	defer m.Close()
	td := t.TempDir()
	ctx, cancel := context.WithCancel(context.Background())
	s := New(ctx, m, td, td, td)
	defer stopTestServer(t, s, cancel)
	h := s.Handler()
	tok, err := signJWT(s.jwtKey)
	if err != nil {
		t.Fatalf("sign jwt: %v", err)
	}
	do := func(method, path string, body any) (int, map[string]any) {
		var r *http.Request
		if body != nil {
			b, _ := json.Marshal(body)
			r = httptest.NewRequest(method, path, strings.NewReader(string(b)))
		} else {
			r = httptest.NewRequest(method, path, nil)
		}
		r.Header.Set("Authorization", "Bearer "+tok)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, r)
		var out map[string]any
		_ = json.Unmarshal(rec.Body.Bytes(), &out)
		return rec.Code, out
	}

	code, out := do("POST", "/api/engagements", map[string]string{"name": "swarm cursor " + time.Now().Format("150405.000000000"), "description": ""})
	if code != 201 {
		t.Fatalf("create engagement: %d (%v)", code, out)
	}
	code, out = do("POST", "/api/tasks", map[string]any{
		"description":   "swarm cursor catch-up",
		"goal":          "Probe the lab target at http://127.0.0.1:9/v1 for unauthenticated data exposure; record one record as evidence and stop.",
		"engagement_id": out["id"],
	})
	if code != 201 {
		t.Fatalf("create task: %d (%v)", code, out)
	}
	taskIDStr, _ := out["id"].(string)
	taskID, _ := strconv.ParseInt(taskIDStr, 10, 64)
	taskPath := fmt.Sprintf("/api/tasks/%d", taskID)

	// Seed 55 checkpoint events, then find the id of the first one.
	const seeded = 55
	for i := 0; i < seeded; i++ {
		if _, err := m.PG().AddAgentEvent(db.AgentEvent{TaskID: taskID, AgentName: "general#1", Kind: db.EventCheckpoint,
			Detail: json.RawMessage(fmt.Sprintf(`{"n":%d}`, i))}); err != nil {
			t.Fatalf("seed event %d: %v", i, err)
		}
	}
	tail, err := m.PG().ListAgentEvents(taskID, 0, seeded+100)
	if err != nil {
		t.Fatalf("read tail: %v", err)
	}
	var firstSeeded int64
	for _, ev := range tail {
		if ev.Kind == db.EventCheckpoint {
			firstSeeded = ev.ID
			break
		}
	}
	if firstSeeded == 0 {
		t.Fatal("seeded checkpoints not found in tail")
	}

	readStream := func(url string) string {
		sctx, scancel := context.WithCancel(context.Background())
		defer scancel()
		rec := httptest.NewRecorder()
		req := httptest.NewRequest("GET", url, nil).WithContext(sctx)
		req.Header.Set("Authorization", "Bearer "+tok)
		done := make(chan int, 1)
		go func() {
			h.ServeHTTP(rec, req)
			done <- rec.Code
		}()
		for i := 0; i < 100 && !strings.Contains(rec.Body.String(), "data:"); i++ {
			time.Sleep(20 * time.Millisecond)
		}
		scancel()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Fatal("stream did not close after cancel")
		}
		if rec.Code != 200 {
			t.Fatalf("stream %s: want 200, got %d", url, rec.Code)
		}
		return rec.Body.String()
	}
	checkpointFrames := func(body string) int {
		n := 0
		for _, line := range strings.Split(body, "\n") {
			if strings.Contains(line, `"kind":"`+db.EventCheckpoint+`"`) {
				n++
			}
		}
		return n
	}

	// Fresh subscriber: the default tail (50 newest) must miss at least the
	// 5 oldest seeded checkpoints.
	if n := checkpointFrames(readStream(taskPath + "/swarm/stream")); n >= seeded {
		t.Fatalf("no-cursor tail should not contain all %d seeded checkpoints, got %d frames", seeded, n)
	}

	// Cursor reconnect: since just below the first seeded checkpoint, every
	// seeded checkpoint must be replayed (plus any engine noise in between).
	since := firstSeeded - 1
	if n := checkpointFrames(readStream(fmt.Sprintf("%s/swarm/stream?since=%d", taskPath, since))); n != seeded {
		t.Fatalf("cursor catch-up: want all %d seeded checkpoints replayed, got %d frames", seeded, n)
	}
}

func TestQuerySince(t *testing.T) {
	r := httptest.NewRequest("GET", "/x?since=42", nil)
	if got := querySince(r); got != 42 {
		t.Fatalf("numeric cursor: want 42, got %d", got)
	}
	r = httptest.NewRequest("GET", "/x?since=notanumber", nil)
	if got := querySince(r); got != 0 {
		t.Fatalf("invalid cursor: want 0, got %d", got)
	}
	r = httptest.NewRequest("GET", "/x?since=-1", nil)
	if got := querySince(r); got != 0 {
		t.Fatalf("negative cursor: want 0, got %d", got)
	}
	r = httptest.NewRequest("GET", "/x", nil)
	if got := querySince(r); got != 0 {
		t.Fatalf("missing cursor: want 0, got %d", got)
	}
}
