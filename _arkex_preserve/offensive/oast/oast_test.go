package oast

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func newTestServer() *Server {
	return New(nil, "oast.threatvector.tech")
}

func TestGeneratePayloadFormat(t *testing.T) {
	s := newTestServer()
	tag, err := s.GeneratePayload(context.Background(), "xss", "intent-1", "eng-1")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(tag, ".oast.threatvector.tech") {
		t.Fatalf("bad payload domain: %s", tag)
	}
	if !strings.HasPrefix(tag, "xss-") {
		t.Fatalf("bad payload prefix: %s", tag)
	}
}

func TestWaitForCallbackTimeout(t *testing.T) {
	s := newTestServer()
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	tag, _ := s.GeneratePayload(ctx, "test", "i1", "e1")
	_, err := s.WaitForCallback(ctx, tag, 50*time.Millisecond)
	if err == nil {
		t.Fatal("expected timeout error")
	}
}

func TestHTTPCallbackDispatch(t *testing.T) {
	s := newTestServer()
	ctx := context.Background()

	tag, err := s.GeneratePayload(ctx, "ssrf", "i2", "e2")
	if err != nil {
		t.Fatal(err)
	}

	// Register waiter
	done := make(chan *Callback, 1)
	go func() {
		waitCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
		defer cancel()
		cb, _ := s.WaitForCallback(waitCtx, tag, 2*time.Second)
		done <- cb
	}()

	time.Sleep(5 * time.Millisecond)

	// Simulate HTTP callback via the real handler
	req := httptest.NewRequest("GET", "/ping", nil)
	req.Host = tag
	rec := httptest.NewRecorder()
	s.HandleHTTPCallback(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("want 200 got %d", rec.Code)
	}

	select {
	case cb := <-done:
		if cb == nil {
			t.Fatal("nil callback received")
		}
		if cb.PayloadTag != tag {
			t.Fatalf("wrong tag: %s", cb.PayloadTag)
		}
		if cb.Protocol != "http" {
			t.Fatalf("wrong protocol: %s", cb.Protocol)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("callback dispatch timeout")
	}
}
