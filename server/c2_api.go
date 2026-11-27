package server

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/Autumn-27/artex/egress/mullvad"
	"github.com/Autumn-27/artex/egress/wgkernel"
	"github.com/Autumn-27/artex/offensive/sliverc2"
)

// sliverManager lazily builds the manager pointed at the operator config.
func (s *Server) sliverManager() *sliverc2.Manager {
	if s.sliverC2 != nil {
		return s.sliverC2
	}
	if s.sliverC2 == nil {
		s.sliverC2 = sliverc2.New(s.sliverConfigPath())
	}
	return s.sliverC2
}

func (s *Server) sliverConfigPath() string {
	// A saved setting row wins over the ARKEX_SLIVER_CONFIG env fallback.
	if s.m != nil {
		if v, ok, _ := s.m.pg.GetSetting(settingSliverConfigPath); ok && strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	if p := strings.TrimSpace(os.Getenv("ARKEX_SLIVER_CONFIG")); p != "" {
		return p
	}
	if s.m != nil && s.m.dir != "" {
		return filepath.Join(s.m.dir, "sliver", "arkex.operator.conf")
	}
	return "sliver.operator.conf"
}

// GET /api/c2/status
func (s *Server) c2Status(w http.ResponseWriter, _ *http.Request) {
	m := s.sliverManager()
	writeJSON(w, 200, map[string]any{
		"connected":   m.Connected(),
		"config_path": m.ConfigPath(),
		"config_set":  os.Getenv("ARKEX_SLIVER_CONFIG") != "" || fileExists(s.sliverConfigPath()),
	})
}

func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

// POST /api/c2/connect
func (s *Server) c2Connect(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ConfigPath string `json:"config_path"`
	}
	if err := decode(w, r, &req); err != nil {
		writeErr(w, 400, "bad json")
		return
	}
	m := s.sliverManager()
	if strings.TrimSpace(req.ConfigPath) != "" {
		m = sliverc2.New(req.ConfigPath)
		s.sliverC2 = m
	}
	if err := m.Connect(r.Context()); err != nil {
		writeErr(w, 502, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true})
}

// POST /api/c2/disconnect
func (s *Server) c2Disconnect(w http.ResponseWriter, _ *http.Request) {
	if err := s.sliverManager().Disconnect(); err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true})
}

// GET /api/c2/sessions
func (s *Server) c2Sessions(w http.ResponseWriter, r *http.Request) {
	items, err := s.sliverManager().Sessions(r.Context())
	if err != nil {
		writeErr(w, 502, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"sessions": items})
}

// GET /api/c2/beacons
func (s *Server) c2Beacons(w http.ResponseWriter, r *http.Request) {
	items, err := s.sliverManager().Beacons(r.Context())
	if err != nil {
		writeErr(w, 502, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"beacons": items})
}

// GET /api/c2/beacons/{id}/tasks
func (s *Server) c2BeaconTasks(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		writeErr(w, 400, "beacon id required")
		return
	}
	items, err := s.sliverManager().BeaconTasks(r.Context(), id)
	if err != nil {
		writeErr(w, 502, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"tasks": items})
}

// POST /api/c2/exec {target, path, args, timeout}
func (s *Server) c2Exec(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Target  string   `json:"target"`
		Path    string   `json:"path"`
		Args    []string `json:"args"`
		Timeout int64    `json:"timeout_seconds"`
	}
	if err := decode(w, r, &req); err != nil || strings.TrimSpace(req.Target) == "" || strings.TrimSpace(req.Path) == "" {
		writeErr(w, 400, "target and path required")
		return
	}
	res, err := s.sliverManager().Exec(r.Context(), req.Target, req.Path, req.Args, time.Duration(req.Timeout)*time.Second)
	if err != nil {
		writeErr(w, 502, err.Error())
		return
	}
	writeJSON(w, 200, res)
}

// POST /api/c2/kill {target}
func (s *Server) c2Kill(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Target string `json:"target"`
	}
	if err := decode(w, r, &req); err != nil || strings.TrimSpace(req.Target) == "" {
		writeErr(w, 400, "target required")
		return
	}
	if err := s.sliverManager().Kill(r.Context(), req.Target); err != nil {
		writeErr(w, 502, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true})
}

// POST /api/c2/implants/generate
func (s *Server) c2Generate(w http.ResponseWriter, r *http.Request) {
	var req struct {
		GOOS     string   `json:"goos"`
		GOARCH   string   `json:"goarch"`
		Format   string   `json:"format"`
		Name     string   `json:"name"`
		C2URLs   []string `json:"c2_urls"`
		IsBeacon bool     `json:"is_beacon"`
		Interval int64    `json:"interval_seconds"`
		Retry    int64    `json:"retry_seconds"`
	}
	if err := decode(w, r, &req); err != nil {
		writeErr(w, 400, "bad json")
		return
	}
	if req.GOOS == "" || req.GOARCH == "" || req.Format == "" {
		writeErr(w, 400, "goos, goarch and format required")
		return
	}
	outDir := ""
	if s.m != nil && s.m.dir != "" {
		outDir = filepath.Join(s.m.dir, "implants")
		_ = os.MkdirAll(outDir, 0o750)
	}
	path, err := s.sliverManager().Generate(r.Context(), sliverc2.GenerateOptions{
		GOOS: req.GOOS, GOARCH: req.GOARCH, Format: req.Format,
		Name: req.Name, C2URLs: req.C2URLs, IsBeacon: req.IsBeacon,
		Interval: req.Interval, Retry: req.Retry, OutputDir: outDir,
	})
	if err != nil {
		writeErr(w, 502, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"path": path})
}

// GET /api/c2/events — SSE stream of Sliver server events.
func (s *Server) c2Events(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeErr(w, 500, "streaming unsupported")
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	events := make(chan map[string]any, 64)
	go func() {
		_ = s.sliverManager().Events(r.Context(), events)
		close(events)
	}()
	for ev := range events {
		raw, _ := json.Marshal(ev)
		fmt.Fprintf(w, "data: %s\n\n", raw)
		flusher.Flush()
	}
}

// ---------- Mullvad ----------

func (s *Server) mullvadClient() *mullvad.Client {
	if s.mullvad == nil {
		dir := ""
		if s.m != nil {
			dir = s.m.dir
		}
		s.mullvad = mullvad.New(dir)
	}
	return s.mullvad
}

// GET /api/mullvad/servers?ping=1 — relay list, optionally latency-sorted.
func (s *Server) mullvadServers(w http.ResponseWriter, r *http.Request) {
	if r.URL.Query().Get("ping") == "1" {
		items, err := s.mullvadClient().ListPinged(r.Context())
		if err != nil {
			writeErr(w, 502, err.Error())
			return
		}
		writeJSON(w, 200, map[string]any{"servers": items})
		return
	}
	relays, err := s.mullvadClient().Relays(r.Context())
	if err != nil {
		writeErr(w, 502, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"servers": relays})
}

// POST /api/mullvad/up {hostname, port}
//
// When the Mullvad userspace WireGuard proxy is enabled in settings
// (mullvad_proxy_enabled + mullvad_private_key), Up starts the wgproxy netstack
// session for the selected relay — no wg-quick/NET_ADMIN needed. Status then
// reports mode "netstack" and the local proxy endpoint, and ProxyAddr() routes
// all task egress through it. Without the settings, the wg-quick path remains
// as before.
func (s *Server) mullvadUp(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Hostname string `json:"hostname"`
		Port     uint16 `json:"port"`
	}
	if err := decode(w, r, &req); err != nil || strings.TrimSpace(req.Hostname) == "" {
		writeErr(w, 400, "hostname required")
		return
	}
	port := uint16(0)
	if req.Port > 0 {
		port = req.Port
	}

	// Userspace netstack path when the settings say so.
	if s.m.MullvadProxyEnabled() && strings.TrimSpace(s.m.MullvadPrivateKey()) != "" {
		s.m.stopWGProxy()
		// Shared start path: configures the tunnel with the Mullvad-assigned
		// address, kicks the handshake, and accepts the session only after a
		// real handshake — otherwise everything stays fail-closed blocked.
		// context.Background(): the session outlives this HTTP request.
		if err := s.m.startWGProxy(context.Background(), req.Hostname, req.Port); err != nil {
			writeErr(w, 502, err.Error())
			return
		}
		// Kernel mode leaves the userspace session nil; only the netstack
		// session exposes a local proxy URL.
		out := map[string]any{"ok": true, "conf_path": ""}
		if sess := s.m.wgProxySession(); sess != nil {
			out["mode"] = "netstack"
			out["proxy"] = sess.ProxyURL()
		} else {
			out["mode"] = "kernel"
		}
		writeJSON(w, 200, out)
		return
	}

	conf, applied, err := s.mullvadClient().Up(r.Context(), req.Hostname, port)
	if err != nil {
		writeErr(w, 502, err.Error())
		return
	}
	out := map[string]any{"ok": applied, "conf_path": conf}
	if applied {
		// wg-quick brought an interface up: repoint the MITM upstream, browser
		// proxy and agent transports at the effective task egress.
		s.m.syncWGProxyEgress()
		s.rebuildAgentsForEgressChange()
	} else {
		out["note"] = "wg-quick unavailable here — apply manually with `wg-quick up <conf_path>` (container needs NET_ADMIN + /dev/net/tun for automatic bring-up)"
	}
	writeJSON(w, 200, out)
}

// POST /api/mullvad/down
func (s *Server) mullvadDown(w http.ResponseWriter, _ *http.Request) {
	s.m.stopWGProxy()
	// Fail closed: with Mullvad still enabled, the runtime paths now resolve to
	// the blocked proxy — nothing dials direct — and the watchdog stays quiet
	// until an explicit Up or re-enable.
	s.m.syncWGProxyEgress()
	s.rebuildAgentsForEgressChange()
	// wg-quick teardown only applies to the wg-quick path; the userspace
	// netstack session was already stopped above and needs no interface work.
	if !s.m.MullvadProxyEnabled() {
		if err := s.mullvadClient().Down(); err != nil {
			writeErr(w, 502, err.Error())
			return
		}
	}
	writeJSON(w, 200, map[string]any{"ok": true, "task_egress_blocked": s.m.TaskEgressBlocked()})
}

// rebuildAgentsForEgressChange rebuilds the running agents after the effective
// task proxy changed (wg session up/down), mirroring the putSettings rebuild
// path. invalidateProfileAgents also drops the per-profile ChatAgent cache —
// its transports captured the old proxy URL — and invalidates the running
// tasks' agent bundles so the next round picks up the new egress. Best-effort:
// only when an LLM config is active.
func (s *Server) rebuildAgentsForEgressChange() {
	s.cfgMu.Lock()
	cfg, on := s.llmCfg, s.llmOn
	s.cfgMu.Unlock()
	if on {
		if err := s.applyLLM(cfg); err != nil {
			log.Printf("[mullvad] agent rebuild after proxy change failed: %v", err)
		}
	}
	s.invalidateProfileAgents()
	egressLive.Notify()
}

// GET /api/mullvad/status
func (s *Server) mullvadStatus(w http.ResponseWriter, r *http.Request) {
	out := s.mullvadClient().Status()
	out["proxy_enabled"] = s.m.MullvadProxyEnabled()
	out["proxy_port"] = s.m.MullvadProxyPort()
	out["mode_setting"] = s.m.MullvadMode()
	if wgkernel.IsUp() {
		out["mode"] = "kernel"
		out["interface"] = wgkernel.Interface
		out["up"] = true
		if _, age, err := wgkernel.Status(r.Context()); err == nil {
			out["handshake_age_sec"] = int(age.Seconds())
		}
	} else if sess := s.m.wgProxySession(); sess != nil {
		out["mode"] = "netstack"
		out["up"] = true
		out["proxy"] = sess.ProxyURL()
		out["session_up"] = sess.Healthy()
		out["handshake_seen"] = sess.Healthy()
	}
	out["task_egress_blocked"] = s.m.TaskEgressBlocked()
	out["address_set"] = s.m.MullvadAddress() != ""
	writeJSON(w, 200, out)
}

// egressPayload is the navbar pill snapshot: tunnel flags + public IP through
// the current task route. Shared by GET /api/mullvad/egress and the live SSE
// stream so both surfaces stay byte-identical.
func (s *Server) egressPayload(ctx context.Context) map[string]any {
	out := s.mullvadClient().Status()
	out["mode_setting"] = s.m.MullvadMode()
	out["egress_direct"] = s.m.EgressDirect()
	proxy := s.m.EffectiveTaskProxyURL()
	out["task_egress_blocked"] = proxy == blockedProxyURL
	out["global_proxy_set"] = s.m.GlobalProxy() != ""
	wgUp, wgEndpoint, _ := s.m.GlobalWGState()
	out["global_wg_up"] = wgUp
	out["global_wg_endpoint"] = wgEndpoint
	out["global_wg_set"] = s.m.GlobalWGConfigured()
	out["egress_ip"] = ""
	out["egress_tunneled"] = false
	if wgkernel.IsUp() {
		out["up"] = true
		out["mode"] = "kernel"
		out["interface"] = wgkernel.Interface
	} else if sess := s.m.wgProxySession(); sess != nil {
		out["up"] = sess.Healthy()
		out["mode"] = "netstack"
	}

	if proxy != blockedProxyURL {
		var caPath string
		if recorder := s.m.Traffic(); recorder != nil && proxy == recorder.ProxyAddr() {
			caPath = s.m.ProxyCACert()
		}
		exit := probePublicEgress(ctx, proxy, caPath, "https://am.i.mullvad.net/json")
		// A routing change during the lookup invalidates the measurement.
		if proxy == s.m.EffectiveTaskProxyURL() {
			out["egress_ip"] = exit.IP
			out["egress_relay"] = exit.Relay
			out["egress_tunneled"] = exit.Mullvad
		}
	}
	return out
}

// GET /api/mullvad/egress — public IP observed through the current task route.
func (s *Server) mullvadEgress(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, 200, s.egressPayload(r.Context()))
}

// GET /api/mullvad/egress/stream — live SSE of egressPayload. Pushes on connect,
// on every egress route change, and every 60s so the public IP stays fresh.
func (s *Server) streamMullvadEgress(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeErr(w, 500, "streaming unsupported")
		return
	}
	tok := extractToken(r)
	if !s.sessionOK(tok) {
		writeErr(w, 401, "token is invalid or expired")
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")

	ch, unsub := egressLive.Subscribe()
	defer unsub()
	send := func() {
		if !s.sessionOK(tok) {
			return
		}
		b, err := json.Marshal(s.egressPayload(r.Context()))
		if err != nil {
			return
		}
		fmt.Fprintf(w, "data: %s\n\n", b)
		flusher.Flush()
	}
	send()

	refresh := time.NewTicker(60 * time.Second)
	defer refresh.Stop()
	ping := time.NewTicker(20 * time.Second)
	defer ping.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case _, ok := <-ch:
			if !ok {
				return
			}
			if !s.sessionOK(tok) {
				return
			}
			send()
		case <-refresh.C:
			if !s.sessionOK(tok) {
				return
			}
			send()
		case <-ping.C:
			if !s.sessionOK(tok) {
				return
			}
			fmt.Fprintf(w, ": ping\n\n")
			flusher.Flush()
		}
	}
}

type publicEgress struct {
	IP      string `json:"ip"`
	Relay   string `json:"mullvad_exit_ip_hostname"`
	Mullvad bool   `json:"mullvad_exit_ip"`
}

func probePublicEgress(ctx context.Context, proxy, caPath, endpoint string) publicEgress {
	transport := &http.Transport{DisableKeepAlives: true}
	if proxy != "" {
		proxyURL, err := url.Parse(proxy)
		if err != nil || proxyURL.Host == "" {
			log.Printf("[egress] probe %s: unusable proxy %q: %v", endpoint, proxyLabel(proxy), err)
			return publicEgress{}
		}
		if proxyURL.Hostname() == "0.0.0.0" {
			proxyURL.Host = "127.0.0.1:" + proxyURL.Port()
		}
		transport.Proxy = http.ProxyURL(proxyURL)
	} else {
		transport.Proxy = nil
	}
	if caPath != "" {
		pem, err := os.ReadFile(caPath)
		if err != nil {
			log.Printf("[egress] probe %s: read CA %s: %v", endpoint, caPath, err)
			return publicEgress{}
		}
		roots, err := x509.SystemCertPool()
		if err != nil {
			log.Printf("[egress] probe %s: system cert pool: %v", endpoint, err)
			return publicEgress{}
		}
		if !roots.AppendCertsFromPEM(pem) {
			log.Printf("[egress] probe %s: CA %s holds no certificates", endpoint, caPath)
			return publicEgress{}
		}
		transport.TLSClientConfig = &tls.Config{RootCAs: roots}
	}
	client := http.Client{
		Transport:     transport,
		Timeout:       6 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		log.Printf("[egress] probe %s: build request: %v", endpoint, err)
		return publicEgress{}
	}
	resp, err := client.Do(req)
	if err != nil {
		// The route is the thing under test, so a failed probe is the only
		// signal that the tunnel, the proxy chain or the DNS layer broke.
		log.Printf("[egress] probe %s via %q failed: %v", endpoint, proxyLabel(proxy), err)
		return publicEgress{}
	}
	defer resp.Body.Close()
	var exit publicEgress
	if resp.StatusCode != http.StatusOK || json.NewDecoder(io.LimitReader(resp.Body, 4096)).Decode(&exit) != nil {
		log.Printf("[egress] probe %s via %q: status %d, unusable body", endpoint, proxyLabel(proxy), resp.StatusCode)
		return publicEgress{}
	}
	if _, err := netip.ParseAddr(exit.IP); err != nil {
		log.Printf("[egress] probe %s via %q: body has no usable ip (%q)", endpoint, proxyLabel(proxy), exit.IP)
		return publicEgress{}
	}
	return exit
}

// proxyLabel renders a proxy URL for logs without its credentials.
func proxyLabel(proxy string) string {
	if proxy == "" {
		return "direct"
	}
	u, err := url.Parse(proxy)
	if err != nil || u.Host == "" {
		return "unparsable"
	}
	return u.Scheme + "://" + u.Host
}

// suppress unused import when ports change
var _ = strconv.Itoa
