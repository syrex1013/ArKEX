package agent

// Knowledge block rendering: the project memory injected before a planner round
// and a worker launch. Ranking leads with the review state (current, then due
// revalidation, then disputed) so a doubted item never displaces a trusted one,
// and within a trust tier is BM25 over title+body with an asset-key match first,
// then confidence. The text derives from target content, so the caller wraps it
// in the data envelope.

import (
	"fmt"
	"math"
	"sort"
	"strings"

	"github.com/Autumn-27/artex/db"
)

// knowledgeBM25 scores items against the query (same k1/b as tool ranking).
func knowledgeBM25(query string, items []db.KnowledgeItem) []float64 {
	const k1, b = 1.2, 0.75
	docs := make([]map[string]int, len(items))
	lens := make([]float64, len(items))
	df := map[string]int{}
	var total float64
	for i, it := range items {
		tf := map[string]int{}
		for _, w := range tokenize(it.Title + " " + it.Body) {
			tf[w]++
			lens[i]++
		}
		for w := range tf {
			df[w]++
		}
		docs[i], total = tf, total+lens[i]
	}
	scores := make([]float64, len(items))
	n := float64(len(items))
	avg, seen := math.Max(total/math.Max(n, 1), 1), map[string]bool{}
	for _, q := range tokenize(query) {
		if seen[q] || df[q] == 0 {
			continue
		}
		seen[q] = true
		idf := math.Log(1 + (n-float64(df[q])+0.5)/(float64(df[q])+0.5))
		for i, tf := range docs {
			if f := float64(tf[q]); f > 0 {
				scores[i] += idf * f * (k1 + 1) / (f + k1*(1-b+b*lens[i]/avg))
			}
		}
	}
	return scores
}

// knowledgeTrustTier maps a review state to its ranking tier: 0 current, 1 due
// revalidation, 2 disputed. Trust is the primary sort key, so no item outside
// "current" can outrank a current one. An empty state reads as current, matching
// db.GetKnowledge for rows written before the column existed.
func knowledgeTrustTier(state string) int {
	switch state {
	case db.KnowledgeRevalidate:
		return 1
	case db.KnowledgeReviewDisputed:
		return 2
	}
	return 0
}

// rankKnowledge orders items by trust tier, then by asset-key match on the
// query, then BM25, then confidence. The three relevance keys only order items
// inside a tier, so review state decides whether an item is preferred at all.
func rankKnowledge(items []db.KnowledgeItem, query string) []db.KnowledgeItem {
	q := strings.ToLower(query)
	scores := knowledgeBM25(query, items)
	idx := make([]int, len(items))
	for i := range idx {
		idx[i] = i
	}
	tiers := make([]int, len(items))
	for i := range items {
		tiers[i] = knowledgeTrustTier(items[i].ReviewState)
	}
	assetMatch := func(i int) bool {
		k := items[i].ScopeAssetKey
		return k != "" && strings.Contains(q, strings.ToLower(k))
	}
	// Comparison chain: trust tier, then asset-key match, then BM25, then
	// confidence. Earlier keys win outright; later keys only break ties.
	sort.SliceStable(idx, func(a, b int) bool {
		ia, ib := idx[a], idx[b]
		if tiers[ia] != tiers[ib] {
			return tiers[ia] < tiers[ib]
		}
		if assetMatch(ia) != assetMatch(ib) {
			return assetMatch(ia)
		}
		if scores[ia] != scores[ib] {
			return scores[ia] > scores[ib]
		}
		return items[ia].Confidence > items[ib].Confidence
	})
	out := make([]db.KnowledgeItem, len(items))
	for i, j := range idx {
		out[i] = items[j]
	}
	return out
}

// renderKnowledgeBlock ranks and formats the scoped knowledge of a task. Each
// line reads "[kind, conf N, support N] Title — body", followed by
// " [INFERENCE]" when confidence is below 50 and by a trust marker —
// " [DISPUTED]" for disputed items, " [REVALIDATE]" for items due
// revalidation — so the agent can tell how far to trust a line. Current items
// carry no trust marker. Returns the block (empty when nothing matches) and the
// rendered ids.
func renderKnowledgeBlock(items []db.KnowledgeItem, query string, maxItems, maxChars int) (string, []int64) {
	if len(items) == 0 || maxItems <= 0 || maxChars <= 0 {
		return "", nil
	}
	items = rankKnowledge(items, query)
	var b strings.Builder
	var ids []int64
	for n, it := range items {
		if n >= maxItems {
			break
		}
		body := strings.Join(strings.Fields(it.Body), " ")
		if r := []rune(body); len(r) > 240 {
			body = string(r[:240]) + "…"
		}
		line := fmt.Sprintf("[%s, conf %d, support %d] %s", it.Kind, it.Confidence, it.Support, it.Title)
		if body != "" {
			line += " — " + body
		}
		if it.Confidence < 50 {
			line += " [INFERENCE]"
		}
		// The trust marker follows [INFERENCE]; current items keep the line
		// byte-identical to the pre-review-state format.
		switch it.ReviewState {
		case db.KnowledgeReviewDisputed:
			line += " [DISPUTED]"
		case db.KnowledgeRevalidate:
			line += " [REVALIDATE]"
		}
		if b.Len()+len(line)+1 > maxChars {
			break
		}
		b.WriteString(line)
		b.WriteByte('\n')
		ids = append(ids, it.ID)
	}
	return strings.TrimRight(b.String(), "\n"), ids
}

// KnowledgeCandidates renders the knowledge block for a tool query.
func (t *ToolSet) KnowledgeCandidates(query string, limit int) (string, []int64) {
	if t.ts == nil || t.ts.DB() == nil {
		return "", nil
	}
	items, err := t.ts.DB().SearchKnowledgeCandidates(t.taskID, 400)
	if err != nil || len(items) == 0 {
		return "", nil
	}
	if limit <= 0 {
		limit = 12
	}
	return renderKnowledgeBlock(items, query, limit, 2500)
}

// logKnowledgeInjections records exactly which knowledge items were selected
// for an agent run (the knowledge_injections table, the rsi_lesson_usage
// mirror). Best-effort: a logging failure never blocks the run.
func logKnowledgeInjections(d *db.DB, taskID, intentID int64, agentName, mode string, ids []int64) {
	if d == nil || len(ids) == 0 || taskID <= 0 || agentName == "" {
		return
	}
	var intentPtr *int64
	if intentID > 0 {
		intentPtr = &intentID
	}
	for _, id := range ids {
		if id <= 0 {
			continue
		}
		if err := d.LogKnowledgeInjection(id, taskID, intentPtr, agentName, mode); err != nil {
			return
		}
	}
}
