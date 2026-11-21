package server

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestOASTHTTPIngressExemptFromJWT: a blind SSRF/DNS probe from the target under
// test cannot carry the operator's JWT, so the Host-header callback ingress must
// stay open. The JSON ingest (/api/oast/callback) stays behind auth for internal
// pollers, so an unauthenticated caller must not reach it.
func TestOASTHTTPIngressExemptFromJWT(t *testing.T) {
	s := &Server{}
	reached := false
	h := s.requireAuth(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		reached = true
	}))

	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("POST", "/api/oast/http", nil))
	if !reached {
		t.Fatal("unauthenticated POST /api/oast/http was rejected; external OAST callbacks cannot carry a JWT")
	}

	reached = false
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("POST", "/api/oast/callback", nil))
	if reached {
		t.Fatal("unauthenticated POST /api/oast/callback reached the handler; the JSON ingest must stay authenticated")
	}
}
