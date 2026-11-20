-- ArKex 2.0 Phase 2/3: OAST, auth vault, CPG sinks, fuzz jobs, CAS blobs.

CREATE TABLE IF NOT EXISTS oast_callbacks (
    id            BIGSERIAL PRIMARY KEY,
    task_id       TEXT NOT NULL DEFAULT '',
    intent_id     TEXT NOT NULL DEFAULT '',
    payload_tag   TEXT NOT NULL UNIQUE,
    protocol      TEXT NOT NULL DEFAULT 'pending',
    remote_addr   TEXT NOT NULL DEFAULT '',
    data          JSONB NOT NULL DEFAULT '{}',
    received_at   TIMESTAMPTZ,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_oast_pending ON oast_callbacks(protocol) WHERE protocol = 'pending';
CREATE INDEX IF NOT EXISTS idx_oast_task ON oast_callbacks(task_id, created_at DESC);

CREATE TABLE IF NOT EXISTS auth_identities (
    id            BIGSERIAL PRIMARY KEY,
    task_id       TEXT NOT NULL,
    label         TEXT NOT NULL,
    kind          TEXT NOT NULL DEFAULT 'cookie',
    credentials   BYTEA NOT NULL,
    expires_at    TIMESTAMPTZ,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (task_id, label)
);

CREATE TABLE IF NOT EXISTS cpg_sinks (
    id            BIGSERIAL PRIMARY KEY,
    task_id       TEXT NOT NULL DEFAULT '',
    file_path     TEXT NOT NULL,
    line_no       INT NOT NULL DEFAULT 0,
    sink_kind     TEXT NOT NULL,
    source_line   TEXT NOT NULL DEFAULT '',
    language      TEXT NOT NULL DEFAULT '',
    confidence    REAL NOT NULL DEFAULT 0.5,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_cpg_sinks_task ON cpg_sinks(task_id, sink_kind);

CREATE TABLE IF NOT EXISTS fuzz_jobs (
    id            BIGSERIAL PRIMARY KEY,
    task_id       TEXT NOT NULL DEFAULT '',
    target_kind   TEXT NOT NULL DEFAULT '',
    target_ref    TEXT NOT NULL DEFAULT '',
    fuzzer        TEXT NOT NULL DEFAULT '',
    status        TEXT NOT NULL DEFAULT 'queued',
    container_id  TEXT NOT NULL DEFAULT '',
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS blobs (
    sha256           TEXT PRIMARY KEY,
    size_original    BIGINT NOT NULL DEFAULT 0,
    size_compressed  BIGINT NOT NULL DEFAULT 0,
    data_zstd        BYTEA NOT NULL,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS lab_desync_runs (
    id            BIGSERIAL PRIMARY KEY,
    task_id       TEXT NOT NULL DEFAULT '',
    target_url    TEXT NOT NULL,
    results       JSONB NOT NULL DEFAULT '[]',
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);
