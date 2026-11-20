// Package traffic provides the MITM HTTP proxy and compressed traffic store.
package traffic

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/threatvector/arkex/storage"
)

const (
	maxFTSBytes = 64 * 1024 // 64 KiB cap for FTS indexing
	scannerRate = 20        // req/s threshold for scanner detection
)

var staticMIMEPrefixes = []string{"image/", "video/", "audio/", "font/"}

// Store persists captured HTTP traffic with deduplication and compression.
type Store struct {
	db  *pgxpool.Pool
	cas *storage.CAS
}

func NewStore(db *pgxpool.Pool, cas *storage.CAS) *Store {
	return &Store{db: db, cas: cas}
}

// RecordRequest stores a captured request/response pair.
func (s *Store) RecordRequest(ctx context.Context, req *CapturedRequest) error {
	// Dedup detection: check if same body hash already stored
	if req.RespBodySHA != "" {
		var exists bool
		_ = s.db.QueryRow(ctx,
			`SELECT EXISTS(SELECT 1 FROM blobs WHERE sha256 = $1)`, req.RespBodySHA,
		).Scan(&exists)
		req.IsDedup = exists
	}

	// Store body in CAS
	var reqBodySHA, respBodySHA string
	var err error
	if len(req.ReqBody) > 0 {
		if reqBodySHA, err = s.cas.PutBody(ctx, req.ReqBody); err != nil {
			slog.Warn("cas put req body", "err", err)
		}
	}
	if len(req.RespBody) > 0 && !req.IsDedup {
		if respBodySHA, err = s.cas.PutBody(ctx, req.RespBody); err != nil {
			slog.Warn("cas put resp body", "err", err)
		}
	}

	var reqID string
	err = s.db.QueryRow(ctx, `
		INSERT INTO traffic_requests
			(session_id, engagement_id, method, url, host, path, status_code,
			 req_headers, req_body_sha, resp_headers, resp_body_sha, resp_body_len,
			 mime_type, is_scanner, is_static, is_dedup, latency_ms)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17)
		RETURNING id`,
		req.SessionID, req.EngagementID, req.Method, req.URL, req.Host, req.Path,
		req.StatusCode, req.ReqHeaders, reqBodySHA,
		req.RespHeaders, respBodySHA, len(req.RespBody),
		req.MIMEType, req.IsScanner, req.IsStatic, req.IsDedup,
		req.LatencyMS,
	).Scan(&reqID)
	if err != nil {
		return fmt.Errorf("traffic record: %w", err)
	}

	// FTS index: only text responses ≤ 64 KiB, non-scanner, non-static
	if !req.IsScanner && !req.IsStatic && len(req.RespBody) <= maxFTSBytes && isTextMIME(req.MIMEType) {
		body := string(req.RespBody)
		if utf8.ValidString(body) {
			_, _ = s.db.Exec(ctx,
				`INSERT INTO traffic_fts (request_id, body_text) VALUES ($1, $2) ON CONFLICT DO NOTHING`,
				reqID, body)
		}
	}
	return nil
}

// Search performs full-text search over indexed traffic.
func (s *Store) Search(ctx context.Context, engID, query string, limit int) ([]*SearchResult, error) {
	rows, err := s.db.Query(ctx, `
		SELECT tr.id, tr.method, tr.url, tr.status_code, tr.created_at,
		       ts_headline('english', tf.body_text, plainto_tsquery('english', $3), 'MaxWords=20') AS snippet
		FROM traffic_fts tf
		JOIN traffic_requests tr ON tr.id = tf.request_id
		JOIN traffic_sessions ts ON ts.id = tr.session_id
		WHERE ts.engagement_id = $1
		  AND tf.tsv @@ plainto_tsquery('english', $3)
		ORDER BY ts_rank(tf.tsv, plainto_tsquery('english', $3)) DESC
		LIMIT $2`,
		engID, limit, query,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var results []*SearchResult
	for rows.Next() {
		r := &SearchResult{}
		if err := rows.Scan(&r.RequestID, &r.Method, &r.URL, &r.StatusCode, &r.CreatedAt, &r.Snippet); err != nil {
			return nil, err
		}
		results = append(results, r)
	}
	return results, rows.Err()
}

// isScanner detects scanner user agents and high-rate request patterns.
func isScanner(ua string) bool {
	scannerUAs := []string{"nuclei", "ffuf", "sqlmap", "nikto", "nmap", "masscan", "gobuster", "feroxbuster", "dirsearch"}
	ua = strings.ToLower(ua)
	for _, s := range scannerUAs {
		if strings.Contains(ua, s) {
			return true
		}
	}
	return false
}

func isStaticMIME(mime string) bool {
	for _, pfx := range staticMIMEPrefixes {
		if strings.HasPrefix(mime, pfx) {
			return true
		}
	}
	// Minified bundles
	return strings.Contains(mime, "javascript") && strings.HasSuffix(mime, ".min")
}

func isTextMIME(mime string) bool {
	return strings.HasPrefix(mime, "text/") ||
		strings.Contains(mime, "json") ||
		strings.Contains(mime, "xml") ||
		strings.Contains(mime, "javascript")
}

// CapturedRequest is a single intercepted HTTP exchange.
type CapturedRequest struct {
	SessionID    string
	EngagementID string
	Method       string
	URL          string
	Host         string
	Path         string
	StatusCode   int
	ReqHeaders   []byte
	ReqBody      []byte
	RespHeaders  []byte
	RespBody     []byte
	RespBodySHA  string
	MIMEType     string
	IsScanner    bool
	IsStatic     bool
	IsDedup      bool
	LatencyMS    int
	CapturedAt   time.Time
}

type SearchResult struct {
	RequestID  string
	Method     string
	URL        string
	StatusCode int
	CreatedAt  time.Time
	Snippet    string
}
