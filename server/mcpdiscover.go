package server

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/Autumn-27/artex/db"
	"github.com/Autumn-27/artex/mcphttp"
	"github.com/Autumn-27/norma/mcp"
	actool "github.com/Autumn-27/norma/tool"
)

// mcpClient is the shared surface of a connected MCP server (stdio, Streamable HTTP,
// or legacy SSE),
// so tools/list and cleanup are handled uniformly regardless of transport.
type mcpClient interface {
	Tools(context.Context) ([]actool.CoreTool, error)
	Close() error
}

// connectMCP dials one MCP server per its transport. Callers must Close the client.
// proxyURL (may be empty) is forced onto http/sse transports for leak-proof task egress.
// proxyCA is the egress MITM CA those transports must trust when the proxy re-signs
// certificates; empty when the proxy is a plain forwarder (capture off).
func connectMCP(ctx context.Context, m *db.MCPServer, priv ExecPrivilege, proxyURL, proxyCA string) (mcpClient, error) {
	switch m.Transport {
	case "stdio":
		if m.Command == "" {
			return nil, fmt.Errorf("stdio transport is missing a command")
		}
		// The stdio command runs as the executor identity (see execpriv.go).
		command, args, err := execDropCommand(priv, m.Command, jsonStrSlice(m.Args))
		if err != nil {
			return nil, err
		}
		return mcp.NewStdioClient(ctx, m.Name, command, stdioEnvWithProxy(jsonStrMap(m.Env), proxyURL, proxyCA), args...)
	case "http":
		if m.URL == "" {
			return nil, fmt.Errorf("http transport is missing a URL")
		}
		// env map doubles as HTTP headers (e.g. Authorization).
		return mcphttp.New(ctx, m.Name, m.URL, jsonStrMap(m.Env), m.Insecure, m.TimeoutSeconds, proxyURL, proxyCA)
	case "sse":
		if m.URL == "" {
			return nil, fmt.Errorf("sse transport is missing a URL")
		}
		return mcphttp.NewSSE(ctx, m.Name, m.URL, jsonStrMap(m.Env), m.Insecure, proxyURL, proxyCA)
	default:
		return nil, fmt.Errorf("unknown transport %q", m.Transport)
	}
}

// stdioEnvWithProxy injects HTTP(S)/ALL_PROXY and the egress MITM CA into a stdio
// MCP tool's child environment so its own outbound requests follow the task egress
// contract and verify the re-signed certificates (Node needs NODE_EXTRA_CA_CERTS;
// a missing CA makes every npx MCP exit instead of answering tools/list).
//
// A socks5 proxy URL is never injected: Node's undici EnvHttpProxyAgent
// (NODE_USE_ENV_PROXY) rejects any scheme but http/https and crashes the process
// before it answers the MCP handshake, which reads as "connection closed". wgproxy
// sessions always advertise http://, so only an operator-configured socks5 global
// proxy lands here; under a kernel WireGuard tunnel a direct dial still exits
// through the tunnel (iptables kill-switch), so the child stays leak-proof there.
func stdioEnvWithProxy(env map[string]string, proxyURL, proxyCA string) map[string]string {
	if proxyURL == "" || strings.HasPrefix(proxyURL, "socks5://") || strings.HasPrefix(proxyURL, "socks5h://") {
		return env
	}
	if env == nil {
		env = map[string]string{}
	}
	for _, k := range []string{"HTTP_PROXY", "HTTPS_PROXY", "ALL_PROXY", "http_proxy", "https_proxy", "all_proxy"} {
		env[k] = proxyURL
	}
	env["NODE_USE_ENV_PROXY"] = "1"
	if proxyCA != "" {
		env["NODE_EXTRA_CA_CERTS"] = proxyCA
		env["SSL_CERT_FILE"] = proxyCA
	}
	return env
}

// discoverAndCacheMCP connects to one MCP, lists its tools, and persists the tool
// names to mcp_tools_cache so the UI shows them without a live connection.
func (s *Server) discoverAndCacheMCP(ctx context.Context, m *db.MCPServer) error {
	cl, err := connectMCP(ctx, m, s.execPriv, s.m.MCPTaskProxy(), s.m.MCPTaskCA())
	if err != nil {
		_ = s.m.pg.UpdateMCPHealth(m.ID, false, err.Error())
		return err
	}
	defer cl.Close()
	ts, err := cl.Tools(ctx)
	if err != nil {
		_ = s.m.pg.UpdateMCPHealth(m.ID, false, err.Error())
		return err
	}
	_ = s.m.pg.UpdateMCPHealth(m.ID, true, "")
	tools := make([]db.MCPTool, 0, len(ts))
	for _, t := range ts {
		tools = append(tools, db.MCPTool{Name: t.Name(), Description: t.Description()})
	}
	if err := s.m.pg.SaveMCPTools(m.ID, tools); err != nil {
		return err
	}
	log.Printf("[mcp] %s discovered %d tools and cached them", m.Name, len(tools))
	return nil
}

// discoverEmptyMCPsOnStartup fills the tool cache for any enabled MCP that has none
// yet (notably the seeded browser MCP on first run). Runs sequentially in one
// goroutine so we never spawn many stdio servers at once, and never blocks
// startup. Best-effort: a failure leaves the cache empty to retry next start.
//
// Agent assembly may connect the same server and populate the cache first; if so
// we skip, and a race that still fails after the cache is full is treated as
// success so it does not look like a real outage in server_logs.
func (s *Server) discoverEmptyMCPsOnStartup() {
	servers, err := s.m.pg.ListMCP()
	if err != nil {
		log.Printf("[mcp] startup auto-discovery: failed to read the list: %v", err)
		return
	}
	for _, m := range servers {
		if !m.Enabled || len(m.Tools) > 0 {
			continue
		}
		// Another path (agent assemble / refresh) may have filled the cache since ListMCP.
		if names, _ := s.m.pg.MCPToolNames(m.ID); len(names) > 0 {
			continue
		}
		ctx, cancel := context.WithTimeout(s.ctx, 90*time.Second)
		err := s.discoverAndCacheMCP(ctx, m)
		cancel()
		if err == nil {
			continue
		}
		if names, _ := s.m.pg.MCPToolNames(m.ID); len(names) > 0 {
			continue // raced a successful connect; cache is warm
		}
		log.Printf("[mcp] startup auto-discovery of %s failed: %v", m.Name, err)
	}
}
