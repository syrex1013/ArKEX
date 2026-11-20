-- ArKex 2.0 PostgreSQL Schema
-- Extensions
CREATE EXTENSION IF NOT EXISTS "uuid-ossp";
CREATE EXTENSION IF NOT EXISTS "pgcrypto";
CREATE EXTENSION IF NOT EXISTS "pg_trgm";
CREATE EXTENSION IF NOT EXISTS "vector";

-- ===== COMPANIES & ASSETS (Global Asset Graph) =====

CREATE TABLE IF NOT EXISTS companies (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name        TEXT NOT NULL,
    slug        TEXT UNIQUE NOT NULL,
    scope_raw   TEXT,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS assets (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    company_id  UUID NOT NULL REFERENCES companies(id) ON DELETE CASCADE,
    kind        TEXT NOT NULL CHECK (kind IN ('root_domain','subdomain','ip','cidr','service','app','endpoint','iam_principal','k8s_role','cloud_resource')),
    value       TEXT NOT NULL,
    metadata    JSONB NOT NULL DEFAULT '{}',
    tags        TEXT[] NOT NULL DEFAULT '{}',
    first_seen  TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_seen   TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (company_id, kind, value)
);
CREATE INDEX IF NOT EXISTS assets_company_kind ON assets(company_id, kind);
CREATE INDEX IF NOT EXISTS assets_tags ON assets USING gin(tags);
CREATE INDEX IF NOT EXISTS assets_meta ON assets USING gin(metadata);

CREATE TABLE IF NOT EXISTS asset_edges (
    id       UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    src_id   UUID NOT NULL REFERENCES assets(id) ON DELETE CASCADE,
    dst_id   UUID NOT NULL REFERENCES assets(id) ON DELETE CASCADE,
    rel      TEXT NOT NULL,
    weight   FLOAT NOT NULL DEFAULT 1.0,
    UNIQUE (src_id, dst_id, rel)
);

-- ===== ENGAGEMENTS (Tasks) =====

CREATE TABLE IF NOT EXISTS engagements (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    company_id      UUID NOT NULL REFERENCES companies(id) ON DELETE CASCADE,
    title           TEXT NOT NULL,
    goal            TEXT NOT NULL,
    status          TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending','running','paused','done','cancelled','timeout')),
    priority        INT NOT NULL DEFAULT 0,
    quiet_mode      BOOLEAN NOT NULL DEFAULT false,
    metadata        JSONB NOT NULL DEFAULT '{}',
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    started_at      TIMESTAMPTZ,
    finished_at     TIMESTAMPTZ
);
CREATE INDEX IF NOT EXISTS engagements_company ON engagements(company_id);
CREATE INDEX IF NOT EXISTS engagements_status ON engagements(status);

CREATE TABLE IF NOT EXISTS intents (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    engagement_id   UUID NOT NULL REFERENCES engagements(id) ON DELETE CASCADE,
    parent_id       UUID REFERENCES intents(id),
    kind            TEXT NOT NULL CHECK (kind IN ('plan','recon','probe','exploit','verify','report','lab')),
    title           TEXT NOT NULL,
    goal            TEXT NOT NULL,
    status          TEXT NOT NULL DEFAULT 'queued' CHECK (status IN ('queued','running','done','failed','killed','stalled')),
    worker_id       TEXT,
    model_tier      INT NOT NULL DEFAULT 1,
    tokens_in       BIGINT NOT NULL DEFAULT 0,
    tokens_out      BIGINT NOT NULL DEFAULT 0,
    scratch_bytes   BIGINT NOT NULL DEFAULT 0,
    metadata        JSONB NOT NULL DEFAULT '{}',
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    finished_at     TIMESTAMPTZ
);
CREATE INDEX IF NOT EXISTS intents_engagement ON intents(engagement_id);
CREATE INDEX IF NOT EXISTS intents_parent ON intents(parent_id);
CREATE INDEX IF NOT EXISTS intents_status ON intents(status);

-- ===== HYPOTHESIS DAG =====

CREATE TABLE IF NOT EXISTS hypotheses (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    engagement_id   UUID NOT NULL REFERENCES engagements(id) ON DELETE CASCADE,
    intent_id       UUID REFERENCES intents(id),
    title           TEXT NOT NULL,
    description     TEXT NOT NULL DEFAULT '',
    confidence      FLOAT NOT NULL DEFAULT 0.5 CHECK (confidence BETWEEN 0 AND 1),
    status          TEXT NOT NULL DEFAULT 'open' CHECK (status IN ('open','confirmed','refuted','deferred')),
    preconditions   JSONB NOT NULL DEFAULT '[]',
    tags            TEXT[] NOT NULL DEFAULT '{}',
    embedding       vector(1536),
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS hypo_engagement ON hypotheses(engagement_id);
CREATE INDEX IF NOT EXISTS hypo_embedding ON hypotheses USING ivfflat (embedding vector_cosine_ops) WITH (lists = 100);

CREATE TABLE IF NOT EXISTS hypothesis_edges (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    src_id      UUID NOT NULL REFERENCES hypotheses(id) ON DELETE CASCADE,
    dst_id      UUID NOT NULL REFERENCES hypotheses(id) ON DELETE CASCADE,
    rel         TEXT NOT NULL CHECK (rel IN ('spawns','derived_from','yields','proves','chains_with','covers','refutes')),
    weight      FLOAT NOT NULL DEFAULT 1.0,
    UNIQUE (src_id, dst_id, rel)
);

-- ===== FACTS (per-task exploration graph nodes) =====

CREATE TABLE IF NOT EXISTS facts (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    engagement_id   UUID NOT NULL REFERENCES engagements(id) ON DELETE CASCADE,
    intent_id       UUID REFERENCES intents(id),
    kind            TEXT NOT NULL,
    value           TEXT NOT NULL,
    confidence      FLOAT NOT NULL DEFAULT 1.0,
    source          TEXT,
    embedding       vector(1536),
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS facts_engagement ON facts(engagement_id);
CREATE INDEX IF NOT EXISTS facts_kind ON facts(engagement_id, kind);

-- ===== FINDINGS =====

CREATE TABLE IF NOT EXISTS findings (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    engagement_id   UUID NOT NULL REFERENCES engagements(id) ON DELETE CASCADE,
    intent_id       UUID REFERENCES intents(id),
    title           TEXT NOT NULL,
    description     TEXT NOT NULL DEFAULT '',
    severity        TEXT NOT NULL DEFAULT 'medium' CHECK (severity IN ('info','low','medium','high','critical')),
    status          TEXT NOT NULL DEFAULT 'open' CHECK (status IN ('open','triaged','verified','false_positive','wont_fix','fixed')),
    verification    TEXT NOT NULL DEFAULT 'unverified' CHECK (verification IN ('unverified','s1_http','s2_browser','s3_oast','s4_sandbox','hallucinated')),
    cvss_score      FLOAT,
    cve_ids         TEXT[] NOT NULL DEFAULT '{}',
    asset_ids       UUID[] NOT NULL DEFAULT '{}',
    evidence        JSONB NOT NULL DEFAULT '{}',
    embedding       vector(1536),
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS findings_engagement ON findings(engagement_id);
CREATE INDEX IF NOT EXISTS findings_severity ON findings(severity, status);
CREATE INDEX IF NOT EXISTS findings_verification ON findings(verification);

-- S1-S4 receipt tables

CREATE TABLE IF NOT EXISTS receipts_s1 (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    finding_id      UUID NOT NULL REFERENCES findings(id) ON DELETE CASCADE,
    traffic_id      UUID NOT NULL,
    verbatim_quote  TEXT NOT NULL,
    byte_offset     INT NOT NULL,
    byte_length     INT NOT NULL,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS receipts_s2 (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    finding_id      UUID NOT NULL REFERENCES findings(id) ON DELETE CASCADE,
    url             TEXT NOT NULL,
    dom_snapshot    BYTEA,
    screenshot_sha  TEXT,
    har_sha         TEXT,
    js_proof        TEXT,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS receipts_s3 (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    finding_id      UUID NOT NULL REFERENCES findings(id) ON DELETE CASCADE,
    oast_payload    TEXT NOT NULL,
    callback_kind   TEXT NOT NULL,
    callback_data   JSONB NOT NULL DEFAULT '{}',
    received_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS receipts_s4 (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    finding_id      UUID NOT NULL REFERENCES findings(id) ON DELETE CASCADE,
    container_id    TEXT NOT NULL,
    stdout_sha      TEXT NOT NULL,
    stderr_sha      TEXT NOT NULL,
    exit_code       INT NOT NULL,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- ===== DELTA LLM RECORDING (Phase 1 — no O(N²) duplication) =====

CREATE TABLE IF NOT EXISTS llm_messages (
    id              BIGSERIAL PRIMARY KEY,
    session_id      UUID NOT NULL,
    turn_seq        INT NOT NULL,
    role            TEXT NOT NULL CHECK (role IN ('system','user','assistant','tool')),
    content_zstd    BYTEA NOT NULL,
    sha256          TEXT NOT NULL,
    token_count     INT NOT NULL DEFAULT 0,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (session_id, turn_seq)
);
CREATE INDEX IF NOT EXISTS llm_messages_session ON llm_messages(session_id, turn_seq);

CREATE TABLE IF NOT EXISTS llm_calls (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    session_id      UUID NOT NULL,
    intent_id       UUID REFERENCES intents(id),
    up_to_seq       INT NOT NULL,
    response_seq    INT NOT NULL,
    model           TEXT NOT NULL,
    profile_id      TEXT,
    tokens_in       INT NOT NULL DEFAULT 0,
    tokens_out      INT NOT NULL DEFAULT 0,
    cache_read      INT NOT NULL DEFAULT 0,
    latency_ms      INT NOT NULL DEFAULT 0,
    status          TEXT NOT NULL DEFAULT 'ok',
    error           TEXT,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS llm_calls_session ON llm_calls(session_id);
CREATE INDEX IF NOT EXISTS llm_calls_intent ON llm_calls(intent_id);

-- Raw wire capture: disabled by default; 24h ring buffer via TTL
CREATE UNLOGGED TABLE IF NOT EXISTS llm_raw_debug (
    id          BIGSERIAL PRIMARY KEY,
    session_id  UUID NOT NULL,
    turn_seq    INT NOT NULL,
    direction   TEXT NOT NULL CHECK (direction IN ('request','response')),
    wire_zstd   BYTEA NOT NULL,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS llm_raw_debug_ttl ON llm_raw_debug(created_at);

-- ===== TRAFFIC STORE =====

CREATE TABLE IF NOT EXISTS traffic_sessions (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    engagement_id   UUID NOT NULL REFERENCES engagements(id) ON DELETE CASCADE,
    proxy_id        TEXT,
    client_addr     INET,
    tls_sni         TEXT,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS traffic_requests (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    session_id      UUID NOT NULL REFERENCES traffic_sessions(id) ON DELETE CASCADE,
    engagement_id   UUID NOT NULL REFERENCES engagements(id) ON DELETE CASCADE,
    method          TEXT NOT NULL,
    url             TEXT NOT NULL,
    host            TEXT NOT NULL,
    path            TEXT NOT NULL,
    status_code     INT,
    req_headers     BYTEA NOT NULL DEFAULT '',
    req_body_sha    TEXT,
    resp_headers    BYTEA NOT NULL DEFAULT '',
    resp_body_sha   TEXT,
    resp_body_len   INT NOT NULL DEFAULT 0,
    mime_type       TEXT,
    is_scanner      BOOLEAN NOT NULL DEFAULT false,
    is_static       BOOLEAN NOT NULL DEFAULT false,
    is_dedup        BOOLEAN NOT NULL DEFAULT false,
    latency_ms      INT,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS traffic_host ON traffic_requests(engagement_id, host);
CREATE INDEX IF NOT EXISTS traffic_method ON traffic_requests(method, status_code);
CREATE INDEX IF NOT EXISTS traffic_created ON traffic_requests(created_at);

-- FTS index on response bodies (≤64 KiB only, dropped after task closure)
CREATE TABLE IF NOT EXISTS traffic_fts (
    request_id  UUID PRIMARY KEY REFERENCES traffic_requests(id) ON DELETE CASCADE,
    body_text   TEXT NOT NULL,
    tsv         tsvector GENERATED ALWAYS AS (to_tsvector('english', body_text)) STORED
);
CREATE INDEX IF NOT EXISTS traffic_fts_tsv ON traffic_fts USING gin(tsv);
CREATE INDEX IF NOT EXISTS traffic_fts_trgm ON traffic_fts USING gin(body_text gin_trgm_ops);

-- Content-addressed blob store (FastCDC deduplicated bodies)
CREATE TABLE IF NOT EXISTS blobs (
    sha256          TEXT PRIMARY KEY,
    size_original   INT NOT NULL,
    size_compressed INT NOT NULL,
    data_zstd       BYTEA NOT NULL,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- ===== STORAGE METRICS =====

CREATE TABLE IF NOT EXISTS storage_metrics (
    id              BIGSERIAL PRIMARY KEY,
    recorded_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    pg_bytes        BIGINT NOT NULL DEFAULT 0,
    blob_bytes      BIGINT NOT NULL DEFAULT 0,
    traffic_fts_bytes BIGINT NOT NULL DEFAULT 0,
    scratch_bytes   BIGINT NOT NULL DEFAULT 0,
    cold_bytes      BIGINT NOT NULL DEFAULT 0,
    compression_ratio FLOAT NOT NULL DEFAULT 1.0,
    total_bytes     BIGINT GENERATED ALWAYS AS (pg_bytes + blob_bytes + scratch_bytes + cold_bytes) STORED
);
CREATE INDEX IF NOT EXISTS storage_metrics_time ON storage_metrics(recorded_at DESC);

-- ===== APPROVALS / INTERCEPTS =====

CREATE TABLE IF NOT EXISTS intercept_requests (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    engagement_id   UUID NOT NULL REFERENCES engagements(id) ON DELETE CASCADE,
    intent_id       UUID REFERENCES intents(id),
    kind            TEXT NOT NULL CHECK (kind IN ('target_gate','rate_limit','tool_use','scope_expand')),
    payload         JSONB NOT NULL DEFAULT '{}',
    resolved_ips    INET[],
    status          TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending','approved','denied','timeout')),
    decided_by      TEXT,
    decided_at      TIMESTAMPTZ,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS intercept_pending ON intercept_requests(status, created_at) WHERE status = 'pending';

-- ===== LLM SAFETY JUDGE LOG =====

CREATE TABLE IF NOT EXISTS safety_decisions (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    intent_id       UUID REFERENCES intents(id),
    action_kind     TEXT NOT NULL,
    action_payload  JSONB NOT NULL DEFAULT '{}',
    verdict         TEXT NOT NULL CHECK (verdict IN ('allow','deny')),
    rule_code       TEXT NOT NULL,
    rationale       TEXT,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- ===== OAST CALLBACKS =====

CREATE TABLE IF NOT EXISTS oast_callbacks (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    engagement_id   UUID REFERENCES engagements(id),
    intent_id       UUID REFERENCES intents(id),
    payload_tag     TEXT NOT NULL,
    protocol        TEXT NOT NULL CHECK (protocol IN ('dns','http','smtp','ldap','smb','ftp')),
    remote_addr     INET,
    data            JSONB NOT NULL DEFAULT '{}',
    received_at     TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS oast_tag ON oast_callbacks(payload_tag);
CREATE INDEX IF NOT EXISTS oast_intent ON oast_callbacks(intent_id);

-- ===== MULTI-IDENTITY AUTH VAULT =====

CREATE TABLE IF NOT EXISTS auth_identities (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    engagement_id   UUID NOT NULL REFERENCES engagements(id) ON DELETE CASCADE,
    label           TEXT NOT NULL CHECK (label IN ('unauth','user_a','user_b','admin','custom')),
    kind            TEXT NOT NULL CHECK (kind IN ('session_cookie','jwt','oauth2','api_key','basic','none')),
    credentials     BYTEA NOT NULL DEFAULT '',
    refresh_expr    JSONB,
    expires_at      TIMESTAMPTZ,
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (engagement_id, label)
);

-- ===== IDOR/BOLA DIFFERENTIAL RESULTS =====

CREATE TABLE IF NOT EXISTS idor_candidates (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    engagement_id   UUID NOT NULL REFERENCES engagements(id) ON DELETE CASCADE,
    request_id      UUID REFERENCES traffic_requests(id),
    baseline_id     TEXT NOT NULL,
    compared_id     TEXT NOT NULL,
    diff_score      FLOAT NOT NULL,
    diff_summary    TEXT,
    status          TEXT NOT NULL DEFAULT 'candidate' CHECK (status IN ('candidate','confirmed','false_positive')),
    finding_id      UUID REFERENCES findings(id),
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- ===== CROSS-TASK NEGATIVE RESULT INDEX =====

CREATE TABLE IF NOT EXISTS negative_results (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    asset_fp        TEXT NOT NULL,
    vuln_class      TEXT NOT NULL,
    method          TEXT NOT NULL,
    tested_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (asset_fp, vuln_class, method)
);
CREATE INDEX IF NOT EXISTS negative_fp ON negative_results(asset_fp);

-- ===== RIVER QUEUE TABLES (auto-managed by riverqueue/river) =====
-- river_jobs, river_queue, river_leader etc. are created by river.Migrate()

-- ===== LLM POOL HEALTH =====

CREATE TABLE IF NOT EXISTS llm_providers (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name            TEXT UNIQUE NOT NULL,
    tier            INT NOT NULL CHECK (tier IN (1,2,3)),
    endpoint        TEXT NOT NULL,
    model           TEXT NOT NULL,
    is_enabled      BOOLEAN NOT NULL DEFAULT true,
    is_healthy      BOOLEAN NOT NULL DEFAULT true,
    last_checked    TIMESTAMPTZ,
    refusal_rate    FLOAT NOT NULL DEFAULT 0,
    latency_p99_ms  INT NOT NULL DEFAULT 0,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS llm_refusal_log (
    id          BIGSERIAL PRIMARY KEY,
    provider_id UUID REFERENCES llm_providers(id),
    intent_id   UUID REFERENCES intents(id),
    model       TEXT NOT NULL,
    turn_seq    INT NOT NULL,
    reason      TEXT NOT NULL,
    failover_to UUID REFERENCES llm_providers(id),
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- ===== USERS & AUTH =====

CREATE TABLE IF NOT EXISTS users (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    email           TEXT UNIQUE NOT NULL,
    password_hash   TEXT NOT NULL,
    role            TEXT NOT NULL DEFAULT 'analyst' CHECK (role IN ('admin','analyst','viewer')),
    is_active       BOOLEAN NOT NULL DEFAULT true,
    last_login      TIMESTAMPTZ,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS sessions (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id     UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    token_hash  TEXT UNIQUE NOT NULL,
    expires_at  TIMESTAMPTZ NOT NULL,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS sessions_user ON sessions(user_id);
CREATE INDEX IF NOT EXISTS sessions_expires ON sessions(expires_at);

-- ===== NOTIFICATION RULES =====

CREATE TABLE IF NOT EXISTS notification_channels (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name        TEXT NOT NULL,
    kind        TEXT NOT NULL CHECK (kind IN ('telegram','slack','webhook','email')),
    config      JSONB NOT NULL DEFAULT '{}',
    is_enabled  BOOLEAN NOT NULL DEFAULT true,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS notification_rules (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    channel_id      UUID NOT NULL REFERENCES notification_channels(id) ON DELETE CASCADE,
    event_kind      TEXT NOT NULL,
    filter_expr     JSONB NOT NULL DEFAULT '{}',
    is_enabled      BOOLEAN NOT NULL DEFAULT true,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS notification_deliveries (
    id          BIGSERIAL PRIMARY KEY,
    rule_id     UUID REFERENCES notification_rules(id),
    event_id    TEXT NOT NULL,
    status      TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending','sent','failed')),
    error       TEXT,
    sent_at     TIMESTAMPTZ,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- ===== LOOT VAULT =====

CREATE TABLE IF NOT EXISTS loot (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    engagement_id   UUID NOT NULL REFERENCES engagements(id) ON DELETE CASCADE,
    intent_id       UUID REFERENCES intents(id),
    kind            TEXT NOT NULL CHECK (kind IN ('credential','token','key','hash','cookie','cert','source_code','config','secret','other')),
    label           TEXT NOT NULL,
    value_encrypted BYTEA NOT NULL,
    metadata        JSONB NOT NULL DEFAULT '{}',
    finding_id      UUID REFERENCES findings(id),
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS loot_engagement ON loot(engagement_id);

-- ===== DISCUSSION / COLLABORATION =====

CREATE TABLE IF NOT EXISTS discussions (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    engagement_id   UUID NOT NULL REFERENCES engagements(id) ON DELETE CASCADE,
    author_id       UUID REFERENCES users(id),
    parent_id       UUID REFERENCES discussions(id),
    body            TEXT NOT NULL,
    is_internal     BOOLEAN NOT NULL DEFAULT false,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS discussions_engagement ON discussions(engagement_id, created_at);

-- ===== CPG / ZERO-DAY RESEARCH =====

CREATE TABLE IF NOT EXISTS cpg_sinks (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    engagement_id   UUID NOT NULL REFERENCES engagements(id) ON DELETE CASCADE,
    file_path       TEXT NOT NULL,
    line_no         INT NOT NULL,
    sink_kind       TEXT NOT NULL,
    taint_path      TEXT,
    source_line     TEXT,
    language        TEXT NOT NULL,
    confidence      FLOAT NOT NULL DEFAULT 0.5,
    finding_id      UUID REFERENCES findings(id),
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS fuzz_jobs (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    engagement_id   UUID NOT NULL REFERENCES engagements(id) ON DELETE CASCADE,
    target_kind     TEXT NOT NULL,
    target_ref      TEXT NOT NULL,
    fuzzer          TEXT NOT NULL CHECK (fuzzer IN ('afl++','libfuzzer','jazzer','go-fuzz','atheris')),
    status          TEXT NOT NULL DEFAULT 'queued' CHECK (status IN ('queued','running','done','crashed','timeout')),
    coverage_pct    FLOAT,
    crash_count     INT NOT NULL DEFAULT 0,
    container_id    TEXT,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    finished_at     TIMESTAMPTZ
);

CREATE TABLE IF NOT EXISTS fuzz_crashes (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    fuzz_job_id     UUID NOT NULL REFERENCES fuzz_jobs(id) ON DELETE CASCADE,
    stack_hash      TEXT NOT NULL,
    reproducer_sha  TEXT,
    exploitability  TEXT NOT NULL DEFAULT 'unknown' CHECK (exploitability IN ('unknown','low','medium','high','critical')),
    finding_id      UUID REFERENCES findings(id),
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (fuzz_job_id, stack_hash)
);

-- ===== CLOUD IAM ATTACK PATH GRAPH =====

CREATE TABLE IF NOT EXISTS iam_principals (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    engagement_id   UUID NOT NULL REFERENCES engagements(id) ON DELETE CASCADE,
    provider        TEXT NOT NULL CHECK (provider IN ('aws','gcp','azure','entra','k8s')),
    kind            TEXT NOT NULL,
    arn             TEXT NOT NULL,
    name            TEXT,
    metadata        JSONB NOT NULL DEFAULT '{}',
    UNIQUE (engagement_id, provider, arn)
);

CREATE TABLE IF NOT EXISTS iam_edges (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    src_id      UUID NOT NULL REFERENCES iam_principals(id) ON DELETE CASCADE,
    dst_id      UUID NOT NULL REFERENCES iam_principals(id) ON DELETE CASCADE,
    rel         TEXT NOT NULL,
    conditions  JSONB NOT NULL DEFAULT '{}',
    UNIQUE (src_id, dst_id, rel)
);

-- ===== RETEST QUEUE =====

CREATE TABLE IF NOT EXISTS retests (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    finding_id      UUID NOT NULL REFERENCES findings(id) ON DELETE CASCADE,
    requested_by    UUID REFERENCES users(id),
    status          TEXT NOT NULL DEFAULT 'queued' CHECK (status IN ('queued','running','verified_fixed','still_vulnerable','error')),
    result_summary  TEXT,
    intent_id       UUID REFERENCES intents(id),
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    finished_at     TIMESTAMPTZ
);

-- ===== HELPER: updated_at trigger =====

CREATE OR REPLACE FUNCTION set_updated_at()
RETURNS TRIGGER AS $$
BEGIN NEW.updated_at = now(); RETURN NEW; END;
$$ LANGUAGE plpgsql;

DO $$ DECLARE t TEXT; BEGIN
    FOREACH t IN ARRAY ARRAY['companies','assets','engagements','intents','hypotheses','findings','auth_identities','discussions'] LOOP
        EXECUTE format('DROP TRIGGER IF EXISTS trig_updated_at_%I ON %I', t, t);
        EXECUTE format('CREATE TRIGGER trig_updated_at_%I BEFORE UPDATE ON %I FOR EACH ROW EXECUTE FUNCTION set_updated_at()', t, t);
    END LOOP;
END $$;
