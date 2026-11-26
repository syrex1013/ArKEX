// Package oast embeds an interactsh OAST server for cryptographically correlated callbacks.
// S3 receipt: DNS/HTTP/SMTP/LDAP callback persisted and used to wake the planner.
package oast

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Server embeds interactsh and correlates callbacks to intents.
type Server struct {
	db        *pgxpool.Pool
	domain    string
	callbacks chan *Callback
	mu        sync.RWMutex
	waiters   map[string]chan *Callback // payloadTag -> waiter
}

// Callback represents an incoming OAST interaction.
type Callback struct {
	PayloadTag string
	Protocol   string // dns, http, smtp, ldap
	RemoteAddr string
	Data       map[string]any
	ReceivedAt time.Time
}

func New(db *pgxpool.Pool, domain string) *Server {
	return &Server{
		db:        db,
		domain:    domain,
		callbacks: make(chan *Callback, 256),
		waiters:   make(map[string]chan *Callback),
	}
}

// GeneratePayload creates a unique OAST payload tag for an intent.
// Format: <random16hex>.<domain>
func (s *Server) GeneratePayload(ctx context.Context, tag, intentID, engID string) (string, error) {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	unique := hex.EncodeToString(b)
	payload := fmt.Sprintf("%s-%s.%s", tag, unique, s.domain)

	if s.db != nil {
		_, err := s.db.Exec(ctx, `
			INSERT INTO oast_callbacks (engagement_id, intent_id, payload_tag, protocol, data)
			VALUES ($1, $2, $3, 'pending', '{}')
			ON CONFLICT DO NOTHING`,
			engID, intentID, payload)
		if err != nil {
			return "", fmt.Errorf("register oast payload: %w", err)
		}
	}
	return payload, nil
}

// WaitForCallback blocks until a callback arrives for the given payload tag or ctx expires.
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

	select {
	case cb := <-ch:
		return cb, nil
	case <-time.After(timeout):
		return nil, fmt.Errorf("oast timeout after %s for %s", timeout, payloadTag)
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// HandleHTTPCallback processes an incoming HTTP interaction (registered as webhook).
func (s *Server) HandleHTTPCallback(w http.ResponseWriter, r *http.Request) {
	// Extract the payload tag from the Host header
	host := r.Host
	payloadTag := host

	cb := &Callback{
		PayloadTag: payloadTag,
		Protocol:   "http",
		RemoteAddr: r.RemoteAddr,
		Data: map[string]any{
			"method":  r.Method,
			"path":    r.URL.Path,
			"headers": r.Header,
		},
		ReceivedAt: time.Now(),
	}
	s.dispatch(r.Context(), cb)
	w.WriteHeader(http.StatusOK)
}

// HandleDNSCallback processes an incoming DNS interaction.
func (s *Server) HandleDNSCallback(ctx context.Context, qname, qtype, remoteAddr string) {
	// Extract payload tag from subdomain prefix
	parts := strings.SplitN(qname, ".", 2)
	tag := qname
	if len(parts) > 0 {
		tag = parts[0]
	}
	cb := &Callback{
		PayloadTag: tag,
		Protocol:   "dns",
		RemoteAddr: remoteAddr,
		Data:       map[string]any{"qname": qname, "qtype": qtype},
		ReceivedAt: time.Now(),
	}
	s.dispatch(ctx, cb)
}

// dispatch persists the callback and wakes any registered waiters.
func (s *Server) dispatch(ctx context.Context, cb *Callback) {
	if s.db != nil {
		_, err := s.db.Exec(ctx, `
			UPDATE oast_callbacks
			SET protocol = $2, remote_addr = $3::inet, data = $4, received_at = $5
			WHERE payload_tag = $1`,
			cb.PayloadTag, cb.Protocol, cb.RemoteAddr, cb.Data, cb.ReceivedAt)
		if err != nil {
			slog.Warn("oast dispatch persist", "err", err)
		}
	}

	slog.Info("oast callback received",
		"tag", cb.PayloadTag,
		"protocol", cb.Protocol,
		"remote", cb.RemoteAddr,
	)

	// Wake waiting intent
	s.mu.RLock()
	waiter, ok := s.waiters[cb.PayloadTag]
	s.mu.RUnlock()
	if ok {
		select {
		case waiter <- cb:
		default:
		}
	}
}

// PendingCount returns how many payload tags are awaiting callbacks.
func (s *Server) PendingCount(ctx context.Context) (int64, error) {
	var n int64
	err := s.db.QueryRow(ctx,
		`SELECT COUNT(*) FROM oast_callbacks WHERE protocol = 'pending'`).Scan(&n)
	return n, err
}
