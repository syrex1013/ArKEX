package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// stubServer builds a Server with nil db/engine/gov — only tests that don't
// hit the database are run here (health, version, goal gate).
func newServer(t *testing.T) *Server {
	t.Helper()
	s := &Server{}
	s.mux = http.NewServeMux()
	s.registerRoutes()
	return s
}

func TestHealthEndpoint(t *testing.T) {
	s := newServer(t)
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, httptest.NewRequest("GET", "/api/health", nil))
	// Without a real DB the handler returns 503/degraded — that's correct.
	var body map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body["status"] == nil {
		t.Fatal("missing status field")
	}
}

func TestVersionEndpoint(t *testing.T) {
	s := newServer(t)
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, httptest.NewRequest("GET", "/api/version", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("want 200 got %d", rec.Code)
	}
}

func TestGoalAmbiguityGate(t *testing.T) {
	s := newServer(t)

	tests := []struct {
		goal string
		want int
	}{
		{"short", http.StatusBadRequest},
		{string(bytes.Repeat([]byte("x"), 79)), http.StatusBadRequest},
		// 80+ char goal passes the gate; db-dependent path not tested here
	}

	for _, tc := range tests {
		body, _ := json.Marshal(map[string]any{
			"company_id": "c1",
			"name":       "test",
			"goal":       tc.goal,
			"scope_pins": []string{"https://example.com"},
		})
		req := httptest.NewRequest("POST", "/api/engagements", bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		s.ServeHTTP(rec, req)
		if rec.Code != tc.want {
			t.Errorf("goal len %d: want %d got %d body=%s", len(tc.goal), tc.want, rec.Code, rec.Body.String())
		}
	}
}

func TestCreateEngagementBadContentType(t *testing.T) {
	s := newServer(t)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/engagements", bytes.NewBufferString("{}"))
	// No Content-Type header
	s.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnsupportedMediaType {
		t.Fatalf("want 415 got %d", rec.Code)
	}
}

func TestNotFound(t *testing.T) {
	s := newServer(t)
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, httptest.NewRequest("GET", "/api/does-not-exist", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("want 404 got %d", rec.Code)
	}
}
