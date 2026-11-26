-- Evidence-backed knowledge: a knowledge item keeps the sources that support it,
-- so support counts distinct evidence instead of calls, an operator can see why a
-- fact is believed, and re-extracting the identical source never inflates it.
-- Also adds the operator review state (current / disputed / due_revalidation) and
-- supersession: a replacement retires its predecessor, history is never rewritten.
--
-- Idempotent: safe to re-run over an existing database. The backfill and repair
-- passes below only add evidence rows, recompute support, or narrow an
-- unreachable row's scope; nothing is deleted.
CREATE TABLE IF NOT EXISTS knowledge_item_sources (
    id BIGSERIAL PRIMARY KEY,
    item_id BIGINT NOT NULL REFERENCES knowledge_items(id) ON DELETE CASCADE,
    source_kind TEXT NOT NULL CHECK (source_kind IN ('task','finding','checkpoint','operator','extraction')),
    source_id BIGINT NOT NULL,
    content_hash TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
-- One row per distinct evidence occurrence: the same source extracted twice
-- (identical content hash) collapses, so support cannot be inflated by re-runs.
CREATE UNIQUE INDEX IF NOT EXISTS uq_knowledge_source ON knowledge_item_sources(item_id, source_kind, source_id, content_hash);
CREATE INDEX IF NOT EXISTS idx_knowledge_sources_item ON knowledge_item_sources(item_id);

-- Review state is operator judgement, separate from status (which is evidence
-- judgement: active / refuted / retired).
ALTER TABLE knowledge_items
    ADD COLUMN IF NOT EXISTS review_state TEXT NOT NULL DEFAULT 'current' CHECK (review_state IN ('current','disputed','due_revalidation')),
    ADD COLUMN IF NOT EXISTS superseded_by BIGINT REFERENCES knowledge_items(id) ON DELETE SET NULL,
    ADD COLUMN IF NOT EXISTS revalidate_after TIMESTAMPTZ;
-- Only the minority of items that need attention are indexed.
CREATE INDEX IF NOT EXISTS idx_knowledge_review_state ON knowledge_items(review_state) WHERE review_state <> 'current';

-- Backfill existing provenance: an item extracted from a task already names that
-- task in source_task_id, so record it as real evidence and recompute support
-- from the evidence table. Rows without provenance keep their legacy support.
-- One evidence row per (item, task): the NOT EXISTS keeps a re-run from adding a
-- second row after a repair pass has edited the body, so the hash below only
-- records the content as it was when the row was first captured.
INSERT INTO knowledge_item_sources (item_id, source_kind, source_id, content_hash)
SELECT k.id, 'task', k.source_task_id, md5(k.kind || E'\x1f' || k.title || E'\x1f' || k.body)
FROM knowledge_items k
WHERE k.source_task_id IS NOT NULL
  AND NOT EXISTS (
      SELECT 1 FROM knowledge_item_sources s
      WHERE s.item_id = k.id AND s.source_kind = 'task' AND s.source_id = k.source_task_id)
ON CONFLICT DO NOTHING;

UPDATE knowledge_items k
SET support = e.n
FROM (
    SELECT item_id, count(DISTINCT (source_kind, source_id))::int AS n
    FROM knowledge_item_sources
    GROUP BY item_id
) e
WHERE k.id = e.item_id
  AND k.source_task_id IS NOT NULL;

-- Orphan repair: an item that matches no scope can never be retrieved again, so
-- attach it to the narrowest scope that still describes where it came from. The
-- row is kept — evidence is never deleted.
--
-- (a) give it the engagement its source task ran for, when there is one. A row
-- that would collide with an existing item on the identity key keeps its current
-- scope instead: the migration must not fail on a duplicate.
UPDATE knowledge_items k
SET scope_engagement_id = t.engagement_id
FROM tasks t
WHERE k.source_task_id = t.id
  AND t.engagement_id IS NOT NULL
  AND k.scope_company_id IS NULL
  AND k.scope_engagement_id IS NULL
  AND k.scope_asset_key = ''
  AND NOT EXISTS (
      SELECT 1 FROM knowledge_items d
      WHERE d.id <> k.id
        AND d.kind = k.kind
        AND d.title = k.title
        AND d.scope_company_id IS NULL
        AND d.scope_engagement_id = t.engagement_id
        AND d.scope_asset_key = ''
        AND d.scope_vulnclass = k.scope_vulnclass);

-- (b) otherwise fall back to the source task's first linked asset key (domain,
-- else ip, else url, lowercased), skipping tasks with no usable key.
UPDATE knowledge_items k
SET scope_asset_key = s.asset_key
FROM (
    SELECT DISTINCT ON (l.task_id) l.task_id,
           lower(COALESCE(NULLIF(a.domain,''), NULLIF(a.ip,''), NULLIF(a.url,''))) AS asset_key
    FROM task_asset_links l
    JOIN assets a ON a.id = l.asset_id
    ORDER BY l.task_id, l.asset_id
) s
WHERE k.source_task_id = s.task_id
  AND s.asset_key <> ''
  AND k.scope_company_id IS NULL
  AND k.scope_engagement_id IS NULL
  AND k.scope_asset_key = ''
  AND NOT EXISTS (
      SELECT 1 FROM knowledge_items d
      WHERE d.id <> k.id
        AND d.kind = k.kind
        AND d.title = k.title
        AND d.scope_company_id IS NULL
        AND d.scope_engagement_id IS NULL
        AND d.scope_asset_key = s.asset_key
        AND d.scope_vulnclass = k.scope_vulnclass);

-- (c) whatever still has no resolvable scope is retired with a reason, so it
-- stays auditable but never surfaces as fresh knowledge. Scope columns stay
-- NULL/empty and the body keeps the original text plus the marker line.
UPDATE knowledge_items
SET status = 'retired',
    body = body || E'\n[retired: no resolvable scope]'
WHERE source_task_id IS NOT NULL
  AND scope_company_id IS NULL
  AND scope_engagement_id IS NULL
  AND scope_asset_key = ''
  AND status <> 'retired';
