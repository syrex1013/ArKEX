package agent

// Knowledge tools: `remember` writes a durable project fact, `knowledge_search`
// reads the scoped store on demand.

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/Autumn-27/artex/db"
	actool "github.com/Autumn-27/norma/tool"
)

// rememberKnowledge persists one knowledge item scoped to this task.
func (t *ToolSet) rememberKnowledge() actool.CoreTool {
	return t.writeExpTool("remember",
		"Store a durable project fact for later tasks of the same company/engagement/assets: an ownership or technology fact (asset_fact/dependency), a tested approach (attempt), a ruled-out hypothesis (failed_hypothesis), a working technique (successful_approach) or a chain. Re-writing the same kind+title merges (support+1).",
		obj(map[string]any{
			"kind":       str("asset_fact | dependency | attempt | failed_hypothesis | successful_approach | chain"),
			"title":      str("short durable statement (one line)"),
			"body":       str("detail: evidence, conditions, why it holds"),
			"confidence": intp("0-100; how sure the statement is (default 40)"),
			"asset_ids":  map[string]any{"type": "array", "items": map[string]any{"type": "integer"}, "description": "asset ids the fact is scoped to"},
		}, "kind", "title"),
		func(ctx context.Context, in json.RawMessage) (actool.Result, error) {
			var a struct {
				Kind       string  `json:"kind"`
				Title      string  `json:"title"`
				Body       string  `json:"body"`
				Confidence int     `json:"confidence"`
				AssetIDs   []int64 `json:"asset_ids"`
			}
			_ = json.Unmarshal(in, &a)
			if !db.ValidKnowledgeKind(a.Kind) {
				return actool.Errorf("kind must be one of asset_fact, dependency, attempt, failed_hypothesis, successful_approach, chain"), nil
			}
			a.Title = strings.Join(strings.Fields(a.Title), " ")
			if a.Title == "" {
				return actool.Errorf("title is required"), nil
			}
			if a.Confidence <= 0 {
				a.Confidence = 40
			}
			if a.Confidence > 100 {
				a.Confidence = 100
			}
			ds := t.ts.DB()
			if ds == nil {
				return actool.Errorf("remember requires a task context"), nil
			}
			companies, engagement, _, err := ds.TaskKnowledgeScope(t.taskID)
			if err != nil {
				return actool.Errorf(err.Error()), nil
			}
			item := db.KnowledgeItem{
				Kind: a.Kind, Title: a.Title, Body: a.Body, Confidence: a.Confidence,
				SourceTaskID: &t.taskID, Status: db.KnowledgeActive,
				Source: &db.KnowledgeSource{
					Kind: "task", ID: t.taskID,
					ContentHash: db.KnowledgeContentHash(a.Kind, a.Title, a.Body),
				},
			}
			if len(companies) == 1 {
				item.ScopeCompanyID = &companies[0]
			}
			if engagement > 0 {
				item.ScopeEngagement = &engagement
			}
			if keys, err := ds.AssetKeys(a.AssetIDs); err == nil && len(keys) > 0 {
				item.ScopeAssetKey = keys[0]
			}
			if item.ScopeCompanyID == nil && item.ScopeEngagement == nil && item.ScopeAssetKey == "" {
				// Retrieval is scoped to company/engagement/asset key; an
				// unscoped row would be stored but never retrievable.
				return actool.Errorf("remember refused: this task has no company, engagement or asset scope, so the item would be stored but never retrievable; pass asset_ids that resolve to assets, or run inside a task scoped to a company, engagement or asset"), nil
			}
			id, err := ds.UpsertKnowledge(ctx, item)
			if err != nil {
				return actool.Errorf(err.Error()), nil
			}
			detail, _ := json.Marshal(map[string]any{"knowledge_id": id, "kind": a.Kind})
			_, _ = ds.AddAgentEvent(db.AgentEvent{TaskID: t.taskID, AgentName: t.worker, Kind: db.EventKnowledgeWrite, Detail: detail})
			return actool.Text(fmt.Sprintf("remembered #%d", id)), nil
		})
}

// searchKnowledge ranks the scoped knowledge for a query.
func (t *ToolSet) searchKnowledge() actool.CoreTool {
	return t.readExpTool("knowledge_search",
		"Search the project knowledge of this task's company/engagement/assets (from earlier runs too). Returns kind+confidence+support+title+body, best match first. Items below confidence 50 are inferences.",
		obj(map[string]any{
			"q":     str("search text"),
			"limit": intp("max items, default 10"),
		}, "q"),
		func(_ context.Context, in json.RawMessage) (actool.Result, error) {
			var a struct {
				Q     string `json:"q"`
				Limit int    `json:"limit"`
			}
			_ = json.Unmarshal(in, &a)
			a.Q = strings.TrimSpace(a.Q)
			if a.Q == "" {
				return actool.Errorf("q is required"), nil
			}
			if a.Limit <= 0 || a.Limit > 50 {
				a.Limit = 10
			}
			ds := t.ts.DB()
			if ds == nil {
				return actool.Errorf("knowledge_search requires a task context"), nil
			}
			items, err := ds.SearchKnowledgeCandidates(t.taskID, 400)
			if err != nil {
				return actool.Errorf(err.Error()), nil
			}
			ranked := rankKnowledge(items, a.Q)
			if len(ranked) > a.Limit {
				ranked = ranked[:a.Limit]
			}
			out := make([]map[string]any, 0, len(ranked))
			for _, it := range ranked {
				out = append(out, map[string]any{
					"id": it.ID, "kind": it.Kind, "title": it.Title, "body": it.Body,
					"confidence": it.Confidence, "support": it.Support, "asset_key": it.ScopeAssetKey,
				})
			}
			return jsonResult(out)
		})
}
