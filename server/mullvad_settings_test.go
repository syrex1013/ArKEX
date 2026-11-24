package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Autumn-27/artex/egress/wgproxy"
)

// The five Mullvad/sliver settings round-trip through PUT /api/settings; the
// sealed private key is never echoed in clear and the "__masked__" placeholder
// keeps the stored value.
func TestMullvadSettingsAPI(t *testing.T) {
	s, _, request := trafficEvidenceServer(t)
	pg := s.m.PG()
	t.Cleanup(func() {
		pg.Exec(`DELETE FROM settings WHERE key IN ('mullvad_proxy_enabled','mullvad_proxy_port','mullvad_private_key','mullvad_relay','sliver_config_path')`)
	})
	key := "wgpriv-0013-secret"

	setting := func(key string) string {
		t.Helper()
		v, ok, _ := pg.GetSetting(key)
		if !ok {
			return ""
		}
		return v
	}

	// Store all five.
	if resp := request("PUT", "/api/settings", `{
		"mullvad_proxy_enabled": true,
		"mullvad_proxy_port": 18102,
		"mullvad_private_key": "`+key+`",
		"mullvad_relay": "de-ber-wg-001",
		"sliver_config_path": "/opt/arkex.operator.conf"
	}`); resp.Code != 200 {
		t.Fatalf("PUT mullvad settings: HTTP %d %s", resp.Code, resp.Body.String())
	}
	if setting("mullvad_proxy_enabled") != "true" {
		t.Fatalf("mullvad_proxy_enabled not persisted: %q", setting("mullvad_proxy_enabled"))
	}
	if setting("mullvad_proxy_port") != "18102" {
		t.Fatalf("mullvad_proxy_port not persisted: %q", setting("mullvad_proxy_port"))
	}
	if setting("mullvad_private_key") != key {
		t.Fatal("mullvad_private_key not persisted")
	}
	if setting("mullvad_relay") != "de-ber-wg-001" {
		t.Fatalf("mullvad_relay not persisted: %q", setting("mullvad_relay"))
	}
	if setting("sliver_config_path") != "/opt/arkex.operator.conf" {
		t.Fatalf("sliver_config_path not persisted: %q", setting("sliver_config_path"))
	}

	// GET: the key is masked, presence flag on, plain fields echoed.
	resp := request("GET", "/api/settings", "")
	if resp.Code != 200 {
		t.Fatalf("GET /api/settings: HTTP %d", resp.Code)
	}
	var got map[string]any
	if err := json.Unmarshal(resp.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got["mullvad_private_key"] != "__masked__" || got["mullvad_private_key_set"] != true {
		t.Fatalf("mullvad key not masked with a presence flag: %v", got["mullvad_private_key"])
	}
	if strings.Contains(resp.Body.String(), key) {
		t.Fatal("mullvad private key leaked in clear")
	}
	if got["mullvad_proxy_enabled"] != true || got["mullvad_relay"] != "de-ber-wg-001" {
		t.Fatalf("plain mullvad fields not echoed: %v", got)
	}

	// "__masked__" keeps the stored secret; "" clears.
	if resp := request("PUT", "/api/settings", `{"mullvad_private_key":"__masked__"}`); resp.Code != 200 {
		t.Fatalf("PUT masked: HTTP %d", resp.Code)
	}
	if setting("mullvad_private_key") != key {
		t.Fatal("a masked placeholder replaced the stored mullvad key")
	}
	if resp := request("PUT", "/api/settings", `{"mullvad_private_key":""}`); resp.Code != 200 {
		t.Fatalf("PUT clear: HTTP %d", resp.Code)
	}
	if setting("mullvad_private_key") != "" {
		t.Fatal(`"" did not clear the mullvad key`)
	}

	// Port validation: 0 is rejected.
	if resp := request("PUT", "/api/settings", `{"mullvad_proxy_port":0}`); resp.Code != 400 {
		t.Fatalf("port 0 accepted: HTTP %d", resp.Code)
	}
}

// EffectiveTaskProxyURL / ProxyAddr: the healthy wg session wins; an enabled
// Mullvad without a healthy session blocks ALL task egress (fail closed);
// otherwise ProxyAddr's capture/global resolution; global_proxy's stored
// value is untouched.
//
// The unhealthy session is a real userspace netstack session bound to an
// unroutable endpoint — the WireGuard handshake never completes (that needs a
// registered Mullvad key + relay), which is exactly the not-healthy state the
// fail-closed path must cover. A live handshake test is integration-only.
func TestEffectiveTaskProxyURL(t *testing.T) {
	m := &Manager{}
	if got := m.EffectiveTaskProxyURL(); got != "" {
		t.Fatalf("empty manager = %q, want \"\"", got)
	}
	m.globalProxy = "http://gp.example:3128"
	if got := m.EffectiveTaskProxyURL(); got != "http://gp.example:3128" {
		t.Fatalf("global fallback = %q", got)
	}

	priv, pub, err := wgproxy.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	deadSess, err := wgproxy.Start(context.Background(), wgproxy.Config{
		PrivateKey: priv,
		PeerKey:    pub,
		Endpoint:   "192.0.2.10:51820", // TEST-NET-1: never reachable, no packets leave
	})
	if err != nil {
		t.Fatalf("start netstack session: %v", err)
	}
	t.Cleanup(func() { _ = deadSess.Stop() })

	// A session without a verified handshake is NOT accepted: still the
	// global proxy (Mullvad not enabled here), and global_proxy is untouched.
	m.wgSession = deadSess
	if got := m.EffectiveTaskProxyURL(); got != "http://gp.example:3128" {
		t.Fatalf("unverified session = %q, want the global fallback", got)
	}
	if m.GlobalProxy() != "http://gp.example:3128" {
		t.Fatalf("global_proxy value was mutated by the wg session: %q", m.GlobalProxy())
	}

	// Healthy session wins.
	m.wgSession = wgproxy.NewHealthyTestSession(18101)
	if got := m.EffectiveTaskProxyURL(); got != "http://127.0.0.1:18101" {
		t.Fatalf("healthy wg session = %q, want the local proxy URL", got)
	}
	if m.ProxyAddr() != "http://127.0.0.1:18101" {
		t.Fatalf("ProxyAddr must share the fail-closed resolution, got %q", m.ProxyAddr())
	}

	// Mullvad enabled + session stopped: ALL task egress blocked, never direct.
	if err := m.wgSession.Stop(); err != nil {
		t.Fatal(err)
	}
	m.wgSession = nil
	// Enabled requires a data source; skipped without a DSN.
	skipWithoutDSN(t)
	mEnabled, err := NewManager(t.TempDir(), "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { mEnabled.SetMullvadProxyEnabled(false) })
	mEnabled.globalProxy = "http://gp.example:3128"
	if err := mEnabled.SetMullvadProxyEnabled(true); err != nil {
		t.Fatal(err)
	}
	if got := mEnabled.EffectiveTaskProxyURL(); got != blockedProxyURL {
		t.Fatalf("enabled without session = %q, want %q (fail closed)", got, blockedProxyURL)
	}
	if got := mEnabled.ProxyAddr(); got != blockedProxyURL {
		t.Fatalf("ProxyAddr must be blocked too, got %q", got)
	}
	if !mEnabled.TaskEgressBlocked() {
		t.Fatal("TaskEgressBlocked must report true while blocked")
	}
}

func TestPublicEgressUsesConfiguredProxy(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, `{"ip":"192.0.2.1"}`)
	}))
	defer target.Close()
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.String() != target.URL+"/" {
			t.Errorf("proxy request = %s", r.URL)
		}
		fmt.Fprint(w, `{"ip":"2001:db8::1","mullvad_exit_ip":true,"mullvad_exit_ip_hostname":"test-relay"}`)
	}))
	defer proxy.Close()

	if direct := probePublicEgress(t.Context(), "", "", target.URL); direct.IP != "192.0.2.1" || direct.Mullvad {
		t.Fatalf("direct probe = %+v", direct)
	}
	if proxied := probePublicEgress(t.Context(), proxy.URL, "", target.URL); proxied.IP != "2001:db8::1" || !proxied.Mullvad {
		t.Fatalf("proxied probe = %+v", proxied)
	}
	proxy.Close()
	if failed := probePublicEgress(t.Context(), proxy.URL, "", target.URL); failed.IP != "" {
		t.Fatalf("failed proxy probe fell back to direct = %+v", failed)
	}
}
