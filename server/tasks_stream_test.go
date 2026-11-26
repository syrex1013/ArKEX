package server

import (
	"bufio"
	"context"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Autumn-27/artex/agent"
	"github.com/Autumn-27/artex/db"
)

func newTasksStreamServer(t *testing.T) *Server {
	t.Helper()
	dsn, _, err := db.DSN()
	if err != nil {
		db.SkipPGUnavailable(t, err)
	}
	pg, err := db.Open(dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { pg.Close() })
	return &Server{
		ctx:    context.Background(),
		m:      &Manager{pg: pg, dir: t.TempDir(), tasks: map[string]*Task{}},
		engine: &Engine{},
		jwtKey: []byte("0123456789abcdef0123456789abcdef"),
	}
}

// firstStreamFrame connects to the handler and returns the first "data:" frame.
func firstStreamFrame(t *testing.T, ts *httptest.Server, token string) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, "GET", ts.URL, nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := ts.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("stream status=%d", resp.StatusCode)
	}
	scanner := bufio.NewScanner(resp.Body)
	for scanner.Scan() {
		if line := scanner.Text(); strings.HasPrefix(line, "data: ") {
			cancel()
			return line
		}
	}
	if err := scanner.Err(); err != nil && ctx.Err() == nil {
		t.Fatalf("stream read: %v", err)
	}
	return ""
}

func TestStreamTasksRunningCount(t *testing.T) {
	s := newTasksStreamServer(t)
	token, err := signJWT(s.jwtKey)
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(http.HandlerFunc(s.streamTasks))
	defer ts.Close()

	if got := firstStreamFrame(t, ts, token); got != `data: {"running":0}` {
		t.Fatalf("empty board frame=%q", got)
	}

	created, err := s.m.CreateTask("tasks stream test", "stream fixture", nil, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	id, convErr := strconv.ParseInt(created.ID, 10, 64)
	if convErr != nil {
		t.Fatal(convErr)
	}
	t.Cleanup(func() { _, _ = s.m.pg.Exec(`DELETE FROM tasks WHERE id=$1`, id) })

	// A freshly created task is not running.
	if got := firstStreamFrame(t, ts, token); got != `data: {"running":0}` {
		t.Fatalf("created task frame=%q", got)
	}

	// Engine loops started with a resolvable roster → running.
	s.engine.resolve = func(*Task) (*agent.Planner, *agent.Worker) {
		return &agent.Planner{}, &agent.Worker{}
	}
	s.engine.started.Store(created.ID, true)
	if got := firstStreamFrame(t, ts, token); got != `data: {"running":1}` {
		t.Fatalf("running task frame=%q", got)
	}

	// User-paused drops it out of the running count.
	s.engine.Pause(created.ID, nil)
	if got := firstStreamFrame(t, ts, token); got != `data: {"running":0}` {
		t.Fatalf("paused task frame=%q", got)
	}
}

func TestStreamTasksRejectsMissingToken(t *testing.T) {
	s := newTasksStreamServer(t)
	w := httptest.NewRecorder()
	s.streamTasks(w, httptest.NewRequest("GET", "/api/tasks/stream", nil))
	if w.Code != 401 {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
}
