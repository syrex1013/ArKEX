// Package oast provides cryptographically tagged OOB callback correlation (S3 receipts).
package oast

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"
)

// Callback is one inbound OAST interaction.
type Callback struct {
	PayloadTag string
	Protocol   string
	RemoteAddr string
	Data       map[string]any
	ReceivedAt time.Time
}

// Server correlates payload tags to callbacks and wakes waiters.
type Server struct {
	db      *sql.DB
	domain  string
	mu      sync.RWMutex
	waiters map[string]chan *Callback
	onHit   func(cb *Callback) // optional planner wakeup hook
}

// New builds an OAST correlator. Domain defaults to ARKEX_OAST_DOMAIN or oast.local.
func New(db *sql.DB, domain string) *Server {
	if domain == "" {
		domain = strings.TrimSpace(os.Getenv("ARKEX_OAST_DOMAIN"))
	}
	if domain == "" {
		domain = "oast.local"
	}
	return &Server{
		db:      db,
		domain:  domain,
		waiters: map[string]chan *Callback{},
	}
}

// SetHitHook registers a callback invoked on every correlated hit (planner wake).
func (s *Server) SetHitHook(fn func(*Callback)) { s.onHit = fn }

// Domain returns the configured callback domain.
func (s *Server) Domain() string { return s.domain }

// GeneratePayload registers a unique tag for task/intent and returns host payload.
func (s *Server) GeneratePayload(ctx context.Context, tag, taskID, intentID string) (string, error) {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	unique := hex.EncodeToString(b)
	if tag == "" {
		tag = "arkex"
	}
	payload := fmt.Sprintf("%s-%s.%s", tag, unique, s.domain)
	if s.db != nil {
		_, err := s.db.ExecContext(ctx, `
INSERT INTO oast_callbacks (task_id, intent_id, payload_tag, protocol, data)
VALUES ($1, $2, $3, 'pending', '{}')
ON CONFLICT (payload_tag) DO NOTHING`, taskID, intentID, payload)
		if err != nil {
			return "", fmt.Errorf("register oast payload: %w", err)
		}
	}
	return payload, nil
}

// WaitForCallback blocks until a hit arrives or timeout/ctx ends.
func (s *Server) WaitForCallback(ctx context.Context, payloadTag string, timeout time.Duration) (*Callback, error) {
	ch := make(chan *Callback, 1)
	s.mu.Lock()
	s.waiters[payloadTag] = ch
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		delete(s.waiters, payloadTag)
		s.mu.Unlock()
	}()
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case cb := <-ch:
		return cb, nil
	case <-timer.C:
		return nil, fmt.Errorf("oast timeout after %s for %s", timeout, payloadTag)
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// HandleHTTPCallback records an HTTP interaction from Host header.
func (s *Server) HandleHTTPCallback(w http.ResponseWriter, r *http.Request) {
	cb := &Callback{
		PayloadTag: r.Host,
		Protocol:   "http",
		RemoteAddr: r.RemoteAddr,
		Data: map[string]any{
			"method": r.Method,
			"path":   r.URL.Path,
		},
		ReceivedAt: time.Now().UTC(),
	}
	s.dispatch(r.Context(), cb)
	w.WriteHeader(http.StatusOK)
}

// Ingest records a callback from an external interactsh poller or DNS hook.
func (s *Server) Ingest(ctx context.Context, cb *Callback) {
	if cb.ReceivedAt.IsZero() {
		cb.ReceivedAt = time.Now().UTC()
	}
	s.dispatch(ctx, cb)
}

func (s *Server) dispatch(ctx context.Context, cb *Callback) {
	if s.db != nil {
		raw, _ := json.Marshal(cb.Data)
		_, err := s.db.ExecContext(ctx, `
UPDATE oast_callbacks
SET protocol = $2, remote_addr = $3, data = $4::jsonb, received_at = $5
WHERE payload_tag = $1 OR payload_tag = split_part($1, ':', 1)`,
			cb.PayloadTag, cb.Protocol, cb.RemoteAddr, string(raw), cb.ReceivedAt)
		if err != nil {
			slog.Warn("oast persist", "err", err)
		}
	}
	slog.Info("oast callback", "tag", cb.PayloadTag, "protocol", cb.Protocol, "remote", cb.RemoteAddr)
	s.mu.RLock()
	waiter, ok := s.waiters[cb.PayloadTag]
	s.mu.RUnlock()
	if ok {
		select {
		case waiter <- cb:
		default:
		}
	}
	if s.onHit != nil {
		s.onHit(cb)
	}
}

// ListRecent returns recent callbacks for a task (empty task = all).
func (s *Server) ListRecent(ctx context.Context, taskID string, limit int) ([]map[string]any, error) {
	if limit <= 0 {
		limit = 50
	}
	var rows *sql.Rows
	var err error
	if taskID == "" {
		rows, err = s.db.QueryContext(ctx, `
SELECT payload_tag, protocol, remote_addr, COALESCE(data::text,'{}'),
       COALESCE(received_at, created_at), task_id, intent_id
FROM oast_callbacks ORDER BY id DESC LIMIT $1`, limit)
	} else {
		rows, err = s.db.QueryContext(ctx, `
SELECT payload_tag, protocol, remote_addr, COALESCE(data::text,'{}'),
       COALESCE(received_at, created_at), task_id, intent_id
FROM oast_callbacks WHERE task_id = $1 ORDER BY id DESC LIMIT $2`, taskID, limit)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []map[string]any
	for rows.Next() {
		var tag, proto, remote, data, task, intent string
		var at time.Time
		if err := rows.Scan(&tag, &proto, &remote, &data, &at, &task, &intent); err != nil {
			return nil, err
		}
		out = append(out, map[string]any{
			"payload_tag": tag,
			"protocol":    proto,
			"remote_addr": remote,
			"data":        json.RawMessage(data),
			"received_at": at.UTC().Format(time.RFC3339),
			"task_id":     task,
			"intent_id":   intent,
		})
	}
	return out, rows.Err()
}

// PendingCount counts payloads still awaiting a hit.
func (s *Server) PendingCount(ctx context.Context) (int64, error) {
	var n int64
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM oast_callbacks WHERE protocol = 'pending'`).Scan(&n)
	return n, err
}
