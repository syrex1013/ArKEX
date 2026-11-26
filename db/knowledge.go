package db

// Project knowledge: durable, evidence-backed facts a run learns and later runs
// retrieve (asset facts, dependencies, attempts, failed hypotheses, successful
// approaches, chains). Scope is company/engagement/asset-key; retrieval is scoped
// the same way so another client's findings never leak in.

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"
)

// Knowledge kinds.
const (
	KnowledgeAssetFact          = "asset_fact"
	KnowledgeDependency         = "dependency"
	KnowledgeAttempt            = "attempt"
	KnowledgeFailedHypothesis   = "failed_hypothesis"
	KnowledgeSuccessfulApproach = "successful_approach"
	KnowledgeChain              = "chain"
	KnowledgeLesson             = "lesson"
)

// Knowledge item statuses.
const (
	KnowledgeActive  = "active"
	KnowledgeRefuted = "refuted"
	KnowledgeRetired = "retired"
)

// ValidKnowledgeKind reports whether k is a knowledge kind.
func ValidKnowledgeKind(k string) bool {
	switch k {
	case KnowledgeAssetFact, KnowledgeDependency, KnowledgeAttempt, KnowledgeFailedHypothesis,
		KnowledgeSuccessfulApproach, KnowledgeChain, KnowledgeLesson:
		return true
	}
	return false
}

// KnowledgeItem is one project memory row.
type KnowledgeItem struct {
	ID              int64      `json:"id"`
	Kind            string     `json:"kind"`
	Title           string     `json:"title"`
	Body            string     `json:"body"`
	ScopeCompanyID  *int64     `json:"scope_company_id,omitempty"`
	ScopeEngagement *int64     `json:"scope_engagement_id,omitempty"`
	ScopeAssetKey   string     `json:"scope_asset_key,omitempty"`
	ScopeVulnclass  string     `json:"scope_vulnclass,omitempty"`
	Confidence      int        `json:"confidence"`
	Support         int        `json:"support"`
	SourceTaskID    *int64     `json:"source_task_id,omitempty"`
	Status          string     `json:"status"`
	ReviewState     string     `json:"review_state"`
	SupersededBy    *int64     `json:"superseded_by,omitempty"`
	RevalidateAfter *time.Time `json:"revalidate_after,omitempty"`
	FirstSeen       time.Time  `json:"first_seen"`
	LastSeen        time.Time  `json:"last_seen"`
	// Source is the evidence this write contributes, not stored state: it is
	// recorded in knowledge_item_sources and left out of the JSON payload.
	Source *KnowledgeSource `json:"-"`
}

// KnowledgeSource is one piece of evidence behind a knowledge item: the kind of
// record it came from, that record's id, and the content hash of the text it
// contributed. The hash is what makes a re-extraction of unchanged content
// recognisable, so support counts evidence rather than extraction runs.
type KnowledgeSource struct {
	Kind        string
	ID          int64
	ContentHash string
}

// KnowledgeContentHash fingerprints one source's contribution. The unit
// separator keeps (kind, title, body) unambiguous, so a body that swallows part
// of a later field cannot collide with a different source.
func KnowledgeContentHash(kind, title, body string) string {
	sum := sha256.Sum256([]byte(kind + "\x1f" + title + "\x1f" + body))
	return hex.EncodeToString(sum[:])
}

// Knowledge review states: whether operators still trust an item.
const (
	KnowledgeReviewCurrent  = "current"
	KnowledgeReviewDisputed = "disputed"
	KnowledgeRevalidate     = "due_revalidation"
)

// ErrKnowledgeReviewState marks a review state outside the three the schema and
// the retrieval order know about.
var ErrKnowledgeReviewState = errors.New("invalid knowledge review state")

// ValidKnowledgeReviewState reports whether s is a review state.
func ValidKnowledgeReviewState(s string) bool {
	switch s {
	case KnowledgeReviewCurrent, KnowledgeReviewDisputed, KnowledgeRevalidate:
		return true
	}
	return false
}

// knowledgeExecer is the query surface the knowledge upsert needs, so the same
// logic runs standalone and inside SupersedeKnowledge's transaction.
type knowledgeExecer interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

// UpsertKnowledge inserts a knowledge item, or merges into the identical one
// (identity is kind + title + scope): last_seen moves to now and confidence
// rises to the greater of the two.
//
// Support counts distinct evidence, not calls: when in.Source is set the write
// records that source and support becomes the number of distinct sources behind
// the item, so re-extracting the identical source (same content hash) is
// idempotent and never inflates it. An item with no evidence rows keeps its
// stored support, which is what pre-evidence rows carry.
//
// Returns the item id.
func (d *DB) UpsertKnowledge(ctx context.Context, in KnowledgeItem) (int64, error) {
	tx, err := d.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback() //nolint:errcheck // no-op after Commit
	id, err := upsertKnowledgeTx(ctx, tx, in)
	if err != nil {
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return id, nil
}

// upsertKnowledgeTx is the body of UpsertKnowledge, shared with SupersedeKnowledge.
func upsertKnowledgeTx(ctx context.Context, ex knowledgeExecer, in KnowledgeItem) (int64, error) {
	var id int64
	err := ex.QueryRowContext(ctx, `INSERT INTO knowledge_items
(kind, title, body, scope_company_id, scope_engagement_id, scope_asset_key, scope_vulnclass, confidence, source_task_id)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)
ON CONFLICT (kind, title, scope_company_id, scope_engagement_id, scope_asset_key, scope_vulnclass) DO UPDATE
SET last_seen = now(),
    confidence = GREATEST(knowledge_items.confidence, EXCLUDED.confidence)
RETURNING id`,
		in.Kind, in.Title, in.Body, in.ScopeCompanyID, in.ScopeEngagement, in.ScopeAssetKey,
		in.ScopeVulnclass, in.Confidence, in.SourceTaskID).Scan(&id)
	if err != nil {
		return 0, err
	}
	if in.Source != nil && in.Source.Kind != "" && in.Source.ID > 0 {
		if _, err := ex.ExecContext(ctx, `INSERT INTO knowledge_item_sources(item_id, source_kind, source_id, content_hash)
VALUES ($1,$2,$3,$4)
ON CONFLICT DO NOTHING`, id, in.Source.Kind, in.Source.ID, in.Source.ContentHash); err != nil {
			return 0, fmt.Errorf("record knowledge source: %w", err)
		}
	}
	// Recompute from evidence. The EXISTS guard keeps an item that has no
	// evidence rows on its stored (legacy) support instead of resetting it to 0.
	if _, err := ex.ExecContext(ctx, `UPDATE knowledge_items
SET support = (SELECT count(DISTINCT (source_kind, source_id))::int FROM knowledge_item_sources WHERE item_id=$1)
WHERE id=$1 AND EXISTS (SELECT 1 FROM knowledge_item_sources WHERE item_id=$1)`, id); err != nil {
		return 0, fmt.Errorf("recompute knowledge support: %w", err)
	}
	return id, nil
}

// TaskKnowledgeScope resolves a task's retrieval scope: its companies, engagement
// and asset keys (lowercased domain, else ip, else url per linked asset).
func (d *DB) TaskKnowledgeScope(taskID int64) (companyIDs []int64, engagementID int64, assetKeys []string, err error) {
	companyIDs, err = d.queryScopeCompanies(taskID)
	if err != nil {
		return nil, 0, nil, err
	}
	assetKeys, err = d.queryScopeAssetKeys(taskID)
	if err != nil {
		return nil, 0, nil, err
	}
	eng, ok, err := d.EngagementIDForTask(taskID)
	if err != nil {
		return nil, 0, nil, err
	}
	if ok {
		engagementID = eng
	}
	return companyIDs, engagementID, assetKeys, nil
}

func (d *DB) queryScopeCompanies(taskID int64) ([]int64, error) {
	rows, err := d.Query(`SELECT company_id FROM task_scope WHERE task_id=$1 AND kind='company' AND company_id IS NOT NULL`, taskID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// AssetKeys returns the knowledge keys of the given asset ids (domain, else ip,
// else url; lowercased), skipping gaps.
func (d *DB) AssetKeys(ids []int64) ([]string, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	rows, err := d.Query(`SELECT lower(COALESCE(NULLIF(domain,''), NULLIF(ip,''), NULLIF(url,'')))
FROM assets WHERE id = ANY($1)`, ids)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var k sql.NullString
		if err := rows.Scan(&k); err != nil {
			return nil, err
		}
		if k.Valid && k.String != "" {
			out = append(out, k.String)
		}
	}
	return out, rows.Err()
}

func (d *DB) queryScopeAssetKeys(taskID int64) ([]string, error) {
	rows, err := d.Query(`SELECT lower(COALESCE(NULLIF(a.domain,''), NULLIF(a.ip,''), NULLIF(a.url,'')))
FROM task_asset_links l JOIN assets a ON a.id = l.asset_id WHERE l.task_id=$1`, taskID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	seen := map[string]bool{}
	var out []string
	for rows.Next() {
		var k sql.NullString
		if err := rows.Scan(&k); err != nil {
			return nil, err
		}
		if !k.Valid || k.String == "" || seen[k.String] {
			continue
		}
		seen[k.String] = true
		out = append(out, k.String)
	}
	return out, rows.Err()
}

// SearchKnowledgeCandidates returns the scoped active knowledge of a task for
// relevance ranking in the agent layer: trustworthy items first, then those due
// for revalidation, then disputed ones; newest first within each group, hard cap
// limit.
//
// Scope precedence is the safety rule here: first applicable scope wins, so
// operator-scoped knowledge stays inside its client/engagement. A company-wide
// item reaches every task of that company, an engagement item reaches only that
// engagement's tasks, and only an item with no company and no engagement may be
// matched by asset key — an item bound to a company or engagement can therefore
// never be rescued by an asset-key match from another client's task.
func (d *DB) SearchKnowledgeCandidates(taskID int64, limit int) ([]KnowledgeItem, error) {
	if limit <= 0 {
		limit = 400
	}
	companies, engagement, keys, err := d.TaskKnowledgeScope(taskID)
	if err != nil {
		return nil, err
	}
	rows, err := d.Query(`SELECT id, kind, title, body, scope_company_id, scope_engagement_id,
scope_asset_key, scope_vulnclass, confidence, support, source_task_id, status, first_seen, last_seen,
review_state, superseded_by, revalidate_after
FROM knowledge_items
WHERE status='active'
  AND ((scope_company_id IS NOT NULL AND scope_company_id = ANY($1))
    OR (scope_company_id IS NULL AND scope_engagement_id IS NOT NULL AND scope_engagement_id = $2)
    OR (scope_company_id IS NULL AND scope_engagement_id IS NULL AND scope_asset_key <> '' AND scope_asset_key = ANY($3)))
ORDER BY CASE review_state WHEN 'current' THEN 0 WHEN 'due_revalidation' THEN 1 ELSE 2 END,
         last_seen DESC
LIMIT $4`, int64Array(companies), engagement, strArray(keys), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []KnowledgeItem
	for rows.Next() {
		k, err := scanKnowledge(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *k)
	}
	return out, rows.Err()
}

func int64Array(v []int64) []int64 {
	if v == nil {
		return []int64{}
	}
	return v
}

func strArray(v []string) []string {
	if v == nil {
		return []string{}
	}
	return v
}

func scanKnowledge(row interface{ Scan(...any) error }) (*KnowledgeItem, error) {
	var k KnowledgeItem
	var comp, eng, src, superseded sql.NullInt64
	var review sql.NullString
	var revalidate sql.NullTime
	if err := row.Scan(&k.ID, &k.Kind, &k.Title, &k.Body, &comp, &eng, &k.ScopeAssetKey, &k.ScopeVulnclass,
		&k.Confidence, &k.Support, &src, &k.Status, &k.FirstSeen, &k.LastSeen,
		&review, &superseded, &revalidate); err != nil {
		return nil, err
	}
	if comp.Valid {
		k.ScopeCompanyID = &comp.Int64
	}
	if eng.Valid {
		k.ScopeEngagement = &eng.Int64
	}
	if src.Valid {
		k.SourceTaskID = &src.Int64
	}
	// Rows written before review state existed, or by a statement that does not
	// set it, read as current.
	k.ReviewState = KnowledgeReviewCurrent
	if review.Valid && review.String != "" {
		k.ReviewState = review.String
	}
	if superseded.Valid {
		k.SupersededBy = &superseded.Int64
	}
	if revalidate.Valid {
		k.RevalidateAfter = &revalidate.Time
	}
	return &k, nil
}

// GetKnowledge returns one knowledge item.
func (d *DB) GetKnowledge(id int64) (*KnowledgeItem, error) {
	k, err := scanKnowledge(d.QueryRow(`SELECT id, kind, title, body, scope_company_id, scope_engagement_id,
scope_asset_key, scope_vulnclass, confidence, support, source_task_id, status, first_seen, last_seen,
review_state, superseded_by, revalidate_after
FROM knowledge_items WHERE id=$1`, id))
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return k, err
}

// SetKnowledgeReviewState records whether operators still trust an item.
func (d *DB) SetKnowledgeReviewState(id int64, state string) error {
	if !ValidKnowledgeReviewState(state) {
		return fmt.Errorf("%w: %q (want %s, %s or %s)", ErrKnowledgeReviewState, state,
			KnowledgeReviewCurrent, KnowledgeReviewDisputed, KnowledgeRevalidate)
	}
	_, err := d.Exec(`UPDATE knowledge_items SET review_state=$2 WHERE id=$1`, id, state)
	return err
}

// SupersedeKnowledge replaces oldID with in and keeps the history: the
// replacement goes through the same upsert path, the old item's evidence rows are
// copied onto it (so an evidenced item keeps its support), and the old row is
// retired with superseded_by pointing at the replacement. The old row's body and
// scope are never rewritten — a reader can always see what was believed before.
//
// The replacement needs its own identity (kind + title + scope) so it is a new
// row: superseding an item with the identity of the item being replaced would
// resolve to the same row and silently rewrite history, so it is rejected.
func (d *DB) SupersedeKnowledge(ctx context.Context, oldID int64, in KnowledgeItem) (int64, error) {
	tx, err := d.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback() //nolint:errcheck // no-op after Commit

	// Also serializes concurrent supersessions of the same item on its row lock.
	var exists bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM knowledge_items WHERE id=$1 FOR UPDATE)`, oldID).Scan(&exists); err != nil {
		return 0, err
	}
	if !exists {
		return 0, fmt.Errorf("supersede knowledge %d: %w", oldID, sql.ErrNoRows)
	}

	newID, err := upsertKnowledgeTx(ctx, tx, in)
	if err != nil {
		return 0, err
	}
	if newID == oldID {
		return 0, fmt.Errorf("supersede knowledge %d: replacement needs its own kind/title/scope identity", oldID)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO knowledge_item_sources(item_id, source_kind, source_id, content_hash)
SELECT $1, source_kind, source_id, content_hash FROM knowledge_item_sources WHERE item_id=$2
ON CONFLICT DO NOTHING`, newID, oldID); err != nil {
		return 0, fmt.Errorf("carry knowledge sources: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE knowledge_items
SET support = (SELECT count(DISTINCT (source_kind, source_id))::int FROM knowledge_item_sources WHERE item_id=$1)
WHERE id=$1 AND EXISTS (SELECT 1 FROM knowledge_item_sources WHERE item_id=$1)`, newID); err != nil {
		return 0, fmt.Errorf("recompute knowledge support: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE knowledge_items SET status=$2, superseded_by=$3 WHERE id=$1`,
		oldID, KnowledgeRetired, newID); err != nil {
		return 0, fmt.Errorf("retire superseded knowledge %d: %w", oldID, err)
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return newID, nil
}

// SetKnowledgeStatus sets an item's status.
func (d *DB) SetKnowledgeStatus(id int64, status string) error {
	_, err := d.Exec(`UPDATE knowledge_items SET status=$2 WHERE id=$1`, id, status)
	return err
}

// EngagementIDForTask returns the task's engagement id when set.
func (d *DB) EngagementIDForTask(taskID int64) (int64, bool, error) {
	var eng sql.NullInt64
	err := d.QueryRow(`SELECT engagement_id FROM tasks WHERE id=$1`, taskID).Scan(&eng)
	if err == sql.ErrNoRows {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, err
	}
	if !eng.Valid {
		return 0, false, nil
	}
	return eng.Int64, true, nil
}

// normKnowledgeKey folds an asset key for storage and comparison.
func normKnowledgeKey(s string) string { return strings.ToLower(strings.TrimSpace(s)) }
