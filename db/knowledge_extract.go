package db

// Extraction of project knowledge from a finished task's outcomes (coverage
// ledger, findings, attack paths). Every row is scoped before it is written —
// company, engagement and the task's first resolvable asset key — because
// retrieval matches on one of those three: an item with none of them can never
// be returned, so a task with no resolvable scope at all is skipped instead of
// writing dead rows.
//
// Provenance is per source: each item records the ledger row, finding or attack
// path it came from (knowledge_item_sources), so support counts distinct
// evidence rather than extraction runs. Re-running extraction over the same
// closed task therefore changes nothing: the identity key merges into the same
// row, the sources are the same, and their content hashes are unchanged.

import (
	"context"
	"fmt"
	"log"
	"strings"
)

// Knowledge source kinds recorded in knowledge_item_sources (the schema's
// allowed set is task / finding / checkpoint / operator / extraction).
const (
	knowledgeSourceTask       = "task"
	knowledgeSourceFinding    = "finding"
	knowledgeSourceExtraction = "extraction"
)

// ExtractKnowledgeForTask derives knowledge items from one task's outcome
// records: ruled-out coverage areas and counterevidence become failed
// hypotheses, follow-ups become attempts, verified findings with proof-level
// evidence become successful approaches, and every non-refuted attack path
// becomes a chain. Each source is independent: a read error skips that source
// and a row error skips that row, but the accumulated count is still returned.
//
// A task with no company, no engagement and no resolvable asset key yields no
// items at all: nothing written for it could ever be retrieved, so the whole
// extraction is skipped with a log line (not an error).
func (d *DB) ExtractKnowledgeForTask(ctx context.Context, taskID int64) (int, error) {
	if taskID <= 0 {
		return 0, nil
	}
	companies, engagementID, taskKeys, err := d.TaskKnowledgeScope(taskID)
	if err != nil {
		return 0, err
	}
	assetKeys, err := d.knowledgeAssetKeys(taskID, taskKeys)
	if err != nil {
		return 0, err
	}
	// Scope carries a single company: a task's knowledge belongs to the client
	// it was run for.
	var companyID *int64
	if len(companies) > 0 {
		companyID = &companies[0]
	}
	var engagement *int64
	if engagementID > 0 {
		engagement = &engagementID
	}
	assetKey := ""
	if len(assetKeys) > 0 {
		assetKey = assetKeys[0]
	}
	if companyID == nil && engagement == nil && assetKey == "" {
		log.Printf("knowledge extraction skipped: task %d has no resolvable scope", taskID)
		return 0, nil
	}
	task := &taskID
	written := 0
	upsert := func(in KnowledgeItem) {
		in.ScopeCompanyID, in.ScopeEngagement, in.SourceTaskID = companyID, engagement, task
		in.ScopeAssetKey = assetKey
		if _, err := d.UpsertKnowledge(ctx, in); err == nil {
			written++
		}
	}

	d.extractLedgerKnowledge(taskID, upsert)
	d.extractFindingKnowledge(taskID, upsert)
	d.extractPathKnowledge(taskID, upsert)
	return written, nil
}

// knowledgeAssetKeys resolves the asset keys this task's extracted knowledge is
// scoped to: the keys of the assets its findings point at (findings.asset_ids)
// first, since those name the surface the outcome was proven on, then the task's
// own linked keys as a fallback. The first key is the one every written item
// carries, mirroring the remember tool's keys[0] convention.
func (d *DB) knowledgeAssetKeys(taskID int64, taskKeys []string) ([]string, error) {
	findingKeys, err := d.findingAssetKeys(taskID)
	if err != nil {
		return nil, err
	}
	seen := make(map[string]bool, len(findingKeys)+len(taskKeys))
	out := make([]string, 0, len(findingKeys)+len(taskKeys))
	for _, group := range [][]string{findingKeys, taskKeys} {
		for _, k := range group {
			k = normKnowledgeKey(k)
			if k == "" || seen[k] {
				continue
			}
			seen[k] = true
			out = append(out, k)
		}
	}
	return out, nil
}

// findingAssetKeys returns the distinct knowledge keys (lowercased domain, else
// ip, else url) of the assets this task's findings were recorded against.
// findings.asset_ids is JSON, so non-array values count as no assets and
// non-numeric ids are skipped rather than failing the cast.
func (d *DB) findingAssetKeys(taskID int64) ([]string, error) {
	rows, err := d.Query(`SELECT DISTINCT lower(COALESCE(NULLIF(a.domain,''),NULLIF(a.ip,''),NULLIF(a.url,'')))
FROM findings f
CROSS JOIN LATERAL (
    SELECT t.v::bigint AS asset_id
    FROM jsonb_array_elements_text(CASE WHEN jsonb_typeof(f.asset_ids)='array' THEN f.asset_ids ELSE '[]'::jsonb END) AS t(v)
    WHERE t.v ~ '^[0-9]+$'
) AS aid
JOIN assets a ON a.id = aid.asset_id
WHERE f.task_id=$1 AND COALESCE(a.domain,a.ip,a.url,'') <> ''`, taskID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var k string
		if err := rows.Scan(&k); err != nil {
			return nil, err
		}
		out = append(out, k)
	}
	return out, rows.Err()
}

// knowledgeSourceOf builds one item's provenance: which record it came from and
// the hash of the exact kind+title+body text it contributes, so a re-extraction
// of unchanged content is recognisable as the same evidence.
func knowledgeSourceOf(sourceKind string, sourceID int64, kind, title, body string) *KnowledgeSource {
	return &KnowledgeSource{Kind: sourceKind, ID: sourceID, ContentHash: KnowledgeContentHash(kind, title, body)}
}

// extractLedgerKnowledge turns coverage-ledger rows into hypotheses: a
// ruled_out area records a dead end (why, plus the evidence); a needs_follow_up
// area records an unfinished attempt worth picking up next run.
func (d *DB) extractLedgerKnowledge(taskID int64, upsert func(KnowledgeItem)) {
	rows, err := d.Query(`SELECT surface, risk_area, outcome, COALESCE(evidence,''), COALESCE(reason,'')
FROM coverage_ledger WHERE task_id=$1`, taskID)
	if err != nil {
		return
	}
	defer rows.Close()
	for rows.Next() {
		var surface, riskArea, outcome, evidence, reason string
		if err := rows.Scan(&surface, &riskArea, &outcome, &evidence, &reason); err != nil {
			return
		}
		var kind string
		conf := 0
		switch outcome {
		case OutcomeRuledOut:
			kind = KnowledgeFailedHypothesis
			conf = 70
		case OutcomeNeedsFollowUp:
			kind = KnowledgeAttempt
			conf = 30
		default:
			continue
		}
		body := strings.TrimSpace(reason)
		if evidence != "" {
			body = strings.TrimSpace(body + "\n" + evidence)
		}
		title := ledgerKnowledgeTitle(surface, riskArea)
		upsert(KnowledgeItem{Kind: kind, Title: title, Body: body, Confidence: conf,
			Source: knowledgeSourceOf(knowledgeSourceTask, taskID, kind, title, body)})
	}
}

// ledgerKnowledgeTitle folds one ledger row's surface and risk area into a
// stable merge title.
func ledgerKnowledgeTitle(surface, riskArea string) string {
	return strings.TrimSpace(surface) + " / " + strings.TrimSpace(riskArea)
}

// extractFindingKnowledge turns finding outcomes into knowledge: counterevidence
// marks the tested variant a failed hypothesis; a verified finding with proof
// level >= 3 records the working approach at the finding's own confidence. The
// finding row is the source in both cases, so two findings that land on the same
// item count as two pieces of evidence.
func (d *DB) extractFindingKnowledge(taskID int64, upsert func(KnowledgeItem)) {
	rows, err := d.Query(`SELECT id, COALESCE(name,''), vulnclass, COALESCE(poe_level,0),
COALESCE(counterevidence,''), COALESCE(summary,''), COALESCE(evidence,''), COALESCE(confidence,0)
FROM findings
WHERE task_id=$1 AND (COALESCE(counterevidence,'') <> '' OR (COALESCE(poe_level,0) >= 3 AND COALESCE(verification_status,'') = $2))`,
		taskID, VerificationVerified)
	if err != nil {
		return
	}
	defer rows.Close()
	for rows.Next() {
		var id, poe, conf int64
		var name, vulnclass, counterevidence, summary, evidence string
		if err := rows.Scan(&id, &name, &vulnclass, &poe, &counterevidence, &summary, &evidence, &conf); err != nil {
			return
		}
		title := findingKnowledgeTitle(name, vulnclass)
		vulnScope := strings.ToLower(strings.TrimSpace(vulnclass))
		if counterevidence != "" {
			upsert(KnowledgeItem{Kind: KnowledgeFailedHypothesis, Title: title,
				Body: counterevidence, Confidence: 50, ScopeVulnclass: vulnScope,
				Source: knowledgeSourceOf(knowledgeSourceFinding, id, KnowledgeFailedHypothesis, title, counterevidence)})
		}
		if poe >= 3 {
			body := summary
			if body == "" {
				body = evidence
			}
			upsert(KnowledgeItem{Kind: KnowledgeSuccessfulApproach, Title: title,
				Body: body, Confidence: int(conf), ScopeVulnclass: vulnScope,
				Source: knowledgeSourceOf(knowledgeSourceFinding, id, KnowledgeSuccessfulApproach, title, body)})
		}
	}
}

// findingKnowledgeTitle prefers the reporter's name and falls back to the class.
func findingKnowledgeTitle(name, vulnclass string) string {
	if n := strings.TrimSpace(name); n != "" {
		return n
	}
	return strings.TrimSpace(vulnclass)
}

// extractPathKnowledge turns every non-refuted attack path into one chain item
// whose body is the ordered step list. The path is the source, so an unchanged
// path re-extracted later is the same evidence rather than a second one.
func (d *DB) extractPathKnowledge(taskID int64, upsert func(KnowledgeItem)) {
	paths, err := d.ListAttackPaths(taskID, nil, 0)
	if err != nil {
		return
	}
	for _, p := range paths {
		if p == nil || p.State == "refuted" {
			continue
		}
		_, steps, err := d.GetAttackPath(p.ID)
		if err != nil {
			continue
		}
		var b strings.Builder
		for _, s := range steps {
			if s == nil {
				continue
			}
			fmt.Fprintf(&b, "%d %s %d %s\n", s.Position, s.Kind, s.RefID, s.Edge)
		}
		conf := int(p.Score)
		if conf > 100 {
			conf = 100
		}
		if conf < 0 {
			conf = 0
		}
		body := b.String()
		upsert(KnowledgeItem{Kind: KnowledgeChain, Title: p.Title, Body: body, Confidence: conf,
			Source: knowledgeSourceOf(knowledgeSourceExtraction, p.ID, KnowledgeChain, p.Title, body)})
	}
}
