package server

import (
	"context"
	"encoding/json"
	"path/filepath"
	"time"

	"github.com/Autumn-27/artex/agent"
	"github.com/Autumn-27/artex/offensive/authmatrix"
	"github.com/Autumn-27/artex/offensive/desync"
	"github.com/Autumn-27/artex/offensive/zeroday"
)

// The lab technique runners are shared by the HTTP handlers (operator views that
// still exist) and the agent LabTechniques adapter: one implementation, two
// callers.

func (s *Server) desyncProbe(ctx context.Context, url, taskID string) (any, error) {
	results, err := desync.New().ProbeAll(ctx, url)
	if err != nil {
		return nil, err
	}
	if s.m != nil && s.m.pg != nil {
		raw, _ := json.Marshal(results)
		_, _ = s.m.pg.Exec(`INSERT INTO lab_desync_runs (task_id, target_url, results) VALUES ($1,$2,$3::jsonb)`,
			taskID, url, string(raw))
	}
	return results, nil
}

// agentCPGPath resolves an agent-supplied source path: relative paths resolve
// under the data dir and the result must stay inside it — no env escape hatch
// for agents, unlike the operator endpoint.
func (s *Server) agentCPGPath(path string) (string, error) {
	if s.m == nil {
		return "", errManagerUnavailable
	}
	if !filepath.IsAbs(path) {
		path = filepath.Join(s.m.dir, path)
	}
	if !pathUnder(s.m.dir, path) {
		return "", errPathOutsideData
	}
	return path, nil
}

var (
	errManagerUnavailable = errStatic("manager unavailable")
	errPathOutsideData    = errStatic("path must stay under the server data directory")
)

type errStatic string

func (e errStatic) Error() string { return string(e) }

// labAgentTools adapts the server's lab techniques to agent.LabTechniques so
// workers invoke them directly instead of going through operator UI pages.
type labAgentTools struct{ s *Server }

func (l labAgentTools) DesyncProbe(ctx context.Context, url, taskID string) (any, error) {
	if !looksLikeHTTPURL(url) {
		return nil, errStatic("url must be http(s)")
	}
	return l.s.desyncProbe(ctx, url, taskID)
}

func (l labAgentTools) CPGIndex(ctx context.Context, taskID, path, language string) (any, error) {
	resolved, err := l.s.agentCPGPath(path)
	if err != nil {
		return nil, err
	}
	sinks, err := l.s.labPipeline().IndexSource(ctx, taskID, resolved, language)
	if err != nil {
		return nil, err
	}
	return map[string]any{"sinks": sinks, "count": len(sinks)}, nil
}

func (l labAgentTools) CPGSinks(ctx context.Context, taskID string) (any, error) {
	sinks, err := l.s.labPipeline().ListSinks(ctx, taskID, 200)
	if err != nil {
		return nil, err
	}
	return map[string]any{"sinks": sinks}, nil
}

func (l labAgentTools) LaunchFuzz(ctx context.Context, taskID, kind, target, targetKind string) (int64, error) {
	if kind == "" {
		kind = "afl++"
	}
	return l.s.labPipeline().LaunchFuzzer(ctx, taskID, &zeroday.FuzzSpec{
		Kind: kind, TargetKind: targetKind, Target: target,
	})
}

// --- OAST backend (agent.OASTBackend) ---

func (l labAgentTools) Domain() string { return l.s.oastServer().Domain() }

func (l labAgentTools) GeneratePayload(ctx context.Context, tag, taskID, intentID string) (string, error) {
	return l.s.oastServer().GeneratePayload(ctx, tag, taskID, intentID)
}

func (l labAgentTools) ListRecent(ctx context.Context, taskID string, limit int) (any, error) {
	return l.s.oastServer().ListRecent(ctx, taskID, limit)
}

func (l labAgentTools) WaitCallback(ctx context.Context, tag string, timeout time.Duration) (any, error) {
	return l.s.oastServer().WaitForCallback(ctx, tag, timeout)
}

func (l labAgentTools) HasCallback(ctx context.Context, tag string) (bool, error) {
	return l.s.oastServer().HasCallback(ctx, tag)
}

// --- auth-matrix diff (shared by the operator endpoint and the agent tool) ---

// authDiffGET runs the differential GET across the task vault's identities.
// One implementation, two callers: the operator HTTP endpoint and the agent tool.
func (s *Server) authDiffGET(ctx context.Context, taskID, url string, labels []string) (any, error) {
	if s.m == nil || s.m.pg == nil {
		return nil, errManagerUnavailable
	}
	if !looksLikeHTTPURL(url) {
		return nil, errStatic("url must be http(s)")
	}
	vault, err := authmatrix.New(s.m.pg.DB, s.vaultKey())
	if err != nil {
		return nil, err
	}
	return authmatrix.NewAnalyzer(vault, s.m.MCPTaskProxy()).DiffGET(ctx, taskID, url, labels)
}

func (l labAgentTools) AuthDiff(ctx context.Context, taskID, url string, labels []string) (any, error) {
	return l.s.authDiffGET(ctx, taskID, url, labels)
}

// labTools returns the agent-facing lab technique backend (never nil; tool
// calls degrade gracefully while s.m is missing).
func (s *Server) labTools() agent.LabTechniques { return labAgentTools{s} }

// oastAgentBackend returns the agent-facing OAST backend (never nil; tool
// calls degrade gracefully while s.m is missing).
func (s *Server) oastAgentBackend() agent.OASTBackend { return labAgentTools{s} }
