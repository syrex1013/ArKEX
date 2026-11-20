// Package authmatrix stores multi-identity credentials and diffs responses for IDOR/BOLA.
package authmatrix

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	LabelUnauth = "unauth"
	LabelUserA  = "user_a"
	LabelUserB  = "user_b"
	LabelAdmin  = "admin"
)

// Vault encrypts per-task identity credentials at rest.
type Vault struct {
	db     *sql.DB
	encKey []byte
}

// New requires a 32-byte AES key.
func New(db *sql.DB, encKey []byte) (*Vault, error) {
	if len(encKey) != 32 {
		return nil, fmt.Errorf("enc key must be 32 bytes, got %d", len(encKey))
	}
	return &Vault{db: db, encKey: encKey}, nil
}

// SetIdentity upserts encrypted credentials for a label on a task.
func (v *Vault) SetIdentity(ctx context.Context, taskID, label, kind string, credentials map[string]any, expiresAt *time.Time) error {
	raw, err := json.Marshal(credentials)
	if err != nil {
		return err
	}
	sealed, err := v.encrypt(raw)
	if err != nil {
		return err
	}
	_, err = v.db.ExecContext(ctx, `
INSERT INTO auth_identities (task_id, label, kind, credentials, expires_at)
VALUES ($1, $2, $3, $4, $5)
ON CONFLICT (task_id, label)
DO UPDATE SET kind = EXCLUDED.kind, credentials = EXCLUDED.credentials,
              expires_at = EXCLUDED.expires_at, updated_at = now()`,
		taskID, label, kind, sealed, expiresAt)
	return err
}

// GetCredentials decrypts credentials for a label.
func (v *Vault) GetCredentials(ctx context.Context, taskID, label string) (map[string]any, error) {
	var sealed []byte
	err := v.db.QueryRowContext(ctx,
		`SELECT credentials FROM auth_identities WHERE task_id = $1 AND label = $2`,
		taskID, label).Scan(&sealed)
	if err != nil {
		return nil, err
	}
	plain, err := v.decrypt(sealed)
	if err != nil {
		return nil, err
	}
	var out map[string]any
	if err := json.Unmarshal(plain, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// ListLabels returns identity labels configured for a task.
func (v *Vault) ListLabels(ctx context.Context, taskID string) ([]map[string]any, error) {
	rows, err := v.db.QueryContext(ctx, `
SELECT label, kind, expires_at, updated_at FROM auth_identities WHERE task_id = $1 ORDER BY label`, taskID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []map[string]any
	for rows.Next() {
		var label, kind string
		var exp sql.NullTime
		var updated time.Time
		if err := rows.Scan(&label, &kind, &exp, &updated); err != nil {
			return nil, err
		}
		item := map[string]any{"label": label, "kind": kind, "updated_at": updated.UTC().Format(time.RFC3339)}
		if exp.Valid {
			item["expires_at"] = exp.Time.UTC().Format(time.RFC3339)
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

// Candidate is a differential access finding across identities.
type Candidate struct {
	URL           string `json:"url"`
	Method        string `json:"method"`
	BaselineLabel string `json:"baseline_label"`
	OtherLabel    string `json:"other_label"`
	StatusBase    int    `json:"status_base"`
	StatusOther   int    `json:"status_other"`
	BodyDiffHint  string `json:"body_diff_hint"`
	Severity      string `json:"severity"`
}

// Analyzer replays a URL across identities with their Authorization/Cookie headers.
type Analyzer struct {
	vault  *Vault
	client *http.Client
}

func NewAnalyzer(v *Vault, proxyURL string) *Analyzer {
	tr := &http.Transport{DisableKeepAlives: true}
	if proxyURL != "" {
		if pu, err := url.Parse(proxyURL); err == nil && pu.Host != "" {
			tr.Proxy = http.ProxyURL(pu)
		}
	}
	return &Analyzer{vault: v, client: &http.Client{Timeout: 15 * time.Second, Transport: tr}}
}

// DiffGET fetches the same path with each identity and reports status/body-shape deltas.
func (a *Analyzer) DiffGET(ctx context.Context, taskID, rawURL string, labels []string) ([]Candidate, error) {
	if len(labels) == 0 {
		labels = []string{LabelUnauth, LabelUserA, LabelUserB, LabelAdmin}
	}
	type sample struct {
		label  string
		status int
		body   string
		err    string
	}
	samples := make([]sample, 0, len(labels))
	for _, label := range labels {
		st, body, err := a.fetch(ctx, taskID, label, rawURL)
		s := sample{label: label, status: st, body: body}
		if err != nil {
			s.err = err.Error()
		}
		samples = append(samples, s)
	}
	var out []Candidate
	for i := 0; i < len(samples); i++ {
		for j := i + 1; j < len(samples); j++ {
			a, b := samples[i], samples[j]
			if a.err != "" || b.err != "" {
				continue
			}
			if a.status == b.status && shape(a.body) == shape(b.body) {
				continue
			}
			sev := "medium"
			if (a.status == 200 && b.status == 403) || (a.status == 403 && b.status == 200) ||
				(a.status == 200 && b.status == 401) || (a.status == 401 && b.status == 200) {
				sev = "high"
			}
			out = append(out, Candidate{
				URL:           rawURL,
				Method:        http.MethodGet,
				BaselineLabel: a.label,
				OtherLabel:    b.label,
				StatusBase:    a.status,
				StatusOther:   b.status,
				BodyDiffHint:  fmt.Sprintf("%s≠%s", shape(a.body), shape(b.body)),
				Severity:      sev,
			})
		}
	}
	return out, nil
}

func (a *Analyzer) fetch(ctx context.Context, taskID, label, rawURL string) (int, string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return 0, "", err
	}
	if label != LabelUnauth {
		creds, err := a.vault.GetCredentials(ctx, taskID, label)
		if err != nil && err != sql.ErrNoRows {
			return 0, "", err
		}
		if err == nil {
			if c, ok := creds["cookie"].(string); ok && c != "" {
				req.Header.Set("Cookie", c)
			}
			if t, ok := creds["authorization"].(string); ok && t != "" {
				req.Header.Set("Authorization", t)
			}
			if t, ok := creds["bearer"].(string); ok && t != "" {
				req.Header.Set("Authorization", "Bearer "+t)
			}
		}
	}
	resp, err := a.client.Do(req)
	if err != nil {
		return 0, "", err
	}
	defer resp.Body.Close()
	buf := make([]byte, 4096)
	n, _ := io.ReadFull(resp.Body, buf)
	return resp.StatusCode, string(buf[:n]), nil
}

func shape(body string) string {
	b := strings.TrimSpace(body)
	if b == "" {
		return "empty"
	}
	if strings.HasPrefix(b, "{") {
		return fmt.Sprintf("json:%d", len(b))
	}
	if strings.HasPrefix(b, "<") {
		return fmt.Sprintf("html:%d", len(b))
	}
	return fmt.Sprintf("text:%d", len(b))
}

func (v *Vault) encrypt(plain []byte) ([]byte, error) {
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
	return gcm.Seal(nonce, nonce, plain, nil), nil
}

func (v *Vault) decrypt(sealed []byte) ([]byte, error) {
	block, err := aes.NewCipher(v.encKey)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	if len(sealed) < gcm.NonceSize() {
		return nil, fmt.Errorf("ciphertext too short")
	}
	nonce, ct := sealed[:gcm.NonceSize()], sealed[gcm.NonceSize():]
	return gcm.Open(nil, nonce, ct, nil)
}
