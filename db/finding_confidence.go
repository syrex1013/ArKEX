package db

// Finding confidence: a 0-100 score derived from evidence only (severity stays a
// separate axis). It ranks findings for the agent and the findings page, and it
// weights attack-path scores.

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
)

// ConfidenceSignal is the evidence a confidence score is computed from.
type ConfidenceSignal struct {
	PoELevel           int
	VerificationStatus string
	OASTTag            string
	ProofBindings      int // bindings with role proof|verification
	DistinctWorkers    int
	HasCounterevidence bool
	HasRationale       bool
}

// FindingConfidence derives 0-100 from evidence. Bare unverified = 0.
func FindingConfidence(sig ConfidenceSignal) int {
	s := 10 + 12*sig.PoELevel
	switch sig.VerificationStatus {
	case VerificationVerified:
		s += 25
	case VerificationBlocked:
		s -= 10
	case VerificationNotAssessed:
		s -= 5
	case VerificationFalsePositive:
		s -= 30
	}
	if sig.OASTTag != "" {
		s += 15
	}
	n := sig.ProofBindings
	if n > 3 {
		n = 3
	}
	s += 6 * n
	w := sig.DistinctWorkers - 1
	if w > 2 {
		w = 2
	}
	if w > 0 {
		s += 8 * w
	}
	if !sig.HasCounterevidence {
		s -= 5
	}
	if !sig.HasRationale {
		s -= 5
	}
	if s < 0 {
		return 0
	}
	if s > 100 {
		return 100
	}
	return s
}

// ConfidenceSignalTx reads the evidence signal of one finding.
func ConfidenceSignalTx(tx *sql.Tx, findingID int64) (ConfidenceSignal, error) {
	var sig ConfidenceSignal
	var status, oast, counter, rationale string
	var workers int
	err := tx.QueryRow(`SELECT f.poe_level, COALESCE(f.verification_status,'unverified'), COALESCE(f.oast_tag,''),
COALESCE(f.counterevidence,''), COALESCE(f.confidence_rationale,''),
(SELECT count(DISTINCT f2.worker) FROM findings f2
 WHERE f2.task_id=f.task_id AND f2.fingerprint=f.fingerprint AND f2.fingerprint <> ''),
(SELECT count(*) FROM finding_traffic_bindings b WHERE b.finding_id=f.id AND b.role IN ('proof','verification'))
FROM findings f WHERE f.id=$1`, findingID).Scan(&sig.PoELevel, &status, &oast, &counter, &rationale, &workers, &sig.ProofBindings)
	if err != nil {
		return sig, err
	}
	if workers < 1 {
		workers = 1
	}
	sig.VerificationStatus = status
	sig.OASTTag = oast
	sig.DistinctWorkers = workers
	sig.HasCounterevidence = counter != ""
	sig.HasRationale = rationale != ""
	return sig, nil
}

// RecomputeFindingConfidenceTx rewrites a finding's confidence and rescores every
// path that uses it.
func RecomputeFindingConfidenceTx(ctx context.Context, tx *sql.Tx, findingID int64) error {
	sig, err := ConfidenceSignalTx(tx, findingID)
	if err != nil {
		return err
	}
	score := FindingConfidence(sig)
	if _, err := tx.ExecContext(ctx, `UPDATE findings SET confidence=$2 WHERE id=$1`, findingID, score); err != nil {
		return err
	}
	rows, err := tx.QueryContext(ctx, `SELECT DISTINCT path_id FROM attack_path_steps WHERE kind='finding' AND ref_id=$1`, findingID)
	if err != nil {
		return err
	}
	ids, err := drainIDs(rows)
	if err != nil {
		return err
	}
	for _, id := range ids {
		if err := RecomputePathScoreTx(ctx, tx, id); err != nil {
			return err
		}
	}
	return nil
}

func drainIDs(rows *sql.Rows) ([]int64, error) {
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

// RecomputeFindingConfidence is the standalone (non-tx) variant.
func (d *DB) RecomputeFindingConfidence(ctx context.Context, findingID int64) (int, error) {
	var score int
	err := d.WithEvidenceTx(ctx, func(tx *sql.Tx) error {
		if err := RecomputeFindingConfidenceTx(ctx, tx, findingID); err != nil {
			return err
		}
		return tx.QueryRow(`SELECT confidence FROM findings WHERE id=$1`, findingID).Scan(&score)
	})
	return score, err
}

// findingFingerprintTx is the dedupe identity of one defect per task: sha256 over
// the task id, the lowercased vulnclass and the sorted asset keys ("" for none).
func findingFingerprintTx(tx *sql.Tx, taskID int64, vulnclass string, assetIDs []int64) (string, error) {
	keys := make([]string, 0, len(assetIDs))
	for _, id := range assetIDs {
		var key sql.NullString
		if err := tx.QueryRow(`SELECT COALESCE(NULLIF(domain,''), NULLIF(url,''), NULLIF(ip,'')) FROM assets WHERE id=$1`, id).Scan(&key); err != nil {
			if err == sql.ErrNoRows {
				continue
			}
			return "", err
		}
		if key.Valid && key.String != "" {
			keys = append(keys, normKnowledgeKey(key.String))
		}
	}
	if len(keys) == 0 {
		return "", nil
	}
	slices.Sort(keys)
	return hashFingerprint(fmt.Sprintf("%d|%s|%s", taskID, normKnowledgeKey(vulnclass), strings.Join(keys, "\n"))), nil
}

// FindingConfidences returns finding id → confidence for the given ids.
func (s *ExplorationStore) FindingConfidences(ids []int64) (map[int64]int, error) {
	out := map[int64]int{}
	if len(ids) == 0 {
		return out, nil
	}
	rows, err := s.db.Query(`SELECT id, confidence FROM findings WHERE id = ANY($1)`, ids)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id int64
		var c int
		if err := rows.Scan(&id, &c); err != nil {
			return nil, err
		}
		out[id] = c
	}
	return out, rows.Err()
}

func hashFingerprint(raw string) string {
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}

// resolveDuplicateTx finds an existing finding of the same task with the same
// fingerprint (any status but duplicate) and merges the new report into it: the
// asset ids are unioned, counterevidence and rationale appended, and the evidence
// version bumped. Returns nil when there is no duplicate.
func resolveDuplicateTx(ctx context.Context, tx *sql.Tx, in RecordFindingInput, fp string) (*DBFinding, error) {
	if fp == "" || in.TaskID == 0 {
		return nil, nil
	}
	var id int64
	err := tx.QueryRowContext(ctx, `SELECT id FROM findings
WHERE task_id=$1 AND fingerprint=$2 AND status <> 'duplicate' ORDER BY id LIMIT 1 FOR UPDATE`,
		in.TaskID, fp).Scan(&id)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	assets := in.AssetIDs
	if assets == nil {
		assets = []int64{}
	}
	raw, _ := json.Marshal(assets)
	var out DBFinding
	var nodeID sql.NullInt64
	err = tx.QueryRowContext(ctx, `UPDATE findings SET
asset_ids = (SELECT COALESCE(jsonb_agg(DISTINCT x), '[]'::jsonb) FROM jsonb_array_elements(asset_ids || $2::jsonb) x),
counterevidence = CASE WHEN $3 = '' THEN counterevidence
  ELSE concat_ws(E'\n---\n', NULLIF(counterevidence,''), $3) END,
confidence_rationale = CASE WHEN $4 = '' THEN confidence_rationale
  ELSE concat_ws(E'\n---\n', NULLIF(confidence_rationale,''), $4) END,
evidence_version = evidence_version + 1
WHERE id=$1 RETURNING id, node_id`, id, string(raw), in.Counterevidence, in.ConfidenceRationale).Scan(&out.ID, &nodeID)
	if err != nil {
		return nil, err
	}
	if nodeID.Valid {
		out.NodeID = &nodeID.Int64
	}
	for _, asset := range in.AssetIDs {
		if out.NodeID == nil {
			break
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO exploration_anchors(node_id,asset_id) VALUES($1,$2) ON CONFLICT DO NOTHING`, *out.NodeID, asset); err != nil {
			return nil, err
		}
	}
	return &out, nil
}
