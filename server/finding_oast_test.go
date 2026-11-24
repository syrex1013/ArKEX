package server

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"testing"

	"github.com/Autumn-27/artex/db"
	"github.com/Autumn-27/artex/offensive/oast"
)

// oastTaskID reads the task a fixture finding belongs to.
func oastTaskID(t *testing.T, s *Server, findingID int64) int64 {
	t.Helper()
	var tid int64
	if err := s.m.pg.QueryRow(`SELECT task_id FROM findings WHERE id=$1`, findingID).Scan(&tid); err != nil {
		t.Fatal(err)
	}
	return tid
}

// S3: with the S1 quote check failing, a confirmed OAST callback for the
// claimed tag still verifies the finding.
func TestVerificationOASTReceiptS3(t *testing.T) {
	s, f, _ := verificationFixture(t, "proof")
	ctx := context.Background()
	payload, err := s.oastServer().GeneratePayload(ctx, "sqli-oob", strconv.FormatInt(oastTaskID(t, s, f.FindingID), 10), "")
	if err != nil {
		t.Fatal(err)
	}
	s.oastServer().Ingest(ctx, &oast.Callback{PayloadTag: payload, Protocol: "http"})
	// The verified message carries no note text; the S3 receipt shows in the
	// stored verification note.
	msg, isErr := updateReport(t, s, f.NodeID, fmt.Sprintf(`,"poe_level":3,"evidence_quotes":["secret=999"],"oast_tag":%q`, payload))
	if isErr || !strings.Contains(msg, "verification: verified") {
		t.Fatalf("unexpected result: err=%v %q", isErr, msg)
	}
	status, poe, note, _ := storedVerification(t, s, f.FindingID)
	if status != db.VerificationVerified || poe != 3 || note != fmt.Sprintf("S3 OAST receipt: tag %s observed", payload) {
		t.Fatalf("stored: status=%s poe=%d note=%q", status, poe, note)
	}
	var tag string
	if err := s.m.pg.QueryRow(`SELECT oast_tag FROM findings WHERE id=$1`, f.FindingID).Scan(&tag); err != nil || tag != payload {
		t.Fatalf("oast_tag not persisted: %q err=%v", tag, err)
	}
}

// Fail closed: a claimed tag with no received callback never upgrades the
// finding — the S1 verdict stands.
func TestVerificationOASTReceiptAbsentStaysUnverified(t *testing.T) {
	s, f, _ := verificationFixture(t, "proof")
	payload, err := s.oastServer().GeneratePayload(context.Background(), "xxe-stage1",
		strconv.FormatInt(oastTaskID(t, s, f.FindingID), 10), "")
	if err != nil {
		t.Fatal(err)
	}
	// Registered (pending) but never hit: pending rows are not receipts.
	msg, isErr := updateReport(t, s, f.NodeID, fmt.Sprintf(`,"poe_level":3,"evidence_quotes":["secret=999"],"oast_tag":%q`, payload))
	if isErr || !strings.Contains(msg, "verification: unverified") {
		t.Fatalf("unexpected result: err=%v %q", isErr, msg)
	}
	status, _, note, _ := storedVerification(t, s, f.FindingID)
	if status != db.VerificationUnverified || !strings.HasPrefix(note, "quote not found") {
		t.Fatalf("stored: status=%s note=%q", status, note)
	}
}
