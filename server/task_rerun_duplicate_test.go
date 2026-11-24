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
