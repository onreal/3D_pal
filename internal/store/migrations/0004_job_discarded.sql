-- A job the user no longer wants: it leaves the panel and can't be approved or sent.
ALTER TABLE jobs ADD COLUMN discarded_at TEXT;
