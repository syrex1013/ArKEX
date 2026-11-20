-- Engagements: optional top-level grouping for tasks (client work, CTF events,
-- research lines). Tasks carry engagement_id; findings, stats and traffic derive
-- from their tasks. Deleting an engagement detaches tasks (SET NULL), never deletes.
CREATE TABLE IF NOT EXISTS engagements (
    id          BIGSERIAL PRIMARY KEY,
    name        TEXT NOT NULL,
    nkey        TEXT NOT NULL UNIQUE,
    description TEXT NOT NULL DEFAULT '',
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TRIGGER trg_engagements_upd BEFORE UPDATE ON engagements
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

ALTER TABLE tasks ADD COLUMN IF NOT EXISTS engagement_id BIGINT REFERENCES engagements(id) ON DELETE SET NULL;

CREATE INDEX IF NOT EXISTS idx_tasks_engagement ON tasks (engagement_id) WHERE deleted_at IS NULL;
