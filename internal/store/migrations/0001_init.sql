-- Structured state only; .scad, .stl, .png and .gcode.3mf files stay in /work (PRD FR-40).

CREATE TABLE settings (
    key        TEXT PRIMARY KEY,
    value      TEXT NOT NULL,              -- JSON
    created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
    updated_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
);

CREATE TABLE printers (
    id         TEXT PRIMARY KEY,
    kind       TEXT NOT NULL,              -- driver kind, e.g. bambu-lan
    name       TEXT NOT NULL,
    config     TEXT NOT NULL DEFAULT '{}', -- driver fields as JSON
    is_default INTEGER NOT NULL DEFAULT 0 CHECK (is_default IN (0, 1)),
    created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
    updated_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
);
CREATE UNIQUE INDEX printers_one_default ON printers (is_default) WHERE is_default = 1;

CREATE TABLE projects (
    name       TEXT PRIMARY KEY,
    created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
    updated_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
);

CREATE TABLE versions (
    project    TEXT NOT NULL REFERENCES projects (name) ON DELETE CASCADE,
    version    INTEGER NOT NULL,
    scad_path  TEXT NOT NULL,
    stl_path   TEXT NOT NULL,
    png_path   TEXT NOT NULL,
    stl_sha256 TEXT NOT NULL,
    params     TEXT NOT NULL DEFAULT '{}', -- parameters used, as JSON
    created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
    updated_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
    PRIMARY KEY (project, version)
);

CREATE TABLE estimates (
    key        TEXT PRIMARY KEY,           -- sha256(stl hash, printer id, profile hashes)
    project    TEXT NOT NULL,
    version    INTEGER NOT NULL,
    printer_id TEXT NOT NULL,
    status     TEXT NOT NULL CHECK (status IN ('running', 'done', 'failed')),
    result     TEXT,                       -- estimate JSON when done
    error      TEXT,                       -- slicer message when failed
    job_path   TEXT,                       -- sliced file, reused at approval (FR-38)
    created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
    updated_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
    FOREIGN KEY (project, version) REFERENCES versions (project, version) ON DELETE CASCADE
);

CREATE TABLE jobs (
    id          TEXT PRIMARY KEY CHECK (length(id) = 8),
    project     TEXT NOT NULL,
    printer_id  TEXT NOT NULL,
    file_path   TEXT NOT NULL,
    file_sha256 TEXT NOT NULL,
    approved_at TEXT,                      -- set only by Approve
    created_at  TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
    updated_at  TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
);

CREATE TABLE runs (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    project     TEXT,
    tool        TEXT NOT NULL,
    args        TEXT NOT NULL DEFAULT '{}',
    exit_code   INTEGER,
    message     TEXT,
    started_at  TEXT NOT NULL,
    finished_at TEXT,
    created_at  TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
    updated_at  TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
);
CREATE INDEX runs_by_project ON runs (project, started_at);
