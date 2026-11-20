// Package server implements the ArKex 2.0 HTTP API and SSE event stream.
package server

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/threatvector/arkex/agent"
	"github.com/threatvector/arkex/storage"
)

// Server is the ArKex HTTP API server.
type Server struct {
	db      *pgxpool.Pool
	engine  *agent.Engine
	gov     *storage.Governor
	mux     *http.ServeMux
	version string
}

func New(db *pgxpool.Pool, eng *agent.Engine, gov *storage.Governor) *Server {
	s := &Server{
		db:      db,
		engine:  eng,
		gov:     gov,
		mux:     http.NewServeMux(),
		version: "2.0.0",
	}
	s.registerRoutes()
	return s
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// CORS for dev
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	s.mux.ServeHTTP(w, r)
}

func (s *Server) registerRoutes() {
	s.mux.HandleFunc("GET /api/health", s.handleHealth)
	s.mux.HandleFunc("GET /api/version", s.handleVersion)

	// Engagements
	s.mux.HandleFunc("GET /api/engagements", s.handleListEngagements)
	s.mux.HandleFunc("POST /api/engagements", s.handleCreateEngagement)
	s.mux.HandleFunc("GET /api/engagements/{id}", s.handleGetEngagement)
	s.mux.HandleFunc("POST /api/engagements/{id}/launch", s.handleLaunchEngagement)
	s.mux.HandleFunc("POST /api/engagements/{id}/pause", s.handlePauseEngagement)

	// Intents
	s.mux.HandleFunc("GET /api/engagements/{id}/intents", s.handleListIntents)
	s.mux.HandleFunc("POST /api/intents/{intentId}/steer", s.handleSteerWork)
	s.mux.HandleFunc("POST /api/intents/{intentId}/kill", s.handleKillWork)

	// Findings
	s.mux.HandleFunc("GET /api/engagements/{id}/findings", s.handleListFindings)
	s.mux.HandleFunc("POST /api/findings/{findingId}/retest", s.handleRetestFinding)

	// Approvals
	s.mux.HandleFunc("GET /api/intercepts", s.handleListIntercepts)
	s.mux.HandleFunc("POST /api/intercepts/{interceptId}/approve", s.handleApproveIntercept)
	s.mux.HandleFunc("POST /api/intercepts/{interceptId}/deny", s.handleDenyIntercept)

	// Storage
	s.mux.HandleFunc("GET /api/system/storage", s.handleStorageBreakdown)

	// SSE event stream
	s.mux.HandleFunc("GET /api/engagements/{id}/stream", s.handleSSEStream)

	// Companies
	s.mux.HandleFunc("GET /api/companies", s.handleListCompanies)
	s.mux.HandleFunc("POST /api/companies", s.handleCreateCompany)
}

// Listen starts the HTTP server on addr (e.g. ":8080").
func (s *Server) Listen(ctx context.Context, addr string) error {
	srv := &http.Server{
		Addr:         addr,
		Handler:      s,
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 90 * time.Second,
		IdleTimeout:  120 * time.Second,
	}
	slog.Info("arkex server listening", "addr", addr, "version", s.version)
	go func() {
		<-ctx.Done()
		shutCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutCtx)
	}()
	if err := srv.ListenAndServe(); err != http.ErrServerClosed {
		return err
	}
	return nil
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	var pgOK bool
	if s.db != nil {
		_ = s.db.QueryRow(r.Context(), "SELECT true").Scan(&pgOK)
	}
	status := "ok"
	code := http.StatusOK
	if !pgOK {
		status = "degraded"
		code = http.StatusServiceUnavailable
	}
	writeJSON(w, code, map[string]any{"status": status, "time": time.Now()})
}

func (s *Server) handleVersion(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"version": s.version})
}

func (s *Server) handleListEngagements(w http.ResponseWriter, r *http.Request) {
	rows, err := s.db.Query(r.Context(), `
		SELECT id, title, status, created_at, updated_at
		FROM engagements ORDER BY created_at DESC LIMIT 50`)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	defer rows.Close()
	var items []map[string]any
	for rows.Next() {
		var id, title, status string
		var ca, ua time.Time
		if err := rows.Scan(&id, &title, &status, &ca, &ua); err != nil {
			continue
		}
		items = append(items, map[string]any{"id": id, "title": title, "status": status, "created_at": ca, "updated_at": ua})
	}
	writeJSON(w, http.StatusOK, items)
}

func (s *Server) handleCreateEngagement(w http.ResponseWriter, r *http.Request) {
	var req struct {
		CompanyID string `json:"company_id"`
		Title     string `json:"title"`
		Goal      string `json:"goal"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	if len(req.Goal) < 80 {
		writeError(w, http.StatusBadRequest, fmt.Errorf("goal must be ≥80 chars (Goal Ambiguity Gate)"))
		return
	}
	var id string
	err := s.db.QueryRow(r.Context(), `
		INSERT INTO engagements (company_id, title, goal) VALUES ($1,$2,$3) RETURNING id`,
		req.CompanyID, req.Title, req.Goal,
	).Scan(&id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"id": id})
}

func (s *Server) handleGetEngagement(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	status, err := s.engine.GetEngagementStatus(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusNotFound, err)
		return
	}
	writeJSON(w, http.StatusOK, status)
}

func (s *Server) handleLaunchEngagement(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	// Set status to running
	_, err := s.db.Exec(r.Context(), `
		UPDATE engagements SET status = 'running', started_at = now() WHERE id = $1 AND status = 'pending'`, id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	if err := s.engine.LaunchEngagement(r.Context(), id); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"launched": true})
}

func (s *Server) handlePauseEngagement(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	_, err := s.db.Exec(r.Context(), `
		UPDATE engagements SET status = 'paused', updated_at = now() WHERE id = $1`, id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"paused": true})
}

func (s *Server) handleListIntents(w http.ResponseWriter, r *http.Request) {
	engID := r.PathValue("id")
	rows, err := s.db.Query(r.Context(), `
		SELECT id, kind, title, status, tokens_in+tokens_out, created_at
		FROM intents WHERE engagement_id = $1 ORDER BY created_at DESC LIMIT 100`, engID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	defer rows.Close()
	var items []map[string]any
	for rows.Next() {
		var id, kind, title, status string
		var tokens int64
		var ca time.Time
		if err := rows.Scan(&id, &kind, &title, &status, &tokens, &ca); err != nil {
			continue
		}
		items = append(items, map[string]any{"id": id, "kind": kind, "title": title, "status": status, "tokens": tokens, "created_at": ca})
	}
	writeJSON(w, http.StatusOK, items)
}

func (s *Server) handleSteerWork(w http.ResponseWriter, r *http.Request) {
	intentID := r.PathValue("intentId")
	var req struct{ Directive string `json:"directive"` }
	if !decodeJSON(w, r, &req) {
		return
	}
	if err := s.engine.SteerWork(r.Context(), intentID, req.Directive); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"steered": true})
}

func (s *Server) handleKillWork(w http.ResponseWriter, r *http.Request) {
	intentID := r.PathValue("intentId")
	if err := s.engine.KillWork(r.Context(), intentID); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"killed": true})
}

func (s *Server) handleListFindings(w http.ResponseWriter, r *http.Request) {
	engID := r.PathValue("id")
	q := r.URL.Query()
	severity := q.Get("severity")
	status := q.Get("status")

	query := `SELECT id, title, severity, status, verification, created_at FROM findings WHERE engagement_id = $1`
	args := []any{engID}
	if severity != "" {
		query += fmt.Sprintf(" AND severity = $%d", len(args)+1)
		args = append(args, severity)
	}
	if status != "" {
		query += fmt.Sprintf(" AND status = $%d", len(args)+1)
		args = append(args, status)
	}
	query += " ORDER BY created_at DESC LIMIT 100"

	rows, err := s.db.Query(r.Context(), query, args...)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	defer rows.Close()
	var items []map[string]any
	for rows.Next() {
		var id, title, sev, stat, verif string
		var ca time.Time
		if err := rows.Scan(&id, &title, &sev, &stat, &verif, &ca); err != nil {
			continue
		}
		items = append(items, map[string]any{"id": id, "title": title, "severity": sev, "status": stat, "verification": verif, "created_at": ca})
	}
	writeJSON(w, http.StatusOK, items)
}

func (s *Server) handleRetestFinding(w http.ResponseWriter, r *http.Request) {
	findingID := r.PathValue("findingId")
	var retestID string
	err := s.db.QueryRow(r.Context(), `
		INSERT INTO retests (finding_id, status) VALUES ($1, 'queued') RETURNING id`, findingID,
	).Scan(&retestID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"retest_id": retestID})
}

func (s *Server) handleListIntercepts(w http.ResponseWriter, r *http.Request) {
	rows, err := s.db.Query(r.Context(), `
		SELECT id, kind, payload, status, created_at
		FROM intercept_requests WHERE status = 'pending'
		ORDER BY created_at ASC LIMIT 50`)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	defer rows.Close()
	var items []map[string]any
	for rows.Next() {
		var id, kind, payload, status string
		var ca time.Time
		if err := rows.Scan(&id, &kind, &payload, &status, &ca); err != nil {
			continue
		}
		items = append(items, map[string]any{"id": id, "kind": kind, "payload": payload, "status": status, "created_at": ca})
	}
	writeJSON(w, http.StatusOK, items)
}

func (s *Server) handleApproveIntercept(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("interceptId")
	_, err := s.db.Exec(r.Context(), `
		UPDATE intercept_requests SET status = 'approved', decided_at = now() WHERE id = $1`, id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"approved": true})
}

func (s *Server) handleDenyIntercept(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("interceptId")
	_, err := s.db.Exec(r.Context(), `
		UPDATE intercept_requests SET status = 'denied', decided_at = now() WHERE id = $1`, id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"denied": true})
}

func (s *Server) handleStorageBreakdown(w http.ResponseWriter, r *http.Request) {
	breakdown, err := s.gov.GetBreakdown(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, breakdown)
}

func (s *Server) handleListCompanies(w http.ResponseWriter, r *http.Request) {
	rows, err := s.db.Query(r.Context(), `SELECT id, name, slug, created_at FROM companies ORDER BY name`)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	defer rows.Close()
	var items []map[string]any
	for rows.Next() {
		var id, name, slug string
		var ca time.Time
		if err := rows.Scan(&id, &name, &slug, &ca); err != nil {
			continue
		}
		items = append(items, map[string]any{"id": id, "name": name, "slug": slug, "created_at": ca})
	}
	writeJSON(w, http.StatusOK, items)
}

func (s *Server) handleCreateCompany(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name  string `json:"name"`
		Slug  string `json:"slug"`
		Scope string `json:"scope"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	var id string
	err := s.db.QueryRow(r.Context(), `
		INSERT INTO companies (name, slug, scope_raw) VALUES ($1,$2,$3) RETURNING id`,
		req.Name, req.Slug, req.Scope,
	).Scan(&id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"id": id})
}

// handleSSEStream delivers real-time server-sent events for an engagement.
func (s *Server) handleSSEStream(w http.ResponseWriter, r *http.Request) {
	engID := r.PathValue("id")

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming not supported", http.StatusInternalServerError)
		return
	}

	// Send initial ping
	fmt.Fprintf(w, "event: ping\ndata: {\"engagement_id\":%q}\n\n", engID)
	flusher.Flush()

	// Poll for new activity every 500ms
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()

	var lastIntentSeq int64
	for {
		select {
		case <-r.Context().Done():
			return
		case <-ticker.C:
			// Send new intents since last poll
			rows, err := s.db.Query(r.Context(), `
				SELECT id, kind, title, status, created_at
				FROM intents
				WHERE engagement_id = $1 AND id::text > $2
				ORDER BY created_at ASC LIMIT 10`,
				engID, fmt.Sprintf("%d", lastIntentSeq))
			if err != nil {
				continue
			}
			for rows.Next() {
				var id, kind, title, status string
				var ca time.Time
				if err := rows.Scan(&id, &kind, &title, &status, &ca); err != nil {
					continue
				}
				data, _ := json.Marshal(map[string]any{
					"id": id, "kind": kind, "title": title, "status": status, "created_at": ca,
				})
				fmt.Fprintf(w, "event: intent\ndata: %s\n\n", data)
				lastIntentSeq++
			}
			rows.Close()
			flusher.Flush()
		}
	}
}

// Helpers

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	if err := enc.Encode(v); err != nil {
		slog.Warn("json encode failed", "err", err)
	}
}

func writeError(w http.ResponseWriter, code int, err error) {
	writeJSON(w, code, map[string]any{"error": err.Error()})
}

func decodeJSON(w http.ResponseWriter, r *http.Request, v any) bool {
	if !strings.Contains(r.Header.Get("Content-Type"), "application/json") {
		writeError(w, http.StatusUnsupportedMediaType, fmt.Errorf("content-type must be application/json"))
		return false
	}
	if err := json.NewDecoder(r.Body).Decode(v); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return false
	}
	return true
}
