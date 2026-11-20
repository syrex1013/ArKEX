-- Team chat: stamp which operator opened a shared conversation.

ALTER TABLE conversations ADD COLUMN IF NOT EXISTS created_by TEXT NOT NULL DEFAULT '';
CREATE INDEX IF NOT EXISTS idx_conversations_created_by ON conversations(created_by) WHERE created_by <> '';
