package db

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
)

// Machine-checked verification state of a finding (S1). Kept apart from
// findings.status, which is the human disposition. Values are validated here
// (server-side), not by a CHECK constraint, like the other findings columns.
const (
	VerificationVerified      = "verified"
	VerificationUnverified    = "unverified"
	VerificationBlocked       = "blocked"
	VerificationNotAssessed   = "not_assessed"
	VerificationFalsePositive = "false_positive"
)

// ValidVerificationStatus reports whether s is a known verification state.
func ValidVerificationStatus(s string) bool {
	switch s {
	case VerificationVerified, VerificationUnverified, VerificationBlocked,
		VerificationNotAssessed, VerificationFalsePositive:
		return true
	}
	return false
}

// SetFindingVerification records the verification state, POE level and note on
// the standalone findings row. It does not touch status, report or evidence.
func (d *DB) SetFindingVerification(findingID int64, status string, poe int, note string) error {
	if !ValidVerificationStatus(status) {
		return fmt.Errorf("invalid verification status %q", status)
	}
	if poe < 0 || poe > 4 {
		return fmt.Errorf("invalid POE level %d (want 0-4)", poe)
	}
	res, err := d.Exec(`UPDATE findings SET verification_status=$2, poe_level=$3, verification_note=$4 WHERE id=$1`,
		findingID, status, poe, note)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrFindingNotFound
	}
	_, err = d.RecomputeFindingConfidence(context.Background(), findingID)
	return err
}

// SetFindingOASTTag records the OAST payload tag a finding claims as its
// out-of-band (S3) proof. Empty string clears the claim.
func (d *DB) SetFindingOASTTag(ctx context.Context, findingID int64, tag string) error {
	res, err := d.ExecContext(ctx, `UPDATE findings SET oast_tag=$2 WHERE id=$1`, findingID, strings.TrimSpace(tag))
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrFindingNotFound
	}
	_, err = d.RecomputeFindingConfidence(ctx, findingID)
	return err
}

// FindingOASTTag returns the finding's claimed OAST receipt tag (empty = none).
func (d *DB) FindingOASTTag(ctx context.Context, findingID int64) (string, error) {
	var tag string
	err := d.QueryRowContext(ctx, `SELECT oast_tag FROM findings WHERE id=$1`, findingID).Scan(&tag)
	if err == sql.ErrNoRows {
		return "", ErrFindingNotFound
	}
	return tag, err
}
