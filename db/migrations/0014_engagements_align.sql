-- Align the pre-existing legacy engagements table with the engagement API
-- contract (name, nkey, description, created_at, updated_at). 0013's CREATE
-- TABLE IF NOT EXISTS no-opped on the legacy schema, so the missing columns
-- are added here and backfilled. Legacy extras (status, company_id, dates,
-- settings) are preserved; the API does not touch them.
ALTER TABLE engagements ADD COLUMN IF NOT EXISTS updated_at TIMESTAMPTZ NOT NULL DEFAULT now();

ALTER TABLE engagements ADD COLUMN IF NOT EXISTS nkey TEXT;
-- trg_engagements_upd (set_updated_at) needs updated_at to exist, hence the
-- ordering above; this backfill fires that trigger.
UPDATE engagements SET nkey = lower(trim(name)) WHERE nkey IS NULL;
-- Duplicates would break the unique index; keep the newest id per nkey.
DELETE FROM engagements a
USING engagements b
WHERE a.id <> b.id AND lower(trim(a.name)) = lower(trim(b.name)) AND a.id < b.id AND a.nkey = b.nkey;
CREATE UNIQUE INDEX IF NOT EXISTS uq_engagements_nkey ON engagements (nkey);
ALTER TABLE engagements ALTER COLUMN nkey SET NOT NULL;

