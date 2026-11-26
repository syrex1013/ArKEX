-- Long-horizon swarm: durable per-task agents, intent checkpoints, peer discovery
-- events, evidence-backed attack paths, project knowledge and lifecycle events.
-- All DDL is idempotent: schema.sql is re-executed on every startup and this file
-- is applied once after it.
CREATE TABLE IF NOT EXISTS task_agents (
    id BIGSERIAL PRIMARY KEY,
    task_id BIGINT NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
    slot INT NOT NULL,
    name TEXT NOT NULL,
    role TEXT NOT NULL CHECK (role IN ('recon','web','api','exploit','verify','general','chain')),
    state TEXT NOT NULL DEFAULT 'idle' CHECK (state IN ('idle','working','cooldown','retired')),
    current_intent_id BIGINT,
    last_heartbeat_at TIMESTAMPTZ,
    intents_done INT NOT NULL DEFAULT 0,
    findings INT NOT NULL DEFAULT 0,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (task_id, slot),
    UNIQUE (task_id, name)
);

CREATE TABLE IF NOT EXISTS intent_checkpoints (
    id BIGSERIAL PRIMARY KEY,
    task_id BIGINT NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
    intent_id BIGINT NOT NULL REFERENCES exploration_nodes(id) ON DELETE CASCADE,
    agent_name TEXT NOT NULL DEFAULT '',
    attempt INT NOT NULL DEFAULT 1,
    phase TEXT NOT NULL DEFAULT '',
    next_step TEXT NOT NULL DEFAULT '',
    done_items JSONB NOT NULL DEFAULT '[]',
    open_items JSONB NOT NULL DEFAULT '[]',
    artifacts JSONB NOT NULL DEFAULT '[]',
    tool_calls INT NOT NULL DEFAULT 0,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_intent_checkpoints_intent ON intent_checkpoints(intent_id, id DESC);

CREATE TABLE IF NOT EXISTS discovery_events (
    id BIGSERIAL PRIMARY KEY,
    task_id BIGINT NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
    intent_id BIGINT,
    agent_name TEXT NOT NULL DEFAULT '',
    kind TEXT NOT NULL CHECK (kind IN ('fact','finding','asset','path','note')),
    summary TEXT NOT NULL DEFAULT '',
    node_id BIGINT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_discovery_events_task ON discovery_events(task_id, id DESC);

CREATE TABLE IF NOT EXISTS attack_paths (
    id BIGSERIAL PRIMARY KEY,
    task_id BIGINT NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
    title TEXT NOT NULL,
    rationale TEXT NOT NULL DEFAULT '',
    state TEXT NOT NULL DEFAULT 'proposed' CHECK (state IN ('proposed','validating','validated','refuted')),
    score DOUBLE PRECISION NOT NULL DEFAULT 0,
    refute_reason TEXT NOT NULL DEFAULT '',
    created_by TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_attack_paths_task ON attack_paths(task_id, score DESC);

CREATE TABLE IF NOT EXISTS attack_path_steps (
    id BIGSERIAL PRIMARY KEY,
    path_id BIGINT NOT NULL REFERENCES attack_paths(id) ON DELETE CASCADE,
    position INT NOT NULL,
    kind TEXT NOT NULL CHECK (kind IN ('asset','finding','fact')),
    ref_id BIGINT NOT NULL,
    edge TEXT NOT NULL DEFAULT 'enables' CHECK (edge IN ('enables','requires','pivots_to','escalates')),
    precondition TEXT NOT NULL DEFAULT '',
    postcondition TEXT NOT NULL DEFAULT '',
    status TEXT NOT NULL DEFAULT 'proposed' CHECK (status IN ('proposed','verified','refuted')),
    evidence_note TEXT NOT NULL DEFAULT '',
    validated_by TEXT NOT NULL DEFAULT '',
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (path_id, position)
);

CREATE TABLE IF NOT EXISTS chain_cursor (
    task_id BIGINT PRIMARY KEY REFERENCES tasks(id) ON DELETE CASCADE,
    last_finding_id BIGINT NOT NULL DEFAULT 0,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS knowledge_items (
    id BIGSERIAL PRIMARY KEY,
    kind TEXT NOT NULL CHECK (kind IN ('asset_fact','dependency','attempt','failed_hypothesis','successful_approach','chain','lesson')),
    title TEXT NOT NULL,
    body TEXT NOT NULL DEFAULT '',
    scope_company_id BIGINT REFERENCES companies(id) ON DELETE SET NULL,
    scope_engagement_id BIGINT REFERENCES engagements(id) ON DELETE SET NULL,
    scope_asset_key TEXT NOT NULL DEFAULT '',
    scope_vulnclass TEXT NOT NULL DEFAULT '',
    confidence SMALLINT NOT NULL DEFAULT 40,
    support INT NOT NULL DEFAULT 1,
    source_task_id BIGINT REFERENCES tasks(id) ON DELETE SET NULL,
    status TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('active','refuted','retired')),
    first_seen TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_seen TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_knowledge_scope ON knowledge_items(scope_company_id, status);
CREATE INDEX IF NOT EXISTS idx_knowledge_asset ON knowledge_items(scope_asset_key) WHERE scope_asset_key <> '';
CREATE UNIQUE INDEX IF NOT EXISTS uq_knowledge_identity ON knowledge_items
    (kind, title, scope_company_id, scope_engagement_id, scope_asset_key, scope_vulnclass) NULLS NOT DISTINCT;

CREATE TABLE IF NOT EXISTS agent_events (
    id BIGSERIAL PRIMARY KEY,
    task_id BIGINT NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
    agent_name TEXT NOT NULL DEFAULT '',
    intent_id BIGINT,
    kind TEXT NOT NULL,
    detail JSONB NOT NULL DEFAULT '{}',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_agent_events_task ON agent_events(task_id, id DESC);

-- Finding identity: the dedupe fingerprint and the evidence-derived confidence.
ALTER TABLE findings ADD COLUMN IF NOT EXISTS confidence SMALLINT NOT NULL DEFAULT 0;
ALTER TABLE findings ADD COLUMN IF NOT EXISTS fingerprint TEXT NOT NULL DEFAULT '';
ALTER TABLE findings ADD COLUMN IF NOT EXISTS duplicate_of BIGINT REFERENCES findings(id) ON DELETE SET NULL;
CREATE INDEX IF NOT EXISTS idx_findings_fingerprint ON findings(task_id, fingerprint) WHERE fingerprint <> '';

-- agents.role gains 'chain' (the correlation agent, not intent-driven).
ALTER TABLE agents DROP CONSTRAINT IF EXISTS agents_role_ck;
ALTER TABLE agents ADD CONSTRAINT agents_role_ck
    CHECK (role IN ('goals','main','planner','worker','assistant','chain'));
