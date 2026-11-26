package server

// Review-state mutation against a live manager: PATCH /api/knowledge/{id} moves
// an existing item to "disputed", rejects a state the schema does not know, and
// 404s an unknown id (rather than reporting it as a bad payload). Skips when PG
// is unavailable.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Autumn-27/artex/db"
)

func TestUpdateKnowledgeReviewState(t *testing.T) {
	m, err := NewManager(t.TempDir(), "")
	if err != nil {
		db.SkipPGUnavailable(t, err)
	}
	defer m.Close()

	task, err := m.pg.CreateTaskWithOptions(
		fmt.Sprintf("knowledge review %d", time.Now().UnixNano()), "goal", db.TaskCreateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = m.pg.DeleteTask(task.ID) })
	// Registered after the task cleanup, so it runs first (cleanups are LIFO).
	t.Cleanup(func() {
		_, _ = m.pg.Exec(`DELETE FROM knowledge_items WHERE source_task_id=$1`, task.ID)
	})

	taskID := task.ID
	title := fmt.Sprintf("knowledge review item %d", time.Now().UnixNano())
	body := "the admin panel accepts default credentials"
	itemID, err := m.pg.UpsertKnowledge(context.Background(), db.KnowledgeItem{
		Kind: db.KnowledgeAssetFact, Title: title, Body: body, Confidence: 70, SourceTaskID: &taskID,
		Source: &db.KnowledgeSource{Kind: "task", ID: taskID,
			ContentHash: db.KnowledgeContentHash(db.KnowledgeAssetFact, title, body)},
	})
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	s := New(ctx, m, t.TempDir(), t.TempDir(), t.TempDir())
	defer stopTestServer(t, s, cancel)
	token, err := signJWT(s.jwtKey)
	if err != nil {
		t.Fatal(err)
	}
	patch := func(path, body string) (int, map[string]any) {
		req := httptest.NewRequest(http.MethodPatch, path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+token)
		rec := httptest.NewRecorder()
		s.Handler().ServeHTTP(rec, req)
		var out map[string]any
		_ = json.Unmarshal(rec.Body.Bytes(), &out)
		return rec.Code, out
	}
	path := fmt.Sprintf("/api/knowledge/%d", itemID)

	// A state the schema knows: accepted, and the response carries the item back.
	code, out := patch(path, `{"review_state":"disputed"}`)
	if code != http.StatusOK {
		t.Fatalf("disputed status=%d body=%v", code, out)
	}
	item, ok := out["knowledge"].(map[string]any)
	if !ok {
		t.Fatalf("disputed: response missing knowledge item: %v", out)
	}
	if item["review_state"] != db.KnowledgeReviewDisputed {
		t.Fatalf("disputed: review_state=%v body=%v", item["review_state"], out)
	}
	// The seeded evidence source is what support counts, so it must survive the
	// update — the row returned is the stored item, not an echo of the request.
	if support, _ := item["support"].(float64); support < 1 {
		t.Fatalf("disputed: support=%v body=%v", item["support"], out)
	}
	stored, err := m.pg.GetKnowledge(itemID)
	if err != nil {
		t.Fatal(err)
	}
	if stored == nil || stored.ReviewState != db.KnowledgeReviewDisputed {
		t.Fatalf("stored review_state=%+v", stored)
	}

	// A state outside the three the schema knows: rejected with the store message.
	code, out = patch(path, `{"review_state":"bogus"}`)
	if code != http.StatusBadRequest {
		t.Fatalf("bogus status=%d body=%v", code, out)
	}
	if msg, _ := out["error"].(string); msg == "" {
		t.Fatalf("bogus: want an error message, got %v", out)
	}
	// The rejected write left the stored state alone.
	if stored, err = m.pg.GetKnowledge(itemID); err != nil {
		t.Fatal(err)
	}
	if stored == nil || stored.ReviewState != db.KnowledgeReviewDisputed {
		t.Fatalf("after rejection review_state=%+v", stored)
	}

	// An unknown id 404s instead of being reported as a bad payload.
	code, out = patch("/api/knowledge/99999999", `{"review_state":"disputed"}`)
	if code != http.StatusNotFound {
		t.Fatalf("unknown id status=%d body=%v", code, out)
	}
}
