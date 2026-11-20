package server

import "testing"

// egressStamp is the classification stamped onto every recorded exchange:
// tunneled (WireGuard kernel or healthy userspace session), proxied:<host>
// (global egress proxy), direct (no VPN, no proxy — the leak-risk state).
func TestEgressStampClassifies(t *testing.T) {
	m := &Manager{}
	if got := m.egressStamp(); got != "direct" {
		t.Fatalf("no vpn, no proxy: %q, want direct", got)
	}
	m.globalProxy = "http://user:secret@egress.example.com:8080"
	if got := m.egressStamp(); got != "proxied:egress.example.com:8080" {
		t.Fatalf("global proxy: %q, want proxied host", got)
	}
	// Credentials must never leak into the stamp: only the host appears.
	if got := (&Manager{globalProxy: "socks5://bob:hunter2@10.0.0.9:1080"}).egressStamp(); got != "proxied:10.0.0.9:1080" {
		t.Fatalf("socks proxy: %q, want proxied:10.0.0.9:1080", got)
	}
}
