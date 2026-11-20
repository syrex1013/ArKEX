// Package authmatrix implements multi-identity auth vault and differential IDOR/BOLA detection.
package authmatrix

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Identity labels
const (
	LabelUnauth = "unauth"
	LabelUserA  = "user_a"
	LabelUserB  = "user_b"
	LabelAdmin  = "admin"
)

// Vault manages per-engagement auth identities with encrypted credential storage.
type Vault struct {
	db        *pgxpool.Pool
	encKey    []byte // 32-byte AES-256 key
}

func New(db *pgxpool.Pool, encKey []byte) (*Vault, error) {
	if len(encKey) != 32 {
		return nil, fmt.Errorf("enc key must be 32 bytes, got %d", len(encKey))
	}
	return &Vault{db: db, encKey: encKey}, nil
}

// SetIdentity stores or updates credentials for a labeled identity.
func (v *Vault) SetIdentity(ctx context.Context, engID, label, kind string, credentials map[string]any, expiresAt *time.Time) error {
	credJSON, err := json.Marshal(credentials)
	if err != nil {
		return err
	}
	encrypted, err := v.encrypt(credJSON)
	if err != nil {
		return err
	}
	_, err = v.db.Exec(ctx, `
		INSERT INTO auth_identities (engagement_id, label, kind, credentials, expires_at)
		VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (engagement_id, label)
		DO UPDATE SET kind = $3, credentials = $4, expires_at = $5, updated_at = now()`,
		engID, label, kind, encrypted, expiresAt,
	)
	return err
}

// GetCredentials retrieves and decrypts credentials for a labeled identity.
func (v *Vault) GetCredentials(ctx context.Context, engID, label string) (map[string]any, error) {
	var encrypted []byte
	err := v.db.QueryRow(ctx,
		`SELECT credentials FROM auth_identities WHERE engagement_id = $1 AND label = $2`,
		engID, label,
	).Scan(&encrypted)
	if err != nil {
		return nil, fmt.Errorf("get identity %s: %w", label, err)
	}
	decrypted, err := v.decrypt(encrypted)
	if err != nil {
		return nil, err
	}
	var creds map[string]any
	if err := json.Unmarshal(decrypted, &creds); err != nil {
		return nil, err
	}
	return creds, nil
}

// IDORAnalyzer replays safe read endpoints across identities to detect access control issues.
type IDORAnalyzer struct {
	db    *pgxpool.Pool
	vault *Vault
}

func NewIDORAnalyzer(db *pgxpool.Pool, vault *Vault) *IDORAnalyzer {
	return &IDORAnalyzer{db: db, vault: vault}
}

// AnalyzeEndpoint replays a request across all identities and computes structural diffs.
func (a *IDORAnalyzer) AnalyzeEndpoint(ctx context.Context, engID, requestID string) ([]*IDORCandidate, error) {
	// Load the baseline request
	var method, url string
	err := a.db.QueryRow(ctx,
		`SELECT method, url FROM traffic_requests WHERE id = $1`, requestID,
	).Scan(&method, &url)
	if err != nil {
		return nil, fmt.Errorf("load baseline request: %w", err)
	}

	// Only analyze GET/HEAD/OPTIONS (safe methods for replay)
	if !isSafeMethod(method) {
		return nil, nil
	}

	// Get all identities for this engagement
	rows, err := a.db.Query(ctx,
		`SELECT label, kind FROM auth_identities WHERE engagement_id = $1`, engID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	type identity struct{ label, kind string }
	var identities []identity
	for rows.Next() {
		var id identity
		if err := rows.Scan(&id.label, &id.kind); err != nil {
			return nil, err
		}
		identities = append(identities, id)
	}

	if len(identities) < 2 {
		return nil, nil // need at least 2 identities to diff
	}

	// Replay and collect responses
	type response struct {
		label string
		body  string
	}
	var responses []response
	for _, id := range identities {
		creds, err := a.vault.GetCredentials(ctx, engID, id.label)
		if err != nil {
			slog.Warn("idor: get creds failed", "label", id.label, "err", err)
			continue
		}
		body, err := replayRequest(ctx, method, url, id.kind, creds)
		if err != nil {
			slog.Warn("idor: replay failed", "label", id.label, "err", err)
			continue
		}
		responses = append(responses, response{label: id.label, body: body})
	}

	// Compute structural diffs between identity pairs
	var candidates []*IDORCandidate
	for i := 0; i < len(responses)-1; i++ {
		for j := i + 1; j < len(responses); j++ {
			diff := structuralDiff(responses[i].body, responses[j].body)
			if diff > 0.1 { // 10% structural difference threshold
				candidate := &IDORCandidate{
					EngagementID: engID,
					RequestID:    requestID,
					BaselineID:   responses[i].label,
					ComparedID:   responses[j].label,
					DiffScore:    diff,
					DiffSummary:  fmt.Sprintf("%.0f%% structural difference between %s and %s responses", diff*100, responses[i].label, responses[j].label),
				}
				candidates = append(candidates, candidate)

				// Persist candidate
				_, _ = a.db.Exec(ctx, `
					INSERT INTO idor_candidates
						(engagement_id, request_id, baseline_id, compared_id, diff_score, diff_summary)
					VALUES ($1,$2,$3,$4,$5,$6)`,
					engID, requestID, responses[i].label, responses[j].label,
					candidate.DiffScore, candidate.DiffSummary)
			}
		}
	}
	return candidates, nil
}

type IDORCandidate struct {
	EngagementID string
	RequestID    string
	BaselineID   string
	ComparedID   string
	DiffScore    float64
	DiffSummary  string
	FindingID    *string
}

// replayRequest sends a request with the given identity's credentials.
func replayRequest(ctx context.Context, method, url, credKind string, creds map[string]any) (string, error) {
	// In production: use uTLS client routed through the MITM proxy
	// Here: placeholder returning empty string (real impl in HTTP transport layer)
	_ = ctx
	_ = method
	_ = url
	_ = credKind
	_ = creds
	return "", nil
}

// structuralDiff computes a rough normalized edit distance between two JSON response bodies.
func structuralDiff(a, b string) float64 {
	if a == b {
		return 0
	}
	if len(a) == 0 || len(b) == 0 {
		return 1.0
	}
	// Rough key-count diff as a proxy for structural difference
	aKeys := countJSONKeys(a)
	bKeys := countJSONKeys(b)
	if aKeys == 0 && bKeys == 0 {
		return levenshteinRatio(a, b)
	}
	diff := float64(abs(aKeys-bKeys)) / float64(max(aKeys, bKeys))
	return diff
}

func countJSONKeys(s string) int {
	return strings.Count(s, `":"`) + strings.Count(s, `": "`)
}

func levenshteinRatio(a, b string) float64 {
	maxLen := len(a)
	if len(b) > maxLen {
		maxLen = len(b)
	}
	if maxLen == 0 {
		return 0
	}
	// Simplified: use length difference as proxy
	return float64(abs(len(a)-len(b))) / float64(maxLen)
}

func isSafeMethod(method string) bool {
	return method == "GET" || method == "HEAD" || method == "OPTIONS"
}

func abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// encrypt encrypts data using AES-256-GCM.
func (v *Vault) encrypt(plaintext []byte) ([]byte, error) {
	block, err := aes.NewCipher(v.encKey)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, err
	}
	return gcm.Seal(nonce, nonce, plaintext, nil), nil
}

// decrypt decrypts AES-256-GCM ciphertext.
func (v *Vault) decrypt(ciphertext []byte) ([]byte, error) {
	block, err := aes.NewCipher(v.encKey)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	nonceSize := gcm.NonceSize()
	if len(ciphertext) < nonceSize {
		return nil, fmt.Errorf("ciphertext too short")
	}
	nonce, ct := ciphertext[:nonceSize], ciphertext[nonceSize:]
	return gcm.Open(nil, nonce, ct, nil)
}
