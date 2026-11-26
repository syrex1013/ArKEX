package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/Autumn-27/artex/db"
	actool "github.com/Autumn-27/norma/tool"
)

// rankKnowledge puts an item whose asset key appears in the query ahead of a
// better-BM25 company-scoped item.
func TestKnowledgeAssetKeyOutranksCompanyMatch(t *testing.T) {
	query := "vpn.corp.example gateway"
	asset := db.KnowledgeItem{ID: 1, Kind: "asset_fact", Title: "vpn gate",
		Body: "the gate sits in front of everything", ScopeAssetKey: "vpn.corp.example", Confidence: 40}
	company := db.KnowledgeItem{ID: 2, Kind: "asset_fact", Title: "gateway rules vpn gateway",
		Body: "gateway rules vpn gateway", ScopeCompanyID: new(int64(7)), Confidence: 95}

	block, ids := renderKnowledgeBlock([]db.KnowledgeItem{company, asset}, query, 10, 2500)
	if len(ids) != 2 || ids[0] != asset.ID || ids[1] != company.ID {
		t.Fatalf("ids=%v, want asset-key item first", ids)
	}
	if lines := strings.Split(block, "\n"); len(lines) != 2 || !strings.HasPrefix(lines[0], "[asset_fact, conf 40") {
		t.Fatalf("block=%q", block)
	}
}

// rankKnowledge leads with the review state: a doubted item never outranks a
// trusted one, however well it matches, while the relevance order inside a trust
// tier is untouched.
func TestKnowledgeRankTrustTierFirst(t *testing.T) {
	cases := []struct {
		name  string
		items []db.KnowledgeItem
		query string
		want  []int64
	}{
		{
			// The disputed body repeats the query term, so BM25 alone would put
			// it first; trust must keep it last.
			name: "disputed never outranks current",
			items: []db.KnowledgeItem{
				{ID: 1, Kind: "note", Title: "gateway note", Body: "gateway", Confidence: 90,
					ReviewState: db.KnowledgeReviewCurrent},
				{ID: 2, Kind: "note", Title: "gateway gateway gateway", Body: "gateway gateway", Confidence: 99,
					ReviewState: db.KnowledgeReviewDisputed},
			},
			query: "gateway",
			want:  []int64{1, 2},
		},
		{
			// Both rows are disputed, so the asset-key match keeps its old
			// precedence over a better BM25 score.
			name: "relevance order preserved inside a trust tier",
			items: []db.KnowledgeItem{
				{ID: 10, Kind: "asset_fact", Title: "gateway gateway gateway", Body: "gateway gateway", Confidence: 99,
					ReviewState: db.KnowledgeReviewDisputed},
				{ID: 11, Kind: "asset_fact", Title: "gate", Body: "gate", ScopeAssetKey: "vpn.corp.example", Confidence: 10,
					ReviewState: db.KnowledgeReviewDisputed},
			},
			query: "vpn.corp.example gateway",
			want:  []int64{11, 10},
		},
		{
			name: "revalidation outranks disputed regardless of score",
			items: []db.KnowledgeItem{
				{ID: 20, Kind: "note", Title: "gateway gateway gateway", Body: "gateway gateway", Confidence: 99,
					ReviewState: db.KnowledgeReviewDisputed},
				{ID: 21, Kind: "note", Title: "gateway note", Body: "gateway", Confidence: 5,
					ReviewState: db.KnowledgeRevalidate},
			},
			query: "gateway",
			want:  []int64{21, 20},
		},
		{
			name: "full chain is current then revalidation then disputed",
			items: []db.KnowledgeItem{
				{ID: 30, Kind: "note", Title: "gateway gateway gateway", Body: "gateway gateway", Confidence: 99,
					ReviewState: db.KnowledgeReviewDisputed},
				{ID: 31, Kind: "note", Title: "gateway gateway gateway", Body: "gateway gateway", Confidence: 99,
					ReviewState: db.KnowledgeRevalidate},
				{ID: 32, Kind: "note", Title: "gateway", Body: "gateway", Confidence: 1,
					ReviewState: db.KnowledgeReviewCurrent},
			},
			query: "gateway",
			want:  []int64{32, 31, 30},
		},
		{
			// Rows written before review_state existed read as current, so they
			// keep ranking ahead of a disputed row that scores better.
			name: "empty review state reads as current",
			items: []db.KnowledgeItem{
				{ID: 40, Kind: "note", Title: "gateway gateway gateway", Body: "gateway gateway", Confidence: 99,
					ReviewState: db.KnowledgeReviewDisputed},
				{ID: 41, Kind: "note", Title: "gateway note", Body: "gateway", Confidence: 10},
			},
			query: "gateway",
			want:  []int64{41, 40},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			ranked := rankKnowledge(c.items, c.query)
			got := make([]int64, len(ranked))
			for i, it := range ranked {
				got[i] = it.ID
			}
			if len(got) != len(c.want) {
				t.Fatalf("ranked %d items, want %d: %v", len(got), len(c.want), got)
			}
			for i := range got {
				if got[i] != c.want[i] {
					t.Fatalf("order = %v, want %v", got, c.want)
				}
			}
		})
	}
}

// Rendering marks the review state after [INFERENCE]: disputed items carry
// [DISPUTED], items due revalidation carry [REVALIDATE], and current items stay
// byte-identical to the pre-review-state line.
func TestKnowledgeRenderReviewMarkers(t *testing.T) {
	items := []db.KnowledgeItem{
		{ID: 1, Kind: "note", Title: "plain fact", Body: "trusted", Confidence: 90,
			ReviewState: db.KnowledgeReviewCurrent},
		{ID: 2, Kind: "note", Title: "contested fact", Body: "operators disagree", Confidence: 90,
			ReviewState: db.KnowledgeReviewDisputed},
		{ID: 3, Kind: "note", Title: "stale fact", Body: "needs a look", Confidence: 90,
			ReviewState: db.KnowledgeRevalidate},
	}
	block, ids := renderKnowledgeBlock(items, "fact", 10, 2500)
	if len(ids) != 3 {
		t.Fatalf("ids = %v, want all three rendered", ids)
	}
	lineOf := func(title string) string {
		for _, line := range strings.Split(block, "\n") {
			if strings.Contains(line, title) {
				return line
			}
		}
		t.Fatalf("no line for %q in %q", title, block)
		return ""
	}
	if line, want := lineOf("plain fact"), "[note, conf 90, support 0] plain fact — trusted"; line != want {
		t.Fatalf("current line = %q, want %q", line, want)
	}
	if line := lineOf("contested fact"); !strings.Contains(line, "[DISPUTED]") || strings.Contains(line, "[REVALIDATE]") {
		t.Fatalf("disputed line = %q, want only [DISPUTED]", line)
	}
	if line := lineOf("stale fact"); !strings.Contains(line, "[REVALIDATE]") || strings.Contains(line, "[DISPUTED]") {
		t.Fatalf("revalidation line = %q, want only [REVALIDATE]", line)
	}
	for _, marker := range []string{"[DISPUTED]", "[REVALIDATE]"} {
		if strings.Contains(lineOf("plain fact"), marker) {
			t.Fatalf("current line carries %s: %q", marker, lineOf("plain fact"))
		}
	}
}

// The rendered block is capped: items that would push it past maxChars are cut,
// and the trust marker makes no difference to the cap.
func TestKnowledgeBlockCharCap(t *testing.T) {
	newItems := func(state string) []db.KnowledgeItem {
		var items []db.KnowledgeItem
		for range 40 {
			items = append(items, db.KnowledgeItem{ID: int64(len(items) + 1), Kind: "note",
				Title: strings.Repeat("x", 120), Body: strings.Repeat("y", 240), Confidence: 90,
				ReviewState: state})
		}
		return items
	}
	for _, state := range []string{db.KnowledgeReviewCurrent, db.KnowledgeReviewDisputed} {
		block, ids := renderKnowledgeBlock(newItems(state), "x", 40, 2500)
		if len(block) > 2500 {
			t.Fatalf("%s: block len %d exceeds cap 2500", state, len(block))
		}
		if len(ids) == 40 {
			t.Fatalf("%s: cap not applied: rendered %d items", state, len(ids))
		}
		if state == db.KnowledgeReviewDisputed && len(ids) > 0 {
			for _, line := range strings.Split(block, "\n") {
				if !strings.HasSuffix(line, "[DISPUTED]") {
					t.Fatalf("%s: line lost its marker: %q", state, line)
				}
			}
		}
	}
}

// Items below confidence 50 are marked as inference, and the trust marker
// follows it rather than replacing it.
func TestKnowledgeInferenceMarker(t *testing.T) {
	items := []db.KnowledgeItem{
		{ID: 1, Kind: "note", Title: "guess", Body: "uncorroborated", Confidence: 30},
		{ID: 2, Kind: "note", Title: "confirmed", Body: "verified", Confidence: 90},
		{ID: 3, Kind: "note", Title: "contested guess", Body: "still a guess", Confidence: 20,
			ReviewState: db.KnowledgeReviewDisputed},
	}
	block, _ := renderKnowledgeBlock(items, "guess confirmed", 10, 2500)
	if !strings.Contains(block, "[INFERENCE]") {
		t.Fatalf("low-confidence item not marked: %q", block)
	}
	if !strings.Contains(block, "[note, conf 30, support 0]") {
		t.Fatalf("confidence header missing: %q", block)
	}
	if !strings.Contains(block, "still a guess [INFERENCE] [DISPUTED]") {
		t.Fatalf("trust marker not appended after inference: %q", block)
	}
	if strings.Contains(block, "verified [INFERENCE]") {
		t.Fatalf("high-confidence item marked: %q", block)
	}
}

// remember must refuse a write whose item resolves to no scope at all: retrieval
// is scoped (company/engagement/asset key), so an unscoped row would be stored
// but never retrievable. A call with resolvable asset_ids is accepted and lands
// under the asset key.
func TestRememberRejectsUnscopedWrite(t *testing.T) {
	d := testDB(t)
	t.Cleanup(func() { _ = d.Close() })
	task, err := d.CreateTaskWithOptions("remember-scope", "scope-less remember", db.TaskCreateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = d.DeleteTask(task.ID)
		_, _ = d.Exec(`DELETE FROM knowledge_items WHERE source_task_id=$1 OR scope_asset_key='remember-scope.example.com'`, task.ID)
		_, _ = d.Exec(`DELETE FROM assets WHERE domain='remember-scope.example.com'`)
	})
	tsx := NewToolSet(d.Exploration(task.ExplorationID), "worker-1")
	tsx.SetTaskID(task.ID)

	call := func(payload map[string]any) actool.Result {
		raw, _ := json.Marshal(payload)
		res, err := tsx.rememberKnowledge().Call(context.Background(), raw, nil)
		if err != nil {
			t.Fatalf("remember Call error: %v", err)
		}
		return res
	}

	res := call(map[string]any{"kind": "asset_fact", "title": fmt.Sprintf("unscoped fact %d", task.ID), "body": "no scope"})
	if !res.IsError {
		t.Fatalf("unscoped remember accepted: %s", res.Flatten())
	}
	if !strings.Contains(res.Flatten(), "asset_ids") {
		t.Fatalf("rejection lacks guidance: %s", res.Flatten())
	}
	var n int
	if err := d.QueryRow(`SELECT count(*) FROM knowledge_items WHERE source_task_id=$1`, task.ID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("unscoped row written: %d", n)
	}

	var assetID int64
	if err := d.QueryRow(`INSERT INTO assets(type,domain) VALUES('subdomain','remember-scope.example.com') RETURNING id`).Scan(&assetID); err != nil {
		t.Fatal(err)
	}
	res = call(map[string]any{"kind": "asset_fact", "title": fmt.Sprintf("scoped fact %d", task.ID), "body": "asset keyed", "asset_ids": []int64{assetID}})
	if res.IsError {
		t.Fatalf("scoped remember refused: %s", res.Flatten())
	}
	var key string
	if err := d.QueryRow(`SELECT scope_asset_key FROM knowledge_items WHERE source_task_id=$1 LIMIT 1`, task.ID).Scan(&key); err != nil {
		t.Fatal(err)
	}
	if key != "remember-scope.example.com" {
		t.Fatalf("scope_asset_key=%q", key)
	}
}
