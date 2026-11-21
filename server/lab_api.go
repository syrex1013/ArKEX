package server

import (
	"crypto/sha256"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Autumn-27/artex/offensive/authmatrix"
	"github.com/Autumn-27/artex/offensive/oast"
	"github.com/Autumn-27/artex/offensive/zeroday"
)

func (s *Server) oastServer() *oast.Server {
	if s.oast != nil {
		return s.oast
	}
	if s.m == nil || s.m.pg == nil {
		return oast.New(nil, "")
	}
	s.oast = oast.New(s.m.pg.DB, "")
	s.oast.SetHitHook(func(cb *oast.Callback) {
		select {
		case s.archiveWake <- struct{}{}:
		default:
		}
	})
	return s.oast
}

func (s *Server) labPipeline() *zeroday.Pipeline {
	if s.labPipe != nil {
		return s.labPipe
	}
	if s.m == nil || s.m.pg == nil {
		return zeroday.New(nil, "")
	}
	dir := filepath.Join(s.m.dir, "lab")
	_ = os.MkdirAll(dir, 0o750)
	s.labPipe = zeroday.New(s.m.pg.DB, dir)
	return s.labPipe
}

// POST /api/lab/desync — run HTTP desync/smuggling probes against a target URL.
func (s *Server) labDesync(w http.ResponseWriter, r *http.Request) {
	var req struct {
		URL    string `json:"url"`
		TaskID string `json:"task_id"`
	}
	if err := decode(w, r, &req); err != nil || strings.TrimSpace(req.URL) == "" {
		writeErr(w, 400, "url required")
		return
	}
	if !looksLikeHTTPURL(req.URL) {
		writeErr(w, 400, "url must be http(s)")
		return
	}
	results, err := s.desyncProbe(r.Context(), req.URL, req.TaskID)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"results": results})
}

// POST /api/lab/cpg/index — index a local source tree for taint sinks.
func (s *Server) labCPGIndex(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Path     string `json:"path"`
		TaskID   string `json:"task_id"`
		Language string `json:"language"`
	}
	if err := decode(w, r, &req); err != nil || strings.TrimSpace(req.Path) == "" {
		writeErr(w, 400, "path required")
		return
	}
	if s.m == nil {
		writeErr(w, 503, "manager unavailable")
		return
	}
	path := req.Path
	if !filepath.IsAbs(path) {
		path = filepath.Join(s.m.dir, "lab", path)
	}
	if !pathUnder(s.m.dir, path) {
		if os.Getenv("ARKEX_LAB_ALLOW_ABS") != "1" {
			writeErr(w, 403, "path must be under data dir unless ARKEX_LAB_ALLOW_ABS=1")
			return
		}
	}
	sinks, err := s.labPipeline().IndexSource(r.Context(), req.TaskID, path, req.Language)
	if err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"sinks": sinks, "count": len(sinks)})
}

// GET /api/lab/cpg/sinks?task=...
func (s *Server) labCPGSinks(w http.ResponseWriter, r *http.Request) {
	task := r.URL.Query().Get("task")
	if task == "" {
		writeErr(w, 400, "task required")
		return
	}
	sinks, err := s.labPipeline().ListSinks(r.Context(), task, 200)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"sinks": sinks})
}

// POST /api/lab/fuzz — queue / start a fuzz job.
func (s *Server) labFuzz(w http.ResponseWriter, r *http.Request) {
	var req struct {
		TaskID     string `json:"task_id"`
		Kind       string `json:"kind"`
		TargetKind string `json:"target_kind"`
		Target     string `json:"target"`
	}
	if err := decode(w, r, &req); err != nil {
		writeErr(w, 400, "bad json")
		return
	}
	if req.Kind == "" {
		req.Kind = "afl++"
	}
	if req.Target == "" {
		writeErr(w, 400, "target required")
		return
	}
	id, err := s.labPipeline().LaunchFuzzer(r.Context(), req.TaskID, &zeroday.FuzzSpec{
		Kind: req.Kind, TargetKind: req.TargetKind, Target: req.Target,
	})
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 201, map[string]any{"job_id": id})
}

// POST /api/oast/payload — mint a callback payload for a task/intent.
func (s *Server) oastPayload(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Tag      string `json:"tag"`
		TaskID   string `json:"task_id"`
		IntentID string `json:"intent_id"`
	}
	if err := decode(w, r, &req); err != nil {
		writeErr(w, 400, "bad json")
		return
	}
	payload, err := s.oastServer().GeneratePayload(r.Context(), req.Tag, req.TaskID, req.IntentID)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{
		"payload": payload,
		"domain":  s.oastServer().Domain(),
		"hint":    "Use in SSRF/RCE blind probes; HTTP callbacks: POST /api/oast/callback",
	})
}

// GET /api/oast/callbacks
func (s *Server) oastList(w http.ResponseWriter, r *http.Request) {
	task := r.URL.Query().Get("task")
	items, err := s.oastServer().ListRecent(r.Context(), task, 100)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	pending, _ := s.oastServer().PendingCount(r.Context())
	writeJSON(w, 200, map[string]any{"callbacks": items, "pending": pending})
}

// POST /api/oast/callback — ingest external interactsh / DNS hit (also used by HandleHTTPCallback host routing).
func (s *Server) oastIngest(w http.ResponseWriter, r *http.Request) {
	var req struct {
		PayloadTag string         `json:"payload_tag"`
		Protocol   string         `json:"protocol"`
		RemoteAddr string         `json:"remote_addr"`
		Data       map[string]any `json:"data"`
	}
	if err := decode(w, r, &req); err != nil || req.PayloadTag == "" {
		writeErr(w, 400, "payload_tag required")
		return
	}
	if req.Protocol == "" {
		req.Protocol = "http"
	}
	s.oastServer().Ingest(r.Context(), &oast.Callback{
		PayloadTag: req.PayloadTag,
		Protocol:   req.Protocol,
		RemoteAddr: req.RemoteAddr,
		Data:       req.Data,
		ReceivedAt: time.Now().UTC(),
	})
	writeJSON(w, 200, map[string]any{"ok": true})
}

// GET/POST /api/lab/auth-vault — list or set identity credentials for a task.
func (s *Server) labAuthVault(w http.ResponseWriter, r *http.Request) {
	pg := s.pg(w)
	if pg == nil {
		return
	}
	key := s.vaultKey()
	vault, err := authmatrix.New(pg.DB, key)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	switch r.Method {
	case http.MethodGet:
		task := r.URL.Query().Get("task")
		if task == "" {
			writeErr(w, 400, "task required")
			return
		}
		labels, err := vault.ListLabels(r.Context(), task)
		if err != nil {
			writeErr(w, 500, err.Error())
			return
		}
		writeJSON(w, 200, map[string]any{"identities": labels})
	case http.MethodPost:
		var req struct {
			TaskID      string         `json:"task_id"`
			Label       string         `json:"label"`
			Kind        string         `json:"kind"`
			Credentials map[string]any `json:"credentials"`
		}
		if err := decode(w, r, &req); err != nil || req.TaskID == "" || req.Label == "" {
			writeErr(w, 400, "task_id, label required")
			return
		}
		if req.Kind == "" {
			req.Kind = "cookie"
		}
		if err := vault.SetIdentity(r.Context(), req.TaskID, req.Label, req.Kind, req.Credentials, nil); err != nil {
			writeErr(w, 400, err.Error())
			return
		}
		writeJSON(w, 201, map[string]any{"ok": true})
	default:
		writeErr(w, 405, "method not allowed")
	}
}

// POST /api/lab/auth-diff — differential GET across vault identities.
func (s *Server) labAuthDiff(w http.ResponseWriter, r *http.Request) {
	pg := s.pg(w)
	if pg == nil {
		return
	}
	var req struct {
		TaskID string   `json:"task_id"`
		URL    string   `json:"url"`
		Labels []string `json:"labels"`
	}
	if err := decode(w, r, &req); err != nil || req.TaskID == "" || req.URL == "" {
		writeErr(w, 400, "task_id and url required")
		return
	}
	if !looksLikeHTTPURL(req.URL) {
		writeErr(w, 400, "url must be http(s)")
		return
	}
	vault, err := authmatrix.New(pg.DB, s.vaultKey())
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	cands, err := authmatrix.NewAnalyzer(vault, s.m.MCPTaskProxy()).DiffGET(r.Context(), req.TaskID, req.URL, req.Labels)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"candidates": cands})
}

func (s *Server) vaultKey() []byte {
	// Derive a stable 32-byte key from the JWT key material already on disk.
	sum := sha256.Sum256(s.jwtKey)
	return sum[:]
}

func looksLikeHTTPURL(u string) bool {
	u = strings.ToLower(u)
	return strings.HasPrefix(u, "http://") || strings.HasPrefix(u, "https://")
}

func pathUnder(root, target string) bool {
	absRoot, err1 := filepath.Abs(root)
	absTarget, err2 := filepath.Abs(target)
	if err1 != nil || err2 != nil {
		return false
	}
	rel, err := filepath.Rel(absRoot, absTarget)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}
