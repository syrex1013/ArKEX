package server

import (
	"bytes"
	"context"
	"log"
	"strings"
	"testing"

	"github.com/Autumn-27/artex/egress/wgproxy"
)

// A stale session (lost relay, crashed handshake) still holds the proxy port;
// the watchdog must stop it before restarting or every rebind fails with
// "address already in use" forever. The restart proves it got past the bind
// by reaching the handshake phase — a bind failure logs "listen on proxy
// port" and never attempts a handshake, so a handshake-failure log line is
// the race-immune witness that the port was released.
func TestMaintainGlobalWGStopsZombieBeforeRestart(t *testing.T) {
	s, _, _ := trafficEvidenceServer(t)
	pg := s.m.PG()
	t.Cleanup(func() {
		// Clear through the manager so the in-memory session + cache die too.
		_ = s.m.SetGlobalWGConfig("")
		pg.Exec(`DELETE FROM settings WHERE key IN ('global_wg_config','global_wg_proxy_port')`)
	})

	conf, _ := testWGConf(t)
	// Store the config + cache directly to skip the first-start handshake wait.
	if err := pg.SetSetting(settingGlobalWGConfig, conf); err != nil {
		t.Fatal(err)
	}
	s.m.mu.Lock()
	s.m.globalWGConf = conf
	s.m.mu.Unlock()

	// Fabricate a zombie: a real session holding the proxy port whose
	// handshake never completes (the dead TEST-NET endpoint).
	cfg, err := wgproxy.ParseConfig(conf)
	if err != nil {
		t.Fatal(err)
	}
	zombie, err := wgproxy.Start(context.Background(), wgproxy.Config{
		PrivateKey: cfg.PrivateKey,
		PeerKey:    cfg.PeerKey,
		Endpoint:   cfg.Endpoint,
		ProxyPort:  s.m.GlobalWGPort(),
		DNS:        cfg.DNS,
		LocalAddr:  cfg.LocalAddr,
	})
	if err != nil {
		t.Fatalf("start zombie session: %v", err)
	}
	s.m.mu.Lock()
	s.m.globalWG = zombie
	s.m.mu.Unlock()

	orig := log.Writer()
	var buf bytes.Buffer
	log.SetOutput(&buf)
	defer log.SetOutput(orig)

	s.m.maintainGlobalWG()

	logs := buf.String()
	if !strings.Contains(logs, "handshake failed") {
		t.Fatalf("restart never reached the handshake phase — zombie still held the port? logs: %s", logs)
	}
}
