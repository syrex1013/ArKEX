-- Engagement records: the engagement-level history the task board does not keep.
-- scope revisions, operator decisions, milestones, generated briefs and plain
-- notes. Append-only: revisions and decisions keep superseded rows, the active
-- row is the latest accepted one. Every entry can point at the task, finding,
-- checkpoint or record it originated from (source_kind/source_id).
CREATE TABLE IF NOT EXISTS engagement_records (
    id BIGSERIAL PRIMARY KEY,
    engagement_id BIGINT NOT NULL REFERENCES engagements(id) ON DELETE CASCADE,
    kind TEXT NOT NULL CHECK (kind IN ('scope_revision','operator_decision','milestone','brief','note')),
    title TEXT NOT NULL,
    body TEXT NOT NULL DEFAULT '',
    actor TEXT NOT NULL DEFAULT '',
    status TEXT NOT NULL DEFAULT 'open' CHECK (status IN ('open','accepted','superseded','done')),
    due_at TIMESTAMPTZ,
    completed_at TIMESTAMPTZ,
    criteria TEXT NOT NULL DEFAULT '',
    supersedes_id BIGINT REFERENCES engagement_records(id) ON DELETE SET NULL,
    source_kind TEXT NOT NULL DEFAULT '' CHECK (source_kind IN ('','task','finding','record','checkpoint','event')),
    source_id BIGINT,
    payload JSONB NOT NULL DEFAULT '{}',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_engagement_records ON engagement_records(engagement_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_engagement_records_open ON engagement_records(engagement_id, kind, status) WHERE status IN ('open','accepted');

-- One row per operator review click: the cursor that powers "changes since my
-- last review" and the weekly brief delta.
CREATE TABLE IF NOT EXISTS engagement_reviews (
    id BIGSERIAL PRIMARY KEY,
    engagement_id BIGINT NOT NULL REFERENCES engagements(id) ON DELETE CASCADE,
    operator TEXT NOT NULL,
    reviewed_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    up_to_record_id BIGINT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_engagement_reviews ON engagement_reviews(engagement_id, operator, reviewed_at DESC);

-- Which knowledge items were actually shown to which agent run. The RSI
-- rsi_lesson_usage pattern: operators can diagnose stale or irrelevant context
-- without storing full prompts.
CREATE TABLE IF NOT EXISTS knowledge_injections (
    id BIGSERIAL PRIMARY KEY,
    item_id BIGINT NOT NULL REFERENCES knowledge_items(id) ON DELETE CASCADE,
    task_id BIGINT NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
    intent_id BIGINT,
    agent_name TEXT NOT NULL DEFAULT '',
    mode TEXT NOT NULL DEFAULT 'shadow' CHECK (mode IN ('shadow','inject')),
    shown_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_knowledge_injections_task ON knowledge_injections(task_id, shown_at DESC);
CREATE INDEX IF NOT EXISTS idx_knowledge_injections_item ON knowledge_injections(item_id);
