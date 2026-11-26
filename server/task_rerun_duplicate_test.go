package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Autumn-27/artex/db"
)

func TestRerunAndDuplicateTaskRoutes(t *testing.T) {
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
	token, err := signJWT(s.jwtKey)
	if err != nil {
		t.Fatalf("sign jwt: %v", err)
	}

	task, err := m.CreateTaskWithOptions("dup source", "find bugs", db.TaskCreateOptions{Name: "source"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	goalID, err := task.Store.AddGoal(map[string]any{"text": "cover the app"}, "planner")
	if err != nil {
		t.Fatalf("add goal: %v", err)
	}
	if err := task.Store.SetNodeState(goalID, "met"); err != nil {
		t.Fatalf("met goal: %v", err)
	}
	if err := m.SetTaskStatus(task.ID, "done"); err != nil {
		t.Fatalf("set done: %v", err)
	}

	auth := func(method, path, body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+token)
		if body != "" {
			req.Header.Set("Content-Type", "application/json")
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec
	}

	resumeWhileDone := auth(http.MethodPost, "/api/tasks/"+task.ID+"/control", `{"action":"resume"}`)
	if resumeWhileDone.Code != http.StatusConflict {
		t.Fatalf("resume on done want 409, got %d %s", resumeWhileDone.Code, resumeWhileDone.Body.String())
	}

	rerun := auth(http.MethodPost, "/api/tasks/"+task.ID+"/rerun", "")
	if rerun.Code != http.StatusOK {
		t.Fatalf("rerun: status=%d body=%s", rerun.Code, rerun.Body.String())
	}
	var rerunOut taskControlResult
	if err := json.Unmarshal(rerun.Body.Bytes(), &rerunOut); err != nil {
		t.Fatalf("rerun json: %v", err)
	}
	if rerunOut.Status != "running" && rerunOut.Status != "queued" {
		t.Fatalf("rerun status=%q", rerunOut.Status)
	}
	open, err := task.Store.HasOpenGoal()
	if err != nil || !open {
		t.Fatalf("rerun must reopen met goals: open=%v err=%v", open, err)
	}
	goal, err := task.Store.GetNode(goalID)
	if err != nil || goal == nil || goal.State != "open" {
		t.Fatalf("goal after rerun: %+v err=%v", goal, err)
	}
	// Must not settle on the first goalless wake: open goals keep the planner path.
	if st := task.lifecycleSnapshot().Status; st == "done" {
		t.Fatalf("rerun flipped straight back to done")
	}

	dup := auth(http.MethodPost, "/api/tasks/"+task.ID+"/duplicate", "")
	if dup.Code != http.StatusCreated {
		t.Fatalf("duplicate: status=%d body=%s", dup.Code, dup.Body.String())
	}
	var created TaskDTO
	if err := json.Unmarshal(dup.Body.Bytes(), &created); err != nil {
		t.Fatalf("dup json: %v", err)
	}
	if created.ID == "" || created.ID == task.ID {
		t.Fatalf("expected new task id, got %+v", created)
	}
	if created.Name != "source (copy)" {
		t.Fatalf("name=%q", created.Name)
	}
	if created.Description != "dup source" || created.Goal != "find bugs" {
		t.Fatalf("brief mismatch: %+v", created)
	}

	liveReject := auth(http.MethodPost, "/api/tasks/"+task.ID+"/rerun", "")
	if liveReject.Code != http.StatusConflict {
		t.Fatalf("rerun live want 409, got %d %s", liveReject.Code, liveReject.Body.String())
	}
}

func TestPrepareTaskRerunGoallessReopensTerminalIntents(t *testing.T) {
	m, err := NewManager(t.TempDir(), "")
	if err != nil {
		db.SkipPGUnavailable(t, err)
	}
	defer m.Close()

	task, err := m.CreateTask("goalless rerun", "one shot", nil, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	intentID, err := task.Store.AddIntent(map[string]any{"summary": "probe once"}, 8, nil, "human")
	if err != nil {
		t.Fatal(err)
	}
	if err := task.Store.SetIntentState(intentID, "done"); err != nil {
		t.Fatal(err)
	}
	if err := m.SetTaskStatus(task.ID, "done"); err != nil {
		t.Fatal(err)
	}

	if err := prepareTaskRerun(task); err != nil {
		t.Fatal(err)
	}
	active, err := task.Store.HasActiveIntent()
	if err != nil || !active {
		t.Fatalf("goalless rerun must reopen settled intents: active=%v err=%v", active, err)
	}
	node, err := task.Store.GetNode(intentID)
	if err != nil || node == nil || node.State != "open" {
		t.Fatalf("intent after prepare: %+v err=%v", node, err)
	}
	notifyTaskRerun(task)
	triggers := task.drainTriggers()
	if len(triggers) != 1 || triggers[0].Kind != "hint" || triggers[0].HintOrigin != "human" {
		t.Fatalf("expected operator rerun hint trigger, got %+v", triggers)
	}
}
