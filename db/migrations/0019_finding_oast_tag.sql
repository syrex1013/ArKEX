-- S3 OAST-receipt finding verification: the payload tag a finding claims as its
-- out-of-band proof (minted with oast_payload, confirmed via oast_callbacks).
-- Empty = no OAST receipt claimed. Column is read by assessFindingVerification;
-- absence of a received callback never upgrades a finding (fail closed).
ALTER TABLE findings ADD COLUMN IF NOT EXISTS oast_tag TEXT NOT NULL DEFAULT '';
