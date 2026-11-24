package server

import (
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Autumn-27/artex/egress/wgproxy"
)

// testWGConf returns a structurally valid WireGuard config whose endpoint is
// TEST-NET-1 (RFC 5737): never reachable, so the first handshake always fails.
func testWGConf(t *testing.T) (conf, priv string) {
	t.Helper()
	priv, _, err := wgproxy.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	return fmt.Sprintf(`[Interface]
PrivateKey = %s
Address = 10.66.0.2/32
DNS = 10.64.0.1

[Peer]
PublicKey = %s
Endpoint = 192.0.2.10:51820
AllowedIPs = 0.0.0.0/0
`, priv, priv), priv
}

// The global WireGuard config route: invalid configs are rejected outright; a
// stored config whose tunnel cannot come up keeps egress fail-closed (blocked,
// never direct); clearing it restores the proxy/direct resolution.
func TestGlobalWGConfigFailClosed(t *testing.T) {
	s, _, request := trafficEvidenceServer(t)
	pg := s.m.PG()
	t.Cleanup(func() {
		// Clear through the API so the in-memory session + cached config die too.
		_ = s.m.SetGlobalWGConfig("")
		pg.Exec(`DELETE FROM settings WHERE key IN ('global_wg_config','global_wg_proxy_port')`)
	})

	stored := func(key string) string {
		t.Helper()
		v, _, _ := pg.GetSetting(key)
		return v
	}

	var resp *httptest.ResponseRecorder
	// Invalid config: 400, nothing stored.
	resp = request("PUT", "/api/settings", `{"global_wg_config":"[Interface]\nnonsense\n"}`)
	if resp.Code != 400 {
		t.Fatalf("PUT invalid config: HTTP %d %s", resp.Code, resp.Body.String())
	}
	if stored("global_wg_config") != "" {
		t.Fatal("invalid config must not be stored")
	}

	// Valid config, dead endpoint: 400 with the handshake error, but the config
	// IS stored — the watchdog keeps retrying while egress stays fail-closed.
	conf, priv := testWGConf(t)
	resp = request("PUT", "/api/settings", `{"global_wg_config":`+jsonLine(t, conf)+`}`)
	if resp.Code != 400 {
		t.Fatalf("PUT dead-endpoint config: HTTP %d %s", resp.Code, resp.Body.String())
	}
	if !strings.Contains(resp.Body.String(), "handshake failed") {
		t.Fatalf("want handshake failure, got %s", resp.Body.String())
	}
	if stored("global_wg_config") == "" {
		t.Fatal("config must stay stored despite the failed first start")
	}
	if got := s.m.EffectiveTaskProxyURL(); got != blockedProxyURL {
		t.Fatalf("configured wg with no healthy session: egress %q, want fail-closed %q", got, blockedProxyURL)
	}

	// GET: masked + presence flag, and the private key never leaks.
	resp = request("GET", "/api/settings", "")
	var got map[string]any
	if err := json.Unmarshal(resp.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got["global_wg_config"] != "__masked__" || got["global_wg_config_set"] != true {
		t.Fatalf("wg config not masked with a presence flag: %v", got["global_wg_config"])
	}
	if strings.Contains(resp.Body.String(), priv) {
		t.Fatal("wg private key leaked in clear")
	}

	// __masked__ keeps the stored config; "" clears and unblocks egress.
	resp = request("PUT", "/api/settings", `{"global_wg_config":"__masked__"}`)
	if resp.Code != 200 {
		t.Fatalf("PUT masked: HTTP %d", resp.Code)
	}
	if stored("global_wg_config") == "" {
		t.Fatal("__masked__ must keep the stored config")
	}
	resp = request("PUT", "/api/settings", `{"global_wg_config":""}`)
	if resp.Code != 200 {
		t.Fatalf("PUT clear: HTTP %d", resp.Code)
	}
	if stored("global_wg_config") != "" {
		t.Fatal("empty config must clear the stored value")
	}
	if got := s.m.EffectiveTaskProxyURL(); got == blockedProxyURL {
		t.Fatal("cleared config must not leave egress blocked")
	}
}

// jsonLine marshals s as a JSON string literal for embedding in a request body.
func jsonLine(t *testing.T, s string) string {
	t.Helper()
	raw, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

// The watchdog path picks up a config stored directly in PG (the restart case)
// without any API action: one synchronous maintainGlobalWG tick keeps egress
// fail-closed while the tunnel cannot come up.
func TestGlobalWGWatchdogPicksUpStoredConfig(t *testing.T) {
	s, _, _ := trafficEvidenceServer(t)
	pg := s.m.PG()
	t.Cleanup(func() {
		_ = s.m.SetGlobalWGConfig("")
		pg.Exec(`DELETE FROM settings WHERE key IN ('global_wg_config')`)
	})
	conf, _ := testWGConf(t)
	if err := pg.SetSetting("global_wg_config", conf); err != nil {
		t.Fatal(err)
	}
	s.m.maintainGlobalWG() // dead endpoint: start fails, egress must stay blocked
	if got := s.m.EffectiveTaskProxyURL(); got != blockedProxyURL {
		t.Fatalf("stored wg config with dead endpoint: egress %q, want fail-closed %q", got, blockedProxyURL)
	}
}
