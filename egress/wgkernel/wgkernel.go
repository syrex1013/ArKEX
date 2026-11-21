// Package wgkernel brings up a real kernel WireGuard interface with an
// iptables kill-switch: every unmarked outbound packet leaves through the
// tunnel or is dropped — no application-level proxy involved for task traffic.
// LLM/control-plane sockets marked with egress.ControlPlaneMark are steered
// onto a host-gateway routing table so provider calls stay off Mullvad.
// Requires NET_ADMIN (and the kernel wireguard module); no TUN device is needed.
package wgkernel

import (
	"context"
	"fmt"
	"log"
	"net"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Autumn-27/artex/egress"
)

const (
	// Interface is the dedicated WireGuard interface name.
	Interface = "arkexwg0"
	// chain is the dedicated iptables OUTPUT chain carrying the kill-switch.
	chain = "ARKEX_WG"
	// mullvadDNS is the resolver reachable inside the tunnel.
	mullvadDNS = "10.64.0.1"
	// handshakeTimeout bounds how long Up waits for the first handshake.
	handshakeTimeout = 20 * time.Second
	// controlPlaneTable holds the host-gateway default route used by
	// SO_MARK'ed LLM sockets (egress.ControlPlaneMark).
	controlPlaneTable = 100
)

var (
	mu      sync.Mutex
	up      bool
	origRes []byte // original /etc/resolv.conf, restored by Down

	// Tunnel-era exception routes, removed by Down.
	relayRouteHost string
	allowRoutes    []string
	gateway        string // pre-tunnel default gateway + device
	gwdev          string
)

// Config is one kernel WireGuard bring-up.
type Config struct {
	PrivateKey string   // base64 WireGuard private key
	PeerKey    string   // base64 peer public key
	Endpoint   string   // relay IPv4:port
	LocalAddr  string   // tunnel interface IPv4 assigned to this device
	AllowCIDRs []string // destinations that keep leaving directly (docker networks)
	HostNames  []string // names resolved BEFORE switching, pinned in /etc/hosts
}

// Available reports whether the host can run kernel-mode WireGuard.
func Available() bool {
	for _, bin := range []string{"wg", "ip", "iptables"} {
		if _, err := exec.LookPath(bin); err != nil {
			return false
		}
	}
	return true
}

// Up creates the interface, routes everything through it and installs the
// kill-switch. On any failure it rolls back completely.
func Up(ctx context.Context, cfg Config) (err error) {
	mu.Lock()
	defer mu.Unlock()
	if up {
		return fmt.Errorf("kernel wireguard already up")
	}
	if cfg.LocalAddr == "" {
		return fmt.Errorf("local address required")
	}
	relayHost, _, err := net.SplitHostPort(cfg.Endpoint)
	if err != nil {
		return fmt.Errorf("parse endpoint: %w", err)
	}
	if _, err := net.ResolveIPAddr("ip4", relayHost); err != nil {
		return fmt.Errorf("resolve relay %q: %w", relayHost, err)
	}

	gateway, gwdev, err = defaultRoute(ctx)
	if err != nil {
		return err
	}

	// Pin names that resolve only through the container DNS (e.g. the Postgres
	// service) before resolv.conf switches to the in-tunnel resolver.
	hostAdds := pinHosts(cfg.HostNames)

	defer func() {
		if err != nil {
			_ = downLocked(ctx)
		}
	}()

	origRes, err = os.ReadFile("/etc/resolv.conf")
	if err != nil {
		return fmt.Errorf("read resolv.conf: %w", err)
	}

	steps := [][]string{
		{"link", "add", Interface, "type", "wireguard"},
		{"addr", "add", cfg.LocalAddr + "/32", "dev", Interface},
		{"link", "set", Interface, "up", "mtu", "1420"},
	}
	for _, args := range steps {
		if err = run(ctx, "ip", args...); err != nil {
			return err
		}
	}

	// Configure the peer; the private key goes through stdin (never on disk).
	wgArgs := []string{"set", Interface, "private-key", "/dev/stdin",
		"peer", cfg.PeerKey, "endpoint", cfg.Endpoint,
		"allowed-ips", "0.0.0.0/0", "persistent-keepalive", "25"}
	if err = runStdin(ctx, strings.NewReader(cfg.PrivateKey+"\n"), "wg", wgArgs...); err != nil {
		return err
	}

	// Direct exceptions: the relay endpoint itself and the docker networks
	// (Postgres). Route-level only; iptables enforces the rest. replace, not
	// add: a stale exception route from a crashed previous session must not
	// abort the bring-up.
	relayRouteHost, allowRoutes = relayHost, cfg.AllowCIDRs
	if err = run(ctx, "ip", "route", "replace", relayHost+"/32", "via", gateway, "dev", gwdev); err != nil {
		return err
	}
	for _, cidr := range cfg.AllowCIDRs {
		if cidr == "" {
			continue
		}
		_ = run(ctx, "ip", "route", "replace", cidr, "via", gateway, "dev", gwdev)
	}
	if err = run(ctx, "ip", "route", "replace", "default", "dev", Interface); err != nil {
		return err
	}

	// Policy routing for LLM/control-plane sockets (SO_MARK): they keep the
	// pre-tunnel default gateway and never enter the Mullvad tunnel. Task
	// traffic stays on the unmarked path through arkexwg0.
	if err = installControlPlaneRouting(ctx, gateway, gwdev); err != nil {
		return err
	}

	// Inside the tunnel: Mullvad's resolver.
	if err = os.WriteFile("/etc/resolv.conf", []byte("nameserver "+mullvadDNS+"\n"), 0o644); err != nil {
		return fmt.Errorf("write resolv.conf: %w", err)
	}
	if hostAdds != "" {
		_ = appendHosts(hostAdds)
	}

	// Kill-switch: default-drop OUTPUT, allow loopback, replies to our own
	// inbound traffic, the docker networks, marked control-plane, the relay
	// handshake, and wg0.
	if err = installKillSwitch(ctx, relayHost, cfg.AllowCIDRs); err != nil {
		return err
	}

	// Accept only a real handshake: an unreachable relay or unregistered key
	// must fail closed (Down rolls the routing back).
	if !waitHandshake(ctx) {
		err = fmt.Errorf("wireguard handshake failed within %s: relay %s unreachable or key not registered", handshakeTimeout, cfg.Endpoint)
		return err
	}
	up = true
	return nil
}

// Down removes the kill-switch, restores routing/DNS and deletes the interface.
func Down(ctx context.Context) error {
	mu.Lock()
	defer mu.Unlock()
	return downLocked(ctx)
}

func downLocked(ctx context.Context) error {
	var errs []string
	fail := func(what string, e error) {
		if e != nil {
			errs = append(errs, what+": "+e.Error())
		}
	}

	// Kill-switch first: if the interface disappears with OUTPUT still DROP,
	// nothing can leave — but the app also needs its control plane back.
	fail("unhook chain", run(ctx, "iptables", "-D", "OUTPUT", "-j", chain))
	fail("flush chain", run(ctx, "iptables", "-F", chain))
	fail("delete chain", run(ctx, "iptables", "-X", chain))
	fail("policy accept", run(ctx, "iptables", "-P", "OUTPUT", "ACCEPT"))
	removeControlPlaneRouting(ctx)

	if gateway != "" {
		fail("route default", run(ctx, "ip", "route", "replace", "default", "via", gateway, "dev", gwdev))
	}
	// Tunnel-era exception routes must go too, or the next Up hits "File exists".
	for _, cidr := range allowRoutes {
		if cidr != "" {
			_ = run(ctx, "ip", "route", "del", cidr)
		}
	}
	if relayRouteHost != "" {
		_ = run(ctx, "ip", "route", "del", relayRouteHost+"/32")
	}
	relayRouteHost, allowRoutes = "", nil
	fail("delete iface", run(ctx, "ip", "link", "del", Interface))
	if origRes != nil {
		fail("restore resolv", os.WriteFile("/etc/resolv.conf", origRes, 0o644))
		origRes = nil
	}
	up = false
	if len(errs) > 0 {
		return fmt.Errorf("kernel wireguard down: %s", strings.Join(errs, "; "))
	}
	return nil
}

// Status returns the interface state and the age of the last handshake.
// An interface whose default route vanished (someone took the link down
// manually) reports an error so the watchdog rebuilds the tunnel instead of
// trusting a fresh handshake over a dead route.
func Status(ctx context.Context) (isUp bool, handshakeAge time.Duration, err error) {
	out, err := exec.CommandContext(ctx, "ip", "route", "show", "default").Output()
	if err != nil || !strings.Contains(string(out), "dev "+Interface) {
		return false, 0, fmt.Errorf("tunnel default route missing")
	}
	out, err = exec.CommandContext(ctx, "wg", "show", Interface, "latest-handshakes").Output()
	if err != nil {
		return false, 0, err
	}
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		f := strings.Fields(line)
		if len(f) != 2 {
			continue
		}
		ts, perr := strconv.ParseInt(f[1], 10, 64)
		if perr != nil || ts == 0 {
			continue
		}
		return true, time.Since(time.Unix(ts, 0)), nil
	}
	return true, 0, nil // interface exists, no handshake yet
}

// IsUp reports whether the interface is currently established by this package.
func IsUp() bool {
	mu.Lock()
	defer mu.Unlock()
	return up
}

func installControlPlaneRouting(ctx context.Context, gw, dev string) error {
	// Fresh table each bring-up: wipe leftovers from a crashed session first.
	_ = run(ctx, "ip", "route", "flush", "table", strconv.Itoa(controlPlaneTable))
	_ = run(ctx, "ip", "rule", "del", "fwmark", strconv.Itoa(egress.ControlPlaneMark), "lookup", strconv.Itoa(controlPlaneTable))
	table := strconv.Itoa(controlPlaneTable)
	// On-link routes for the host NIC so the gateway (and docker-bridge peers
	// like host.docker.internal via the gateway) are reachable without the
	// tunnel default route.
	if out, err := exec.CommandContext(ctx, "ip", "-4", "route", "show", "dev", dev, "scope", "link").Output(); err == nil {
		for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
			f := strings.Fields(line)
			if len(f) == 0 || !strings.Contains(f[0], "/") {
				continue
			}
			_ = run(ctx, "ip", "route", "replace", f[0], "dev", dev, "table", table)
		}
	}
	if err := run(ctx, "ip", "route", "replace", gw+"/32", "dev", dev, "table", table); err != nil {
		return err
	}
	if err := run(ctx, "ip", "route", "replace", "default", "via", gw, "dev", dev, "table", table); err != nil {
		return err
	}
	return run(ctx, "ip", "rule", "add", "fwmark", strconv.Itoa(egress.ControlPlaneMark), "lookup", table, "priority", "100")
}

func removeControlPlaneRouting(ctx context.Context) {
	_ = run(ctx, "ip", "rule", "del", "fwmark", strconv.Itoa(egress.ControlPlaneMark), "lookup", strconv.Itoa(controlPlaneTable))
	_ = run(ctx, "ip", "route", "flush", "table", strconv.Itoa(controlPlaneTable))
}

func installKillSwitch(ctx context.Context, relayHost string, allow []string) error {
	_ = run(ctx, "iptables", "-N", chain) // exists on a retry: fine
	if err := run(ctx, "iptables", "-F", chain); err != nil {
		return err
	}
	rules := [][]string{
		{"-A", chain, "-o", "lo", "-j", "ACCEPT"},
		{"-A", chain, "-m", "conntrack", "--ctstate", "ESTABLISHED,RELATED", "-j", "ACCEPT"},
		{"-A", chain, "-o", Interface, "-j", "ACCEPT"},
		// LLM/control-plane sockets (egress.ControlPlaneDialer) leave marked and
		// must be accepted before the default DROP — they route via table 100.
		{"-A", chain, "-m", "mark", "--mark", strconv.Itoa(egress.ControlPlaneMark), "-j", "ACCEPT"},
	}
	for _, cidr := range allow {
		if cidr != "" {
			rules = append(rules, []string{"-A", chain, "-d", cidr, "-j", "ACCEPT"})
		}
	}
	rules = append(rules,
		[]string{"-A", chain, "-d", relayHost, "-p", "udp", "-j", "ACCEPT"},
		[]string{"-A", chain, "-j", "DROP"},
	)
	for _, r := range rules {
		if err := run(ctx, "iptables", r...); err != nil {
			return err
		}
	}
	if err := run(ctx, "iptables", "-C", "OUTPUT", "-j", chain); err != nil {
		if err := run(ctx, "iptables", "-I", "OUTPUT", "1", "-j", chain); err != nil {
			return err
		}
	}
	return run(ctx, "iptables", "-P", "OUTPUT", "DROP")
}

func waitHandshake(ctx context.Context) bool {
	deadline := time.Now().Add(handshakeTimeout)
	for time.Now().Before(deadline) {
		if isUp, _, err := Status(ctx); err == nil && isUp {
			if age, ok := handshakeAge(ctx); ok && age >= 0 {
				return true
			}
		}
		time.Sleep(500 * time.Millisecond)
	}
	return false
}

func handshakeAge(ctx context.Context) (time.Duration, bool) {
	out, err := exec.CommandContext(ctx, "wg", "show", Interface, "latest-handshakes").Output()
	if err != nil {
		return 0, false
	}
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		f := strings.Fields(line)
		if len(f) == 2 && f[1] != "0" {
			return time.Since(time.Unix(mustInt(f[1]), 0)), true
		}
	}
	return 0, false
}

func mustInt(s string) int64 {
	v, _ := strconv.ParseInt(s, 10, 64)
	return v
}

func defaultRoute(ctx context.Context) (gateway, dev string, err error) {
	out, err := exec.CommandContext(ctx, "ip", "-4", "route", "show", "default").Output()
	if err != nil {
		return "", "", fmt.Errorf("read default route: %w", err)
	}
	f := strings.Fields(string(out))
	for i := 0; i+1 < len(f); i++ {
		switch f[i] {
		case "via":
			gateway = f[i+1]
		case "dev":
			dev = f[i+1]
		}
	}
	if gateway == "" || dev == "" {
		return "", "", fmt.Errorf("no IPv4 default route found")
	}
	return gateway, dev, nil
}

// pinHosts resolves names now (before the resolver switch) and returns the
// /etc/hosts lines that keep them reachable inside the tunnel. A rebuild may
// run while resolv.conf still points at the tunnel's resolver (a previous Down
// raced a failed Up and lost the original), where Docker's embedded DNS is
// unreachable — so every name gets a second try through Docker's own resolver.
func pinHosts(names []string) string {
	var b strings.Builder
	for _, n := range names {
		if n == "" || net.ParseIP(n) != nil {
			continue
		}
		ips, err := net.LookupHost(n)
		if err != nil || len(ips) == 0 {
			ips, err = dockerLookupHost(n)
			if err != nil || len(ips) == 0 {
				log.Printf("[wgkernel] failed to pin host %q for the tunnel: default resolver and Docker DNS (127.0.0.11) both failed: %v", n, err)
				continue
			}
		}
		b.WriteString(ips[0] + " " + n + "\n")
	}
	out := b.String()
	if len(names) > 0 {
		log.Printf("[wgkernel] pinned hosts: %q", strings.TrimSpace(out))
	}
	return out
}

// dockerLookupHost resolves one name against Docker's embedded DNS (127.0.0.11)
// directly, bypassing whatever resolv.conf currently says.
func dockerLookupHost(name string) ([]string, error) {
	r := &net.Resolver{
		PreferGo: true,
		Dial: func(ctx context.Context, network, _ string) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, "udp", "127.0.0.11:53")
		},
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	return r.LookupHost(ctx, name)
}

func appendHosts(add string) error {
	// Repeated bring-ups (watchdog rebuilds) must not grow the file without
	// bound: skip names already pinned by an earlier Up.
	if existing, err := os.ReadFile("/etc/hosts"); err == nil {
		var kept []string
		for _, line := range strings.Split(add, "\n") {
			f := strings.Fields(line)
			if len(f) != 2 {
				continue
			}
			if strings.Contains(string(existing), " "+f[1]+"\n") {
				continue
			}
			kept = append(kept, line)
		}
		add = strings.Join(kept, "\n")
		if add == "" {
			return nil
		}
	}
	f, err := os.OpenFile("/etc/hosts", os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.WriteString("\n# arkex wireguard pinned hosts\n" + add)
	return err
}

func run(ctx context.Context, bin string, args ...string) error {
	cmd := exec.CommandContext(ctx, bin, args...)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("%s %s: %v: %s", bin, strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return nil
}

func runStdin(ctx context.Context, stdin *strings.Reader, bin string, args ...string) error {
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Stdin = stdin
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("%s %s: %v: %s", bin, strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return nil
}
