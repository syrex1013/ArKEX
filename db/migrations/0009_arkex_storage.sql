-- ArKex 2.0 Phase 1: delta LLM storage + raw-wire ring buffer + storage metrics.

CREATE TABLE IF NOT EXISTS llm_messages (
    session_id   TEXT NOT NULL,
    turn_seq     INT  NOT NULL,
    role         TEXT NOT NULL DEFAULT '',
    content_zstd BYTEA NOT NULL,
    sha256       TEXT NOT NULL,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (session_id, turn_seq)
);
CREATE INDEX IF NOT EXISTS idx_llm_messages_sha ON llm_messages(sha256);

CREATE TABLE IF NOT EXISTS llm_raw_debug (
    id           BIGSERIAL PRIMARY KEY,
    session_id   TEXT,
    task_id      TEXT,
    raw_request  BYTEA,
    raw_response BYTEA,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_llm_raw_debug_created ON llm_raw_debug(created_at);

CREATE TABLE IF NOT EXISTS storage_metrics (
    id              BIGSERIAL PRIMARY KEY,
    kind            TEXT NOT NULL,
    size_original   BIGINT NOT NULL DEFAULT 0,
    size_compressed BIGINT NOT NULL DEFAULT 0,
    meta            JSONB NOT NULL DEFAULT '{}',
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_storage_metrics_kind ON storage_metrics(kind, created_at DESC);

-- Delta pointer columns on llm_records (nullable for legacy rows).
ALTER TABLE llm_records ADD COLUMN IF NOT EXISTS up_to_seq INT;
ALTER TABLE llm_records ADD COLUMN IF NOT EXISTS request_sha256 TEXT;
