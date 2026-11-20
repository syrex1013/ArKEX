// Package mullvad integrates Mullvad WireGuard egress: relay discovery,
// latency probing, config generation, and wg-quick bring-up.
package mullvad

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const (
	defaultTTL = time.Hour
)

// relaysURL is a var so tests can point it at a stub server.
var relaysURL = "https://api.mullvad.net/public/relays/wireguard/v1/"

// Relay is one Mullvad WireGuard server.
type Relay struct {
	Hostname    string `json:"hostname"`
	IPv4        string `json:"ipv4"`
	PublicKey   string `json:"public_key"`
	Country     string `json:"country"`
	CountryCode string `json:"country_code"`
	City        string `json:"city"`
}

// RelayWithPing pairs a relay with measured TCP-connect latency.
// Latency is TCP:443 (the OpenVPN listener every Mullvad relay serves) as an
// RTT proxy — WireGuard itself is UDP and cannot be pinged unprivileged.
type RelayWithPing struct {
	Relay
	PingMS float64 `json:"ping_ms"`
}

type relayCache struct {
	fetchedAt time.Time
	relays    []Relay
}

// Client manages the Mullvad integration.
type Client struct {
	http    *http.Client
	mu      sync.Mutex
	cache   relayCache
	dataDir string
}

func New(dataDir string) *Client {
	return &Client{
		http:    &http.Client{Timeout: 20 * time.Second},
		dataDir: filepath.Join(dataDir, "mullvad"),
	}
}

// setRelaysURL is a test-only hook.
func setRelaysURL(u string) { relaysURL = u }

// Relays fetches (cached 1h) the Mullvad WireGuard relay list.
func (c *Client) Relays(ctx context.Context) ([]Relay, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.cache.relays != nil && time.Since(c.cache.fetchedAt) < defaultTTL {
		return c.cache.relays, nil
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, relaysURL, nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("mullvad relay list: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("mullvad relay list: HTTP %d", resp.StatusCode)
	}
	var payload struct {
		Countries []struct {
			Name   string `json:"name"`
			Code   string `json:"code"`
			Cities []struct {
				Name   string `json:"name"`
				Code   string `json:"code"`
				Relays []struct {
					Hostname   string `json:"hostname"`
					IPv4AddrIn string `json:"ipv4_addr_in"`
					PublicKey  string `json:"public_key"`
				} `json:"relays"`
			} `json:"cities"`
		} `json:"countries"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return nil, err
	}
	var relays []Relay
	for _, c := range payload.Countries {
		for _, city := range c.Cities {
			for _, r := range city.Relays {
				relays = append(relays, Relay{
					Hostname:    r.Hostname,
					IPv4:        r.IPv4AddrIn,
					PublicKey:   r.PublicKey,
					Country:     c.Name,
					CountryCode: c.Code,
					City:        city.Name,
				})
			}
		}
	}
	c.cache = relayCache{fetchedAt: time.Now(), relays: relays}
	return c.cache.relays, nil
}

// FindRelay resolves one relay by hostname (exact match against the cached relay list).
func (c *Client) FindRelay(ctx context.Context, hostname string) (Relay, error) {
	relays, err := c.Relays(ctx)
	if err != nil {
		return Relay{}, err
	}
	hostname = strings.TrimSpace(hostname)
	for _, r := range relays {
		if strings.EqualFold(r.Hostname, hostname) {
			return r, nil
		}
	}
	return Relay{}, fmt.Errorf("relay %q not found in the Mullvad relay list", hostname)
}

// PingRelay measures TCP-connect RTT to relay:443 (best of 3). WireGuard ports
// are UDP; 443/TCP is served by every Mullvad relay and is a stable RTT proxy.
func PingRelay(ctx context.Context, r Relay, port uint16) (float64, error) {
	if port == 0 {
		port = 443
	}
	addr := net.JoinHostPort(r.IPv4, fmt.Sprint(port))
	best := -1.0
	var lastErr error
	for i := 0; i < 2; i++ {
		start := time.Now()
		d := net.Dialer{Timeout: 1500 * time.Millisecond}
		conn, err := d.DialContext(ctx, "tcp", addr)
		ms := float64(time.Since(start).Microseconds()) / 1000.0
		if err == nil {
			_ = conn.Close()
			if best < 0 || ms < best {
				best = ms
			}
		} else {
			lastErr = err
		}
	}
	if best < 0 {
		return -1, lastErr
	}
	return best, nil
}

// ListPinged returns relays with concurrent latency probes, sorted fastest first.
func (c *Client) ListPinged(ctx context.Context) ([]RelayWithPing, error) {
	relays, err := c.Relays(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]RelayWithPing, len(relays))
	workers := 64
	jobs := make(chan int)
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range jobs {
				item := RelayWithPing{Relay: relays[i], PingMS: -1}
				pctx, cancel := context.WithTimeout(ctx, 8*time.Second)
				if ms, err := PingRelay(pctx, relays[i], 0); err == nil {
					item.PingMS = ms
				}
				cancel()
				out[i] = item
			}
		}()
	}
	for i := range relays {
		select {
		case jobs <- i:
		case <-ctx.Done():
			close(jobs)
			wg.Wait()
			return nil, ctx.Err()
		}
	}
	close(jobs)
	wg.Wait()
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j].PingMS >= 0 && (out[j-1].PingMS < 0 || out[j-1].PingMS > out[j].PingMS); j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return out, nil
}

// privateKeySource resolves the user's Mullvad-registered WireGuard private key.
// Mullvad only tunnels keys registered to a Mullvad account, so it is never
// auto-generated here.
func (c *Client) privateKeySource() (string, error) {
	if k := strings.TrimSpace(os.Getenv("ARKEX_MULLVAD_PRIVKEY")); k != "" {
		return k, nil
	}
	if k, err := os.ReadFile(filepath.Join(c.dataDir, "private.key")); err == nil {
		if s := strings.TrimSpace(string(k)); s != "" {
			return s, nil
		}
	}
	return "", errors.New(
		"Mullvad private key required: put your account-registered WireGuard private key in data/mullvad/private.key or ARKEX_MULLVAD_PRIVKEY (generate+register one via the Mullvad app or account page)")
}

// Up writes a wg-quick config for the chosen relay and brings it up when
// wg-quick is available. Without wg-quick (e.g. inside a container without
// NET_ADMIN/TUN), the config path is returned for manual `wg-quick up`.
func (c *Client) Up(ctx context.Context, hostname string, port uint16) (confPath string, applied bool, err error) {
	relays, err := c.Relays(ctx)
	if err != nil {
		return "", false, err
	}
	var relay *Relay
	for i := range relays {
		if strings.EqualFold(relays[i].Hostname, hostname) {
			relay = &relays[i]
			break
		}
	}
	if relay == nil {
		return "", false, fmt.Errorf("unknown relay %q", hostname)
	}
	if port == 0 {
		port = 51820
	}
	priv, err := c.privateKeySource()
	if err != nil {
		return "", false, err
	}
	if err := os.MkdirAll(c.dataDir, 0o750); err != nil {
		return "", false, err
	}
	conf := fmt.Sprintf(`[Interface]
PrivateKey = %s
# Address/DNS are assigned by Mullvad to your account key; set them from the
# Mullvad app if you use a fixed address.
[Peer]
PublicKey = %s
AllowedIPs = 0.0.0.0/0, ::/0
Endpoint = %s:%d
`, priv, relay.PublicKey, relay.IPv4, port)
	confPath = filepath.Join(c.dataDir, "mullvad-"+relay.Hostname+".conf")
	if err := os.WriteFile(confPath, []byte(conf), 0o600); err != nil {
		return "", false, err
	}
	wgQuick, lookErr := exec.LookPath("wg-quick")
	if lookErr != nil {
		return confPath, false, nil
	}
	cmd := exec.CommandContext(ctx, wgQuick, "up", confPath)
	if out, err := cmd.CombinedOutput(); err != nil {
		return confPath, false, fmt.Errorf("wg-quick up: %s: %w", strings.TrimSpace(string(out)), err)
	}
	return confPath, true, nil
}

// Down tears down the active mullvad interface if wg-quick is available.
func (c *Client) Down() error {
	wgQuick, err := exec.LookPath("wg-quick")
	if err != nil {
		return errors.New("wg-quick not available on this host")
	}
	entries, _ := os.ReadDir(c.dataDir)
	var lastErr error
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".conf") || e.Name() == "private.key" {
			continue
		}
		iface := strings.TrimSuffix(e.Name(), ".conf")
		if out, err := exec.Command(wgQuick, "down", iface).CombinedOutput(); err != nil {
			lastErr = fmt.Errorf("wg-quick down %s: %s: %w", iface, strings.TrimSpace(string(out)), err)
		}
	}
	return lastErr
}

// Status reports wg-quick availability and any active mullvad interface.
func (c *Client) Status() map[string]any {
	out := map[string]any{"wg_quick": false, "active": false, "interface": ""}
	if _, err := exec.LookPath("wg-quick"); err == nil {
		out["wg_quick"] = true
		if b, err := exec.Command("wg").CombinedOutput(); err == nil {
			for _, line := range strings.Split(string(b), "\n") {
				line = strings.TrimSpace(line)
				// Any active interface counts — arkexwg0 (kernel kill-switch mode) as well as mullvad-*.
				if iface, ok := strings.CutPrefix(line, "interface: "); ok && iface != "" {
					out["active"] = true
					out["interface"] = iface
				}
			}
		}
	}
	return out
}
