-- Repair installs whose tasks.engagement_id was tightened to NOT NULL (with a
-- hand-added legacy_engagement_id() default that selects nothing) after this
-- repository's migrations had already run: such a database rejects every task
-- creation with a not-null violation, while 0013 defines the column as nullable
-- and the API/UI legitimately create tasks with no engagement ("All
-- engagements"). Both statements are idempotent and no-ops on a schema that
-- already matches 0013.
ALTER TABLE tasks ALTER COLUMN engagement_id DROP DEFAULT;
ALTER TABLE tasks ALTER COLUMN engagement_id DROP NOT NULL;
