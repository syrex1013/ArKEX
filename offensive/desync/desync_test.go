package desync

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

// normalServer behaves like a well-behaved HTTP/1.1 server — no desync.
func normalServer() *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
}

func TestProbeKindConstants(t *testing.T) {
	kinds := []ProbeKind{ProbeCLTE, ProbeTECL, ProbeH2CL, ProbeH2TE, ProbeH2SP}
	seen := map[ProbeKind]bool{}
	for _, k := range kinds {
		if seen[k] {
			t.Fatalf("duplicate ProbeKind: %s", k)
		}
		seen[k] = true
		if k == "" {
			t.Fatal("empty ProbeKind")
		}
	}
}

func TestProbeAllReturnsResults(t *testing.T) {
	srv := normalServer()
	defer srv.Close()

	e := New()
	results, err := e.ProbeAll(context.Background(), srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) == 0 {
		t.Fatal("expected at least one probe result")
	}
	for _, r := range results {
		if r.Kind == "" {
			t.Fatal("empty kind in result")
		}
		if r.Target == "" {
			t.Fatal("empty target in result")
		}
	}
}

func TestProbeSpecificKind(t *testing.T) {
	srv := normalServer()
	defer srv.Close()

	e := New()
	r, err := e.Probe(context.Background(), srv.URL, ProbeCLTE)
	if err != nil {
		t.Fatal(err)
	}
	if r.Kind != ProbeCLTE {
		t.Fatalf("want KindCLTE got %s", r.Kind)
	}
}

func TestProbeUnknownKind(t *testing.T) {
	e := New()
	_, err := e.Probe(context.Background(), "http://localhost:9", ProbeKind("invalid"))
	if err == nil {
		t.Fatal("expected error for unknown probe kind")
	}
}
