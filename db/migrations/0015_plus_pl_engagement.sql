-- Scope the platform to the plus.pl / Polkomtel offensive assessment: every
-- existing task and conversation belongs to that engagement, and conversations
-- gain an optional engagement binding for scoped traffic views.

-- Legacy pre-0013 databases carry engagements.company_id; fresh installs don't
-- (0013's CREATE TABLE never had it), so make sure it exists before we set it.
ALTER TABLE engagements ADD COLUMN IF NOT EXISTS company_id BIGINT;

-- Conversations bind to an engagement the same way tasks do (SET NULL on delete).
ALTER TABLE conversations ADD COLUMN IF NOT EXISTS engagement_id BIGINT REFERENCES engagements(id) ON DELETE SET NULL;
CREATE INDEX IF NOT EXISTS idx_conversations_engagement ON conversations (engagement_id) WHERE engagement_id IS NOT NULL;

-- Seed the plus.pl engagement (idempotent), then move every task and every
-- already-bound conversation onto it: all existing work is plus.pl work.
INSERT INTO engagements (name, nkey, description, company_id)
VALUES ('plus.pl', 'plus.pl', 'plus.pl / Polkomtel offensive assessment', 1)
ON CONFLICT (nkey) DO NOTHING;

UPDATE tasks
SET engagement_id = (SELECT id FROM engagements WHERE nkey = 'plus.pl');

UPDATE conversations
SET engagement_id = (SELECT id FROM engagements WHERE nkey = 'plus.pl')
WHERE engagement_id IS NOT NULL;

-- Drop the leftover test-artifact engagement; its task detaches (ON DELETE SET NULL)
-- before this runs, because the UPDATE above moved every bound task to plus.pl.
DELETE FROM engagements WHERE nkey = 'acme corp pentest 2026';
