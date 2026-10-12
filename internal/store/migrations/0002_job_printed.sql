-- A job is sent once; printing another copy needs a new approval.
ALTER TABLE jobs ADD COLUMN printed_at TEXT;
ALTER TABLE jobs ADD COLUMN version INTEGER NOT NULL DEFAULT 0;
ALTER TABLE jobs ADD COLUMN estimate TEXT; -- estimate JSON shown beside the Approve button
