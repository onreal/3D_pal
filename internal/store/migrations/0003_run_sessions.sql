-- Which agent session (and client) made each tool call.
ALTER TABLE runs ADD COLUMN session_id TEXT;
ALTER TABLE runs ADD COLUMN client TEXT;
CREATE INDEX runs_by_session ON runs (session_id, id);
