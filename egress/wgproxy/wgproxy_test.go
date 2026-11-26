package wgproxy

import (
	"context"
	"encoding/base64"
	"net"
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

func TestParseForwardRequest(t *testing.T) {
	cases := []struct {
		name       string
		head       string
		wantTarget string
		wantHead   string
		wantOK     bool
	}{
		{
			name:       "absolute-form GET keeps existing Host header",
			head:       "GET http://www.plus.pl/ HTTP/1.1\r\nHost: www.plus.pl\r\nUser-Agent: x\r\n\r\n",
			wantTarget: "www.plus.pl:80",
			wantHead:   "GET / HTTP/1.1\r\nHost: www.plus.pl\r\nUser-Agent: x\r\n\r\n",
			wantOK:     true,
		},
		{
			name:       "missing Host header is synthesized",
			head:       "GET http://www.plus.pl/blad-polaczenia_W500 HTTP/1.1\r\n\r\n",
			wantTarget: "www.plus.pl:80",
			wantHead:   "GET /blad-polaczenia_W500 HTTP/1.1\r\nHost: www.plus.pl\r\n\r\n",
			wantOK:     true,
		},
		{
			name:       "port and query pass through",
			head:       "POST http://dmc.plus.pl:8080/DMC/services?a=1 HTTP/1.0\r\nContent-Length: 0\r\n\r\n",
			wantTarget: "dmc.plus.pl:8080",
			wantHead:   "POST /DMC/services?a=1 HTTP/1.0\r\nHost: dmc.plus.pl:8080\r\nContent-Length: 0\r\n\r\n",
			wantOK:     true,
		},
		{
			name:       "bare authority becomes / on port 80",
			head:       "HEAD http://ustaw.plus.pl HTTP/1.1\r\nHost: ustaw.plus.pl\r\n\r\n",
			wantTarget: "ustaw.plus.pl:80",
			wantHead:   "HEAD / HTTP/1.1\r\nHost: ustaw.plus.pl\r\n\r\n",
			wantOK:     true,
		},
		{
			name:   "absolute-form https is refused",
			head:   "GET https://www.plus.pl/ HTTP/1.1\r\nHost: www.plus.pl\r\n\r\n",
			wantOK: false,
		},
		{
			name:   "origin-form is refused",
			head:   "GET / HTTP/1.1\r\nHost: www.plus.pl\r\n\r\n",
			wantOK: false,
		},
		{
			name:   "two-field request line is refused",
			head:   "GET http://www.plus.pl/\r\n\r\n",
			wantOK: false,
		},
		{
			name:       "body beyond the terminator is preserved",
			head:       "POST http://www.plus.pl/x HTTP/1.1\r\nHost: www.plus.pl\r\n\r\nBODY",
			wantTarget: "www.plus.pl:80",
			wantHead:   "POST /x HTTP/1.1\r\nHost: www.plus.pl\r\n\r\nBODY",
			wantOK:     true,
		},
	}
	for _, c := range cases {
		target, out, ok := parseForwardRequest([]byte(c.head))
		if ok != c.wantOK {
			t.Fatalf("%s: ok = %v, want %v", c.name, ok, c.wantOK)
		}
		if !c.wantOK {
			continue
		}
		if target != c.wantTarget {
			t.Fatalf("%s: target = %q, want %q", c.name, target, c.wantTarget)
		}
		if string(out) != c.wantHead {
			t.Fatalf("%s: rewritten head = %q, want %q", c.name, string(out), c.wantHead)
		}
	}
}

func TestCompleteHeadAccumulates(t *testing.T) {
	client, server := net.Pipe()
	defer client.Close()
	defer server.Close()
	go func() {
		client.Write([]byte("GET http://x/ HTTP/1.1\r\nHost: x\r\n"))
		client.Write([]byte("\r\nbody-start"))
		client.Close()
	}()
	head, ok := completeHead(server, []byte("GET http://x/ HTTP/1.1\r\n"))
	if !ok {
		t.Fatal("completeHead did not find the terminator")
	}
	if !strings.Contains(string(head), "body-start") {
		t.Fatalf("head = %q, want body bytes included", string(head))
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
