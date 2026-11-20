package wgproxy

import (
	"context"
	"encoding/base64"
	"strings"
	"testing"
)

func TestGenerateKey(t *testing.T) {
	priv, pub, err := GenerateKey()
	if err != nil {
		t.Fatalf("GenerateKey failed: %v", err)
	}
	for name, k := range map[string]string{"private": priv, "public": pub} {
		raw, err := base64.StdEncoding.DecodeString(k)
		if err != nil {
			t.Fatalf("%s key not base64: %v", name, err)
		}
		if len(raw) != 32 {
			t.Fatalf("%s key = %d bytes, want 32", name, len(raw))
		}
	}
	// Private and public must differ.
	if priv == pub {
		t.Fatal("private and public key identical")
	}
}

func TestHexBytes(t *testing.T) {
	if got := hexBytes([]byte{0x00, 0x0f, 0xff}); got != "000fff" {
		t.Fatalf("hexBytes = %q, want 000fff", got)
	}
	if got := hexBytes(nil); got != "" {
		t.Fatalf("hexBytes(nil) = %q, want \"\"", got)
	}
}

func TestStartValidation(t *testing.T) {
	priv, pub, err := GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name string
		cfg  Config
		want string
	}{
		{"invalid base64 private key", Config{PrivateKey: "!!!", PeerKey: pub, Endpoint: "192.0.2.10:51820"}, "decode private key"},
		{"invalid base64 peer key", Config{PrivateKey: priv, PeerKey: "!!!", Endpoint: "192.0.2.10:51820"}, "decode peer key"},
		{"bad endpoint", Config{PrivateKey: priv, PeerKey: pub, Endpoint: "no-host-port"}, "parse endpoint"},
		{"ipv6 endpoint", Config{PrivateKey: priv, PeerKey: pub, Endpoint: "[::1]:51820"}, "endpoint must be IPv4"},
	}
	for _, c := range cases {
		_, err := Start(context.Background(), c.cfg)
		if err == nil {
			t.Fatalf("%s: Start unexpectedly succeeded", c.name)
		}
		if !strings.Contains(err.Error(), c.want) {
			t.Fatalf("%s: error %q, want substring %q", c.name, err.Error(), c.want)
		}
	}
}

func TestProxyURLHelper(t *testing.T) {
	up := &Session{up: true, port: 18101}
	if got := up.ProxyURL(); got != "http://127.0.0.1:18101" {
		t.Fatalf("ProxyURL = %q, want http://127.0.0.1:18101", got)
	}

	down := &Session{}
	if got := down.ProxyURL(); got != "" {
		t.Fatalf("stopped ProxyURL = %q, want empty", got)
	}
}

func TestStatus(t *testing.T) {
	down := &Session{}
	st := down.Status()
	if st["up"] != false || st["mode"] != "" {
		t.Fatalf("stopped status = %v", st)
	}
	// A Session built by hand has no device; the "up" contract with a real
	// device is covered by TestEffectiveTaskProxyURL (server pkg).
}

func TestStopIdempotent(t *testing.T) {
	s := &Session{}
	if err := s.Stop(); err != nil {
		t.Fatalf("Stop on empty session: %v", err)
	}
	if err := s.Stop(); err != nil {
		t.Fatalf("second Stop: %v", err)
	}
}
