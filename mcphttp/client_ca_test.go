package mcphttp

import (
	"crypto/x509"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

// A recording egress proxy re-signs target certificates, so an MCP transport
// without its CA fails the handshake ("connection closed" in the logs). The CA
// file must be honored, and an absent CA must keep the server unreachable.
func TestProxyTransportTrustsEgressCA(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer srv.Close()

	ca := filepath.Join(t.TempDir(), "ca.pem")
	der := srv.Certificate().Raw
	if err := os.WriteFile(ca, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o600); err != nil {
		t.Fatal(err)
	}
	if !selfSigned(t, der) {
		t.Fatalf("fixture is not self-signed")
	}

	get := func(proxyCA string) error {
		tr, err := proxyTransport("", proxyCA, false)
		if err != nil {
			t.Fatal(err)
		}
		hc := &http.Client{Transport: tr}
		resp, err := hc.Get(srv.URL)
		if err != nil {
			return err
		}
		return resp.Body.Close()
	}
	if err := get(""); err == nil {
		t.Fatal("untrusted self-signed server was accepted")
	}
	if err := get(ca); err != nil {
		t.Fatalf("egress CA not applied: %v", err)
	}
}

func selfSigned(t *testing.T, der []byte) bool {
	t.Helper()
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	return cert.CheckSignatureFrom(cert) == nil
}
