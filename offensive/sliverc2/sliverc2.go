// Package sliverc2 embeds the Sliver operator console core: sessions, beacons,
// implant generation, command execution and live events over the operator
// gRPC API (github.com/bishopfox/sliver v1.7.9).
package sliverc2

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/bishopfox/sliver/client/assets"
	"github.com/bishopfox/sliver/client/transport"
	"github.com/bishopfox/sliver/protobuf/clientpb"
	"github.com/bishopfox/sliver/protobuf/commonpb"
	"github.com/bishopfox/sliver/protobuf/rpcpb"
	"github.com/bishopfox/sliver/protobuf/sliverpb"
	"google.golang.org/grpc"
)

// Manager holds one operator connection. A Sliver server is external: point it
// at an operator config produced by `sliver multiplayer new-operator --name arkex`.
type Manager struct {
	mu       sync.Mutex
	confPath string
	rpc      rpcpb.SliverRPCClient
	conn     *grpc.ClientConn
}

func New(confPath string) *Manager { return &Manager{confPath: confPath} }

// ConfigPath returns the operator config in use.
func (m *Manager) ConfigPath() string { return m.confPath }

// Connected reports whether an operator session is live.
func (m *Manager) Connected() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.rpc != nil
}

// Connect loads the operator config and dials the Sliver server.
func (m *Manager) Connect(ctx context.Context) error {
	if _, err := os.Stat(m.confPath); err != nil {
		return fmt.Errorf("operator config not found: %w", err)
	}
	conf, err := assets.ReadConfig(m.confPath)
	if err != nil {
		return fmt.Errorf("read operator config: %w", err)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.closeLocked()
	rpc, conn, err := transport.MTLSConnect(conf)
	if err != nil {
		return fmt.Errorf("sliver connect: %w", err)
	}
	// Fail fast when the server is unreachable rather than on first call later.
	vctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if _, err := rpc.GetVersion(vctx, &commonpb.Empty{}); err != nil {
		_ = conn.Close()
		return fmt.Errorf("sliver version probe: %w", err)
	}
	m.rpc, m.conn = rpc, conn
	return nil
}

// Disconnect closes the operator session.
func (m *Manager) Disconnect() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.closeLocked()
}

func (m *Manager) closeLocked() error {
	if m.conn == nil {
		return nil
	}
	err := m.conn.Close()
	m.rpc, m.conn = nil, nil
	return err
}

func (m *Manager) client() (rpcpb.SliverRPCClient, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.rpc == nil {
		return nil, errors.New("not connected: POST /api/c2/connect first")
	}
	return m.rpc, nil
}

// Target identifies a session or beacon: "session:<id>" or "beacon:<id>".
func parseTarget(target string) (kind, id string, err error) {
	kind, id, ok := strings.Cut(target, ":")
	if !ok || id == "" || (kind != "session" && kind != "beacon") {
		return "", "", fmt.Errorf("target must be session:<id> or beacon:<id>, got %q", target)
	}
	return kind, id, nil
}

// unixOrEmpty renders a Sliver unix timestamp, "" when unset.
func unixOrEmpty(unix int64) string {
	if unix <= 0 {
		return ""
	}
	return time.Unix(unix, 0).UTC().Format(time.RFC3339)
}

// Sessions lists live interactive sessions.
func (m *Manager) Sessions(ctx context.Context) ([]map[string]any, error) {
	rpc, err := m.client()
	if err != nil {
		return nil, err
	}
	res, err := rpc.GetSessions(ctx, &commonpb.Empty{})
	if err != nil {
		return nil, err
	}
	out := make([]map[string]any, 0, len(res.Sessions))
	for _, s := range res.Sessions {
		out = append(out, map[string]any{
			"id": s.ID, "name": s.Name, "hostname": s.Hostname,
			"username": s.Username, "os": s.OS, "arch": s.Arch,
			"remote": s.RemoteAddress, "uuid": s.UUID,
			"alive": !s.IsDead, "last_seen": unixOrEmpty(s.LastCheckin),
		})
	}
	return out, nil
}

// Beacons lists asynchronous beacons.
func (m *Manager) Beacons(ctx context.Context) ([]map[string]any, error) {
	rpc, err := m.client()
	if err != nil {
		return nil, err
	}
	res, err := rpc.GetBeacons(ctx, &commonpb.Empty{})
	if err != nil {
		return nil, err
	}
	out := make([]map[string]any, 0, len(res.Beacons))
	for _, b := range res.Beacons {
		out = append(out, map[string]any{
			"id": b.ID, "name": b.Name, "hostname": b.Hostname,
			"username": b.Username, "os": b.OS, "arch": b.Arch,
			"remote": b.RemoteAddress, "alive": !b.IsDead,
			"interval": b.Interval, "last_seen": unixOrEmpty(b.LastCheckin),
		})
	}
	return out, nil
}

// BeaconTasks lists a beacon's task history.
func (m *Manager) BeaconTasks(ctx context.Context, beaconID string) ([]map[string]any, error) {
	rpc, err := m.client()
	if err != nil {
		return nil, err
	}
	res, err := rpc.GetBeaconTasks(ctx, &clientpb.Beacon{ID: beaconID})
	if err != nil {
		return nil, err
	}
	out := make([]map[string]any, 0, len(res.Tasks))
	for _, t := range res.Tasks {
		out = append(out, map[string]any{
			"id": t.ID, "description": t.Description, "state": t.State,
			"sent_at": unixOrEmpty(t.SentAt), "completed_at": unixOrEmpty(t.CompletedAt),
		})
	}
	return out, nil
}

// Exec runs a command on a session (blocking) or beacon (timeout-bounded).
func (m *Manager) Exec(ctx context.Context, target, path string, args []string, timeout time.Duration) (map[string]any, error) {
	rpc, err := m.client()
	if err != nil {
		return nil, err
	}
	kind, id, err := parseTarget(target)
	if err != nil {
		return nil, err
	}
	if timeout <= 0 {
		timeout = 60 * time.Second
	}
	req := &sliverpb.ExecuteReq{
		Path:   path,
		Args:   args,
		Output: true,
		Request: &commonpb.Request{
			Timeout: int64(timeout),
			SessionID: map[string]string{
				"session": id,
			}[kind],
			BeaconID: map[string]string{
				"beacon": id,
			}[kind],
		},
	}
	res, err := rpc.Execute(ctx, req)
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"target": target,
		"status": res.Status,
		"stdout": string(res.Stdout),
		"stderr": string(res.Stderr),
	}, nil
}

// Kill removes a session (Terminate) or beacon (RmBeacon).
func (m *Manager) Kill(ctx context.Context, target string) error {
	rpc, err := m.client()
	if err != nil {
		return err
	}
	kind, id, err := parseTarget(target)
	if err != nil {
		return err
	}
	switch kind {
	case "session":
		_, err = rpc.Kill(ctx, &sliverpb.KillReq{
			Force:   true,
			Request: &commonpb.Request{SessionID: id, Timeout: int64(60 * time.Second)},
		})
	case "beacon":
		_, err = rpc.RmBeacon(ctx, &clientpb.Beacon{ID: id})
	}
	return err
}

// GenerateOptions configures an implant build.
type GenerateOptions struct {
	GOOS      string   `json:"goos"`
	GOARCH    string   `json:"goarch"`
	Format    string   `json:"format"` // shared_lib|exe|shared-service|shellcode
	Name      string   `json:"name"`
	C2URLs    []string `json:"c2_urls"` // mtls://host:port, wg://..., dns://..., http(s)://...
	IsBeacon  bool     `json:"is_beacon"`
	Interval  int64    `json:"interval_seconds"`
	Retry     int64    `json:"retry_seconds"`
	OutputDir string   `json:"-"`
}

var formatByName = map[string]clientpb.OutputFormat{
	"shared-lib":     clientpb.OutputFormat_SHARED_LIB,
	"exe":            clientpb.OutputFormat_EXECUTABLE,
	"shared-service": clientpb.OutputFormat_SERVICE,
	"shellcode":      clientpb.OutputFormat_SHELLCODE,
}

// Generate builds an implant via the Sliver server compiler and saves it.
func (m *Manager) Generate(ctx context.Context, opts GenerateOptions) (string, error) {
	rpc, err := m.client()
	if err != nil {
		return "", err
	}
	format, ok := formatByName[opts.Format]
	if !ok {
		return "", fmt.Errorf("format must be one of shared-lib|exe|shared-service|shellcode")
	}
	interval := opts.Interval
	if interval <= 0 {
		interval = 60
	}
	retry := opts.Retry
	if retry <= 0 {
		retry = 60
	}
	c2 := make([]*clientpb.ImplantC2, 0, len(opts.C2URLs))
	for i, u := range opts.C2URLs {
		c2 = append(c2, &clientpb.ImplantC2{URL: u, Priority: uint32(i + 1)})
	}
	cfg := &clientpb.ImplantConfig{
		GOOS:              opts.GOOS,
		GOARCH:            opts.GOARCH,
		Format:            format,
		IsBeacon:          opts.IsBeacon,
		BeaconInterval:    interval,
		ReconnectInterval: retry,
		C2:                c2,
		ObfuscateSymbols:  true,
	}
	res, err := rpc.Generate(ctx, &clientpb.GenerateReq{Config: cfg, Name: opts.Name})
	if err != nil {
		return "", fmt.Errorf("generate: %w", err)
	}
	dir := opts.OutputDir
	if dir == "" {
		dir, _ = os.Getwd()
	}
	path := filepath.Join(dir, res.ImplantName)
	if res.File != nil && len(res.File.Data) > 0 {
		if err := os.WriteFile(path, res.File.Data, 0o750); err != nil {
			return "", err
		}
	}
	log.Printf("[sliverc2] generated %s (%d bytes) for %s/%s", res.ImplantName, len(res.File.GetData()), opts.GOOS, opts.GOARCH)
	return path, nil
}

// Events streams server events (session/beacon connect-disconnect, jobs, task
// results) until ctx ends. Each event is a flat map for JSON transport.
func (m *Manager) Events(ctx context.Context, ch chan<- map[string]any) error {
	rpc, err := m.client()
	if err != nil {
		return err
	}
	stream, err := rpc.Events(ctx, &commonpb.Empty{})
	if err != nil {
		return err
	}
	for {
		ev, err := stream.Recv()
		if err != nil {
			return err
		}
		item := map[string]any{"type": ev.EventType}
		if ev.Session != nil {
			item["session"] = ev.Session.ID
			item["name"] = ev.Session.Name
		}
		if ev.Job != nil {
			item["job"] = ev.Job.ID
			item["protocol"] = ev.Job.Protocol
			item["port"] = ev.Job.Port
		}
		if ev.Client != nil {
			item["operator"] = ev.Client.Name
		}
		if len(ev.Data) > 0 {
			var data map[string]any
			if json.Unmarshal(ev.Data, &data) == nil {
				item["data"] = data
			} else {
				item["data"] = string(ev.Data)
			}
		}
		if ev.Err != "" {
			item["error"] = ev.Err
		}
		select {
		case ch <- item:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}
