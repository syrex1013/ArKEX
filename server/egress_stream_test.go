package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestMullvadEgressStreamPushesOnNotify(t *testing.T) {
	s, _, _ := trafficEvidenceServer(t)
	tok, err := signJWT(s.jwtKey)
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req := httptest.NewRequest(http.MethodGet, "/api/mullvad/egress/stream", nil).WithContext(ctx)
	req.Header.Set("Authorization", "Bearer "+tok)

	frames := make(chan string, 4)
	var once sync.Once
	started := make(chan struct{})
	w := &frameRecorder{
		ResponseRecorder: httptest.NewRecorder(),
		onChunk: func(chunk string) {
			once.Do(func() { close(started) })
			for _, part := range strings.Split(chunk, "\n\n") {
				part = strings.TrimSpace(part)
				if strings.HasPrefix(part, "data: ") {
					select {
					case frames <- strings.TrimPrefix(part, "data: "):
					default:
					}
				}
			}
		},
	}

	done := make(chan struct{})
	go func() {
		s.streamMullvadEgress(w, req)
		close(done)
	}()

	// The first frame carries a live egress probe capped at 6s, so allow for a
	// slow or unreachable exit rather than assuming a fast network.
	select {
	case <-started:
	case <-time.After(12 * time.Second):
		t.Fatal("egress stream did not start")
	}

	select {
	case raw := <-frames:
		var got map[string]any
		if err := json.Unmarshal([]byte(raw), &got); err != nil {
			t.Fatalf("initial frame: %v (%s)", err, raw)
		}
		if _, ok := got["global_wg_set"]; !ok {
			t.Fatalf("initial frame missing global_wg_set: %v", got)
		}
		if _, ok := got["egress_ip"]; !ok {
			t.Fatalf("initial frame missing egress_ip: %v", got)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("no initial egress frame")
	}

	egressLive.Notify()
	select {
	case raw := <-frames:
		var got map[string]any
		if err := json.Unmarshal([]byte(raw), &got); err != nil {
			t.Fatalf("notify frame: %v (%s)", err, raw)
		}
		if _, ok := got["task_egress_blocked"]; !ok {
			t.Fatalf("notify frame missing task_egress_blocked: %v", got)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("no frame after Notify")
	}

	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("stream did not stop on cancel")
	}
}

// frameRecorder captures only the bytes written since the previous Flush.
type frameRecorder struct {
	*httptest.ResponseRecorder
	seen    int
	onChunk func(chunk string)
}

func (f *frameRecorder) Flush() {
	body := f.Body.String()
	if f.seen > len(body) {
		f.seen = 0
	}
	chunk := body[f.seen:]
	f.seen = len(body)
	if f.onChunk != nil && chunk != "" {
		f.onChunk(chunk)
	}
}
