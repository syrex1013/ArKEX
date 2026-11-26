package agent

// Chain-agent tools: propose/validate/refute scored attack paths. The store owns
// every rule (ref validation, scoring, state derivation); these handlers stay
// thin wrappers plus the follow-up intent spawn and the evidence gates.

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/Autumn-27/artex/db"
	actool "github.com/Autumn-27/norma/tool"
)

// pathStepAssetIDs resolves the asset ids a step anchors the follow-up intent on:
// a finding step uses the finding's own asset_ids, an asset step is its ref, a
// fact step reuses the fact node's exploration anchors (nil when none).
func (t *ToolSet) pathStepAssetIDs(ctx context.Context, tx *sql.Tx, st db.PathStepInput) ([]int64, error) {
	switch st.Kind {
	case "asset":
		return []int64{st.RefID}, nil
	case "finding":
		var raw []byte
		if err := tx.QueryRowContext(ctx, `SELECT asset_ids FROM findings WHERE id=$1`, st.RefID).Scan(&raw); err != nil {
			return nil, err
		}
		var ids []int64
		if err := json.Unmarshal(raw, &ids); err != nil {
			return nil, err
		}
		return ids, nil
	default: // fact
		rows, err := tx.QueryContext(ctx, `SELECT asset_id FROM exploration_anchors WHERE node_id=$1 ORDER BY asset_id`, st.RefID)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		var ids []int64
		for rows.Next() {
			var id int64
			if err := rows.Scan(&id); err != nil {
				return nil, err
			}
			ids = append(ids, id)
		}
		return ids, rows.Err()
	}
}

// spawnPathIntentTx creates the follow-up intent for the first proposed step of a
// newly proposed path, inside the caller's transaction. Returns (intentID,
// spawned). The 3-open-intents cap is checked by the caller (CountOpenPathIntents).
func (t *ToolSet) spawnPathIntentTx(ctx context.Context, tx *sql.Tx, p *db.AttackPath, st db.PathStepInput) (int64, bool, error) {
	pos := 1 // a fresh path has exactly the proposed steps, first one drives the work
	role := "verify"
	switch st.Edge {
	case "enables", "pivots_to", "escalates":
		role = "exploit"
	}
	summary := fmt.Sprintf("[path #%d step %d] %s -> %s", p.ID, pos,
		strings.TrimSpace(st.Precondition), strings.TrimSpace(st.Postcondition))
	if strings.TrimSpace(st.Precondition) == "" && strings.TrimSpace(st.Postcondition) == "" {
		summary = fmt.Sprintf("[path #%d step %d] advance the %s step", p.ID, pos, st.Kind)
	}
	anchors, err := t.pathStepAssetIDs(ctx, tx, st)
	if err != nil {
		anchors = nil // intent spawns anchor-less rather than failing the whole proposal
	}
	payload := map[string]any{"summary": summary, "path_id": p.ID, "step_pos": pos, "role": role}
	if len(anchors) > 0 {
		payload["asset_ids"] = anchors
	}
	raw, _ := json.Marshal(payload)
	var intentID int64
	if err := tx.QueryRowContext(ctx, `INSERT INTO exploration_nodes(exploration_id, kind, payload, priority, state, origin)
VALUES ($1, 'intent', $2, 9, 'open', 'chain') RETURNING id`, t.ts.ID(), string(raw)).Scan(&intentID); err != nil {
		return 0, false, err
	}
	for _, aid := range anchors {
		if _, err := tx.ExecContext(ctx, `INSERT INTO exploration_anchors(node_id, asset_id) VALUES ($1,$2) ON CONFLICT DO NOTHING`, intentID, aid); err != nil {
			return 0, false, err
		}
	}
	// lineage: the intent derives from the step's evidence (finding node / fact node;
	// asset steps have no exploration node, the intent stands alone with its anchors).
	src := st.RefID
	if st.Kind == "finding" {
		if err := tx.QueryRowContext(ctx, `SELECT node_id FROM findings WHERE id=$1`, st.RefID).Scan(&src); err != nil {
			return intentID, true, nil
		}
	}
	if src > 0 && st.Kind != "asset" {
		if _, err := tx.ExecContext(ctx, `INSERT INTO exploration_edges(exploration_id, src_id, rel, dst_id) VALUES ($1,$2,$3,$4)
ON CONFLICT (exploration_id, src_id, rel, dst_id) DO NOTHING`, t.ts.ID(), src, db.RelDerivedFrom, intentID); err != nil {
			return 0, false, err
		}
	}
	return intentID, true, nil
}

// countOpenPathIntentsTx counts a path's live intents inside the caller's tx
// (the DB-level helper reads outside it, racy with an in-flight spawn).
func countOpenPathIntentsTx(ctx context.Context, tx *sql.Tx, pathID int64) (int, error) {
	var n int
	err := tx.QueryRowContext(ctx, `SELECT count(*) FROM exploration_nodes
WHERE kind='intent' AND state IN ('open','running') AND payload ? 'path_id'
  AND (payload->>'path_id')::bigint = $1`, pathID).Scan(&n)
	return n, err
}

func (t *ToolSet) proposePath() actool.CoreTool {
	return t.writeExpTool("propose_path",
		"Propose a scored attack path: a chain of 2-8 steps, each an asset, a confirmed finding (ref_id = finding_id from list_findings) or an exploration fact (ref_id = the fact node id), linked by an edge. Steps must be evidence-backed — never invent refs; every ref is validated against this task and an unknown id fails the call. The first proposed step automatically spawns one high-priority intent (exploit or verify depending on the edge) for a worker to advance. Returns the path id and whether an intent was spawned.",
		obj(map[string]any{
			"title":     str("short name of the whole chain, e.g. \"SSRF on reports → metadata → instance takeover\""),
			"rationale": str("why this chain plausibly works, citing the evidence of the referenced findings/facts"),
			"steps": map[string]any{"type": "array", "items": map[string]any{"type": "object", "properties": map[string]any{
				"kind":          str("asset | finding | fact"),
				"ref_id":        intp("finding_id (for finding), fact node id (for fact) or asset id (for asset)"),
				"edge":          str("enables | requires | pivots_to | escalates — how this step leads to the next"),
				"precondition":  str("what must hold before this step"),
				"postcondition": str("what this step, once verified, gives the attacker"),
			}}, "description": "the chain in order (min 2, max 8)"},
		}, "title", "rationale", "steps"),
		func(ctx context.Context, in json.RawMessage) (actool.Result, error) {
			var a db.PathInput
			if err := json.Unmarshal(in, &a); err != nil {
				return actool.Errorf(err.Error()), nil
			}
			a.TaskID = t.taskID
			a.CreatedBy = t.worker
			ds := t.ts.DB()
			if ds == nil {
				return actool.Errorf("propose_path requires a task context"), nil
			}
			tx, err := ds.Begin()
			if err != nil {
				return actool.Errorf(err.Error()), nil
			}
			defer tx.Rollback()
			p, err := db.ProposePathTx(ctx, tx, a)
			if err != nil {
				return actool.Errorf(err.Error()), nil
			}
			spawned := false
			intentID := int64(0)
			// auto-spawn: one follow-up intent for the first proposed step, capped at 3
			// open intents per path so one chain cannot flood the frontier.
			open, err := countOpenPathIntentsTx(ctx, tx, p.ID)
			if err != nil {
				return actool.Errorf(err.Error()), nil
			}
			if open < 3 {
				intentID, spawned, err = t.spawnPathIntentTx(ctx, tx, p, a.Steps[0])
				if err != nil {
					return actool.Errorf(err.Error()), nil
				}
			}
			if err := tx.Commit(); err != nil {
				return actool.Errorf(err.Error()), nil
			}
			detail, _ := json.Marshal(map[string]any{"path_id": p.ID, "steps": len(a.Steps), "intent_id": intentID, "spawned": spawned})
			_, _ = ds.AddAgentEvent(db.AgentEvent{TaskID: t.taskID, AgentName: t.worker, Kind: db.EventPathProposed, Detail: detail})
			return actool.Text(fmt.Sprintf("path #%d proposed (%d steps, score %.1f); follow-up intent spawned: %v", p.ID, len(a.Steps), p.Score, spawned)), nil
		})
}

func (t *ToolSet) validatePathStep() actool.CoreTool {
	return t.writeExpTool("validate_path_step",
		"Record a verdict (verified | refuted) on one step of a proposed attack path with an evidence note, and re-score the path. Evidence gates: a finding step needs proof-level evidence (POE>=3 or a bound proof/verification traffic request); a fact step needs confidence=observed. Verify with real evidence only — a step you cannot prove stays proposed.",
		obj(map[string]any{
			"path_id":       intp("the path id"),
			"position":      intp("1-based step position"),
			"verdict":       str("verified | refuted"),
			"evidence_note": str("what was observed and where — the proof this verdict rests on"),
		}, "path_id", "position", "verdict"),
		func(ctx context.Context, in json.RawMessage) (actool.Result, error) {
			var a struct {
				PathID       int64  `json:"path_id"`
				Position     int    `json:"position"`
				Verdict      string `json:"verdict"`
				EvidenceNote string `json:"evidence_note"`
			}
			if err := json.Unmarshal(in, &a); err != nil {
				return actool.Errorf(err.Error()), nil
			}
			if a.Verdict != db.StepStatusVerified && a.Verdict != db.StepStatusRefuted {
				return actool.Errorf("verdict must be verified or refuted"), nil
			}
			ds := t.ts.DB()
			if ds == nil {
				return actool.Errorf("validate_path_step requires a task context"), nil
			}
			// evidence gates run before any write: a step without proof-level evidence
			// cannot be marked verified, no matter what the model claims.
			p, steps, err := ds.GetAttackPath(a.PathID)
			if err != nil {
				return actool.Errorf(err.Error()), nil
			}
			if p == nil {
				return actool.Errorf(fmt.Sprintf("path %d not found", a.PathID)), nil
			}
			var step *db.AttackPathStep
			for _, s := range steps {
				if s.Position == a.Position {
					step = s
					break
				}
			}
			if step == nil {
				return actool.Errorf(fmt.Sprintf("path %d has no step %d", a.PathID, a.Position)), nil
			}
			if a.Verdict == db.StepStatusVerified {
				switch step.Kind {
				case "finding":
					var poe, bindings int
					if err := ds.QueryRow(`SELECT COALESCE(poe_level,0),
(SELECT count(*) FROM finding_traffic_bindings b WHERE b.finding_id=$1 AND b.role IN ('proof','verification'))
FROM findings WHERE id=$1 AND task_id=$2`, step.RefID, t.taskID).Scan(&poe, &bindings); err != nil {
						return actool.Errorf(err.Error()), nil
					}
					if poe < 3 && bindings < 1 {
						return actool.Errorf("finding step needs proof-level evidence (POE>=3 or a bound proof request) first"), nil
					}
				case "fact":
					var conf string
					if err := ds.QueryRow(`SELECT COALESCE(payload->>'confidence','') FROM exploration_nodes
WHERE id=$1 AND kind='fact'`, step.RefID).Scan(&conf); err != nil {
						return actool.Errorf(err.Error()), nil
					}
					if conf != "observed" {
						return actool.Errorf("fact step needs confidence=observed before it can be verified"), nil
					}
				}
			}
			tx, err := ds.Begin()
			if err != nil {
				return actool.Errorf(err.Error()), nil
			}
			defer tx.Rollback()
			if err := db.SetStepStatusTx(ctx, tx, a.PathID, a.Position, a.Verdict, a.EvidenceNote, t.worker); err != nil {
				return actool.Errorf(err.Error()), nil
			}
			if err := tx.Commit(); err != nil {
				return actool.Errorf(err.Error()), nil
			}
			kind := db.EventPathStep
			if a.Verdict == db.StepStatusRefuted {
				kind = db.EventPathRefuted
			} else {
				kind = db.EventPathValidated
			}
			detail, _ := json.Marshal(map[string]any{"path_id": a.PathID, "position": a.Position, "verdict": a.Verdict, "step_kind": step.Kind})
			_, _ = ds.AddAgentEvent(db.AgentEvent{TaskID: t.taskID, AgentName: t.worker, Kind: kind, Detail: detail})
			np, _, _ := ds.GetAttackPath(a.PathID)
			return actool.Text(fmt.Sprintf("path #%d step %d %s; path state now %s, score %.1f", a.PathID, a.Position, a.Verdict, np.State, np.Score)), nil
		})
}

func (t *ToolSet) refutePath() actool.CoreTool {
	return t.writeExpTool("refute_path",
		"Refute a whole attack path with a reason (score drops to 0; its open follow-up intents stay but planners should stop promoting it). Use when a precondition is disproven, not when one step merely fails.",
		obj(map[string]any{
			"path_id": intp("the path id"),
			"reason":  str("what evidence disproves the chain"),
		}, "path_id", "reason"),
		func(ctx context.Context, in json.RawMessage) (actool.Result, error) {
			var a struct {
				PathID int64  `json:"path_id"`
				Reason string `json:"reason"`
			}
			if err := json.Unmarshal(in, &a); err != nil {
				return actool.Errorf(err.Error()), nil
			}
			ds := t.ts.DB()
			if ds == nil {
				return actool.Errorf("refute_path requires a task context"), nil
			}
			if err := ds.RefutePath(ctx, a.PathID, a.Reason); err != nil {
				return actool.Errorf(err.Error()), nil
			}
			detail, _ := json.Marshal(map[string]any{"path_id": a.PathID, "reason": a.Reason})
			_, _ = ds.AddAgentEvent(db.AgentEvent{TaskID: t.taskID, AgentName: t.worker, Kind: db.EventPathRefuted, Detail: detail})
			return actool.Text(fmt.Sprintf("path #%d refuted", a.PathID)), nil
		})
}

func (t *ToolSet) listPaths() actool.CoreTool {
	return t.readExpTool("list_paths",
		"List this task's attack paths (id/title/state/score/steps summary), best score first. state filter optional: proposed | validating | validated | refuted; omit for all non-refuted plus refuted.",
		obj(map[string]any{
			"state": str("optional filter: proposed | validating | validated | refuted"),
		}),
		func(_ context.Context, in json.RawMessage) (actool.Result, error) {
			var a struct {
				State string `json:"state"`
			}
			_ = json.Unmarshal(in, &a)
			var states []string
			if a.State != "" {
				states = []string{a.State}
			}
			paths, err := t.ts.DB().ListAttackPaths(t.taskID, states, 50)
			if err != nil {
				return actool.Errorf(err.Error()), nil
			}
			out := make([]map[string]any, 0, len(paths))
			for _, p := range paths {
				out = append(out, map[string]any{"id": p.ID, "title": p.Title, "state": p.State, "score": p.Score})
			}
			return jsonResult(out)
		})
}

func (t *ToolSet) pathDetail() actool.CoreTool {
	return t.readExpTool("path_detail",
		"Fetch one attack path with all steps (kind/ref/edge/pre/post/status/evidence note/validated_by); missing refs render as missing and contribute no score.",
		obj(map[string]any{
			"path_id": intp("the path id"),
		}, "path_id"),
		func(_ context.Context, in json.RawMessage) (actool.Result, error) {
			var a struct {
				PathID int64 `json:"path_id"`
			}
			if err := json.Unmarshal(in, &a); err != nil {
				return actool.Errorf(err.Error()), nil
			}
			p, steps, err := t.ts.DB().GetAttackPath(a.PathID)
			if err != nil {
				return actool.Errorf(err.Error()), nil
			}
			return jsonResult(map[string]any{"path": p, "steps": steps})
		})
}

// ChainTools is the chain agent's tool surface: the path tools plus the shared
// read tools it correlates from.
func (t *ToolSet) ChainTools() []actool.CoreTool {
	return []actool.CoreTool{
		t.graphOverview(), t.listFindings(), t.listAssets(), t.nodeDetail(),
		t.proposePath(), t.validatePathStep(), t.refutePath(), t.listPaths(), t.pathDetail(),
	}
}
