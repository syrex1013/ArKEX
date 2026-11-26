package db

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
)

// persistedArchiveWarnings returns the warnings already stored on an archive row.
// The restore-retry path uses it so a repeat call reports the same operator-visible
// warnings as the attempt that actually wrote the rows.
func persistedArchiveWarnings(tx *sql.Tx, archiveID int64) ([]string, error) {
	warnings := []string{}
	var raw []byte
	if err := tx.QueryRow(`SELECT COALESCE(warnings,'[]'::jsonb) FROM task_archives WHERE id=$1`, archiveID).Scan(&raw); err != nil {
		return nil, err
	}
	if len(raw) == 0 {
		return warnings, nil
	}
	if err := json.Unmarshal(raw, &warnings); err != nil {
		return nil, err
	}
	return warnings, nil
}

// restoreArchiveSwarmState restores the long-horizon swarm tables: discoveries,
// attack paths, the coverage ledger, RSI lesson usage, intercept rules, and the
// project knowledge an archived task contributed.
//
// The tables that carry a plain task_id go through insertArchiveRows. The two
// structural exceptions are handled explicitly: attack path ids are re-assigned by
// the database (steps are re-pointed at the new ids), and knowledge items are
// merged by their identity index instead of blind-inserted, because project
// knowledge is shared across tasks and a re-extracted fact already exists.
func restoreArchiveSwarmState(tx *sql.Tx, taskID int64, snapshot *TaskArchiveSnapshot, assetMap map[int64]int64) ([]string, error) {
	warnings := []string{}
	for _, table := range []string{"discovery_events", "task_intercept_rules", "chain_cursor", "coverage_ledger", "coverage_ledger_history"} {
		if err := insertArchiveRows(tx, table, snapshot.Tables[table]); err != nil {
			return nil, fmt.Errorf("restore %s: %w", table, err)
		}
	}
	// A lesson the library no longer holds cannot be referenced; the usage row is
	// dropped rather than failing the whole restore of the task's state.
	if raw := snapshot.Tables["rsi_lesson_usage"]; rawRowCount(raw) > 0 {
		if _, err := tx.Exec(`INSERT INTO rsi_lesson_usage SELECT usage.* FROM json_populate_recordset(NULL::rsi_lesson_usage,$1::json) usage
WHERE usage.lesson_id IN (SELECT id FROM rsi_lessons)`, string(raw)); err != nil {
			return nil, fmt.Errorf("restore rsi_lesson_usage: %w", err)
		}
	}
	pathWarnings, err := restoreArchiveAttackPaths(tx, taskID, snapshot.Tables["attack_paths"], snapshot.Tables["attack_path_steps"], assetMap)
	if err != nil {
		return nil, err
	}
	warnings = append(warnings, pathWarnings...)
	knowledgeWarnings, err := restoreArchiveKnowledge(tx, taskID, snapshot.Tables["knowledge_items"], snapshot.Tables["knowledge_item_sources"], snapshot.Tables["knowledge_injections"])
	if err != nil {
		return nil, err
	}
	warnings = append(warnings, knowledgeWarnings...)
	return warnings, nil
}

// restoreArchiveAttackPaths re-inserts paths and their steps. Path ids are new
// (archived ids may collide with another task's rows on a shared instance), so the
// steps are re-pointed; step asset references are translated through assetMap,
// which the asset restore produced when host ids also changed.
func restoreArchiveAttackPaths(tx *sql.Tx, taskID int64, pathsRaw, stepsRaw json.RawMessage, assetMap map[int64]int64) ([]string, error) {
	warnings := []string{}
	paths, err := decodeArchiveRows(pathsRaw)
	if err != nil {
		return nil, err
	}
	pathIDs := make(map[int64]int64, len(paths))
	for _, path := range paths {
		oldID, ok := jsonInt64(path["id"])
		if !ok || oldID <= 0 {
			return nil, errors.New("archived attack path has invalid id")
		}
		var newID int64
		if err := tx.QueryRow(`INSERT INTO attack_paths (task_id,title,rationale,state,score,refute_reason,created_by,created_at,updated_at)
VALUES ($1,$2,$3,$4,$5,$6,$7,COALESCE($8,now()),COALESCE($9,now())) RETURNING id`,
			taskID, archiveString(path["title"]), archiveString(path["rationale"]), archiveStringDefault(path["state"], "proposed"),
			archiveFloat(path["score"]), archiveString(path["refute_reason"]), archiveString(path["created_by"]),
			archiveTime(path["created_at"]), archiveTime(path["updated_at"])).Scan(&newID); err != nil {
			return nil, fmt.Errorf("restore attack_paths: %w", err)
		}
		pathIDs[oldID] = newID
	}
	steps, err := decodeArchiveRows(stepsRaw)
	if err != nil {
		return nil, err
	}
	for _, step := range steps {
		oldPathID, ok := jsonInt64(step["path_id"])
		if !ok {
			return nil, errors.New("archived attack path step has invalid path_id")
		}
		newPathID, ok := pathIDs[oldPathID]
		if !ok {
			warnings = append(warnings, fmt.Sprintf("attack path step for path %d dropped: path was not archived", oldPathID))
			continue
		}
		refID, _ := jsonInt64(step["ref_id"])
		if kind := archiveString(step["kind"]); kind == "asset" && refID > 0 {
			if mapped, ok := assetMap[refID]; ok {
				refID = mapped
			}
		}
		if _, err := tx.Exec(`INSERT INTO attack_path_steps (path_id,position,kind,ref_id,edge,precondition,postcondition,status,evidence_note,validated_by,updated_at)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,COALESCE($11,now()))`,
			newPathID, archiveInt(step["position"]), archiveString(step["kind"]), refID, archiveStringDefault(step["edge"], "enables"),
			archiveString(step["precondition"]), archiveString(step["postcondition"]), archiveStringDefault(step["status"], "proposed"),
			archiveString(step["evidence_note"]), archiveString(step["validated_by"]), archiveTime(step["updated_at"])); err != nil {
			return nil, fmt.Errorf("restore attack_path_steps: %w", err)
		}
	}
	return warnings, nil
}

// restoreArchiveKnowledge merges archived project knowledge back by identity and
// re-points the task's sources and injection log at the surviving item ids.
// Merging keeps the live item's history: last_seen/confidence/support are widened
// with GREATEST, never lowered, so a restore cannot erase newer knowledge.
func restoreArchiveKnowledge(tx *sql.Tx, taskID int64, itemsRaw, sourcesRaw, injectionsRaw json.RawMessage) ([]string, error) {
	warnings := []string{}
	items, err := decodeArchiveRows(itemsRaw)
	if err != nil {
		return nil, err
	}
	itemIDs := make(map[int64]int64, len(items))
	for _, item := range items {
		oldID, ok := jsonInt64(item["id"])
		if !ok || oldID <= 0 {
			return nil, errors.New("archived knowledge item has invalid id")
		}
		companyID := nullableArchiveInt64(item["scope_company_id"])
		if companyID != nil && !rowExists(tx, "companies", *companyID) {
			companyID = nil
			warnings = append(warnings, fmt.Sprintf("knowledge item %q restored unaffiliated: its company was deleted", archiveString(item["title"])))
		}
		engagementID := nullableArchiveInt64(item["scope_engagement_id"])
		if engagementID != nil && !rowExists(tx, "engagements", *engagementID) {
			engagementID = nil
		}
		var newID int64
		if err := tx.QueryRow(`INSERT INTO knowledge_items
 (kind,title,body,scope_company_id,scope_engagement_id,scope_asset_key,scope_vulnclass,confidence,support,source_task_id,status,first_seen,last_seen)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,COALESCE($12,now()),COALESCE($13,now()))
ON CONFLICT (kind,title,scope_company_id,scope_engagement_id,scope_asset_key,scope_vulnclass) DO UPDATE SET
 support=GREATEST(knowledge_items.support, EXCLUDED.support),
 confidence=GREATEST(knowledge_items.confidence, EXCLUDED.confidence),
 last_seen=GREATEST(knowledge_items.last_seen, EXCLUDED.last_seen)
RETURNING id`,
			archiveString(item["kind"]), archiveString(item["title"]), archiveString(item["body"]), companyID, engagementID,
			archiveString(item["scope_asset_key"]), archiveString(item["scope_vulnclass"]),
			archiveIntDefault(item["confidence"], 40), archiveIntDefault(item["support"], 1), taskID,
			archiveStringDefault(item["status"], "active"), archiveTime(item["first_seen"]), archiveTime(item["last_seen"])).Scan(&newID); err != nil {
			return nil, fmt.Errorf("restore knowledge_items: %w", err)
		}
		itemIDs[oldID] = newID
	}
	sources, err := decodeArchiveRows(sourcesRaw)
	if err != nil {
		return nil, err
	}
	for _, source := range sources {
		oldItemID, ok := jsonInt64(source["item_id"])
		if !ok {
			return nil, errors.New("archived knowledge source has invalid item_id")
		}
		newItemID, ok := itemIDs[oldItemID]
		if !ok {
			warnings = append(warnings, fmt.Sprintf("knowledge source for item %d dropped: item was not archived", oldItemID))
			continue
		}
		if _, err := tx.Exec(`INSERT INTO knowledge_item_sources (item_id,source_kind,source_id,content_hash,created_at)
VALUES ($1,$2,$3,$4,COALESCE($5,now())) ON CONFLICT DO NOTHING`,
			newItemID, archiveString(source["source_kind"]), archiveInt(source["source_id"]), archiveString(source["content_hash"]), archiveTime(source["created_at"])); err != nil {
			return nil, fmt.Errorf("restore knowledge_item_sources: %w", err)
		}
	}
	injections, err := decodeArchiveRows(injectionsRaw)
	if err != nil {
		return nil, err
	}
	for _, injection := range injections {
		oldItemID, ok := jsonInt64(injection["item_id"])
		if !ok {
			return nil, errors.New("archived knowledge injection has invalid item_id")
		}
		newItemID, ok := itemIDs[oldItemID]
		if !ok {
			warnings = append(warnings, fmt.Sprintf("knowledge injection for item %d dropped: item was not archived", oldItemID))
			continue
		}
		if _, err := tx.Exec(`INSERT INTO knowledge_injections (item_id,task_id,intent_id,agent_name,mode,shown_at)
VALUES ($1,$2,$3,$4,$5,COALESCE($6,now()))`,
			newItemID, taskID, nullableArchiveInt64(injection["intent_id"]), archiveString(injection["agent_name"]),
			archiveStringDefault(injection["mode"], "shadow"), archiveTime(injection["shown_at"])); err != nil {
			return nil, fmt.Errorf("restore knowledge_injections: %w", err)
		}
	}
	return warnings, nil
}

func archiveString(raw any) string {
	if value, ok := raw.(string); ok {
		return value
	}
	return ""
}

func archiveStringDefault(raw any, fallback string) string {
	if value, ok := raw.(string); ok && value != "" {
		return value
	}
	return fallback
}

func archiveInt(raw any) int64 {
	value, _ := jsonInt64(raw)
	return value
}

func archiveIntDefault(raw any, fallback int64) int64 {
	value, ok := jsonInt64(raw)
	if !ok {
		return fallback
	}
	return value
}

func archiveFloat(raw any) float64 {
	switch value := raw.(type) {
	case json.Number:
		parsed, err := value.Float64()
		if err != nil {
			return 0
		}
		return parsed
	case float64:
		return value
	}
	return 0
}

func archiveTime(raw any) any {
	if value, ok := raw.(string); ok && value != "" {
		return value
	}
	return nil
}

func nullableArchiveInt64(raw any) *int64 {
	value, ok := jsonInt64(raw)
	if !ok || value <= 0 {
		return nil
	}
	return &value
}
