// Package store opens the SQLite database and keeps its schema current (PRD FR-39 to FR-42).
package store

import (
	"database/sql"
	"embed"
	"fmt"
	"io/fs"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	_ "modernc.org/sqlite" // pure-Go driver, so CGO_ENABLED=0 builds still work
)

//go:embed migrations/*.sql
var migrationFiles embed.FS

type Migration struct {
	Version int
	SQL     string
}

// Migrations returns the embedded migrations, ordered by the number that starts each file name.
func Migrations() ([]Migration, error) {
	entries, err := fs.ReadDir(migrationFiles, "migrations")
	if err != nil {
		return nil, err
	}
	var ms []Migration
	for _, e := range entries {
		num, _, ok := strings.Cut(e.Name(), "_")
		v, err := strconv.Atoi(num)
		if !ok || err != nil {
			return nil, fmt.Errorf("migration %q: name must start with a number and _", e.Name())
		}
		body, err := migrationFiles.ReadFile("migrations/" + e.Name())
		if err != nil {
			return nil, err
		}
		ms = append(ms, Migration{Version: v, SQL: string(body)})
	}
	sort.Slice(ms, func(i, j int) bool { return ms[i].Version < ms[j].Version })
	return ms, nil
}

// Open opens the database at path and applies the embedded migrations.
func Open(path string) (*sql.DB, error) {
	ms, err := Migrations()
	if err != nil {
		return nil, err
	}
	return OpenWith(path, ms)
}

// OpenWith opens the database and applies the given migrations; tests use it to inject broken ones.
func OpenWith(path string, ms []Migration) (*sql.DB, error) {
	dsn := "file:" + filepath.ToSlash(path) +
		"?_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	// One connection: a single writer, so MCP and HTTP calls never see "database is locked".
	db.SetMaxOpenConns(1)
	if err := migrate(db, ms); err != nil {
		db.Close()
		return nil, err
	}
	return db, nil
}

// SchemaVersion is the highest applied migration, 0 for a new database.
func SchemaVersion(db *sql.DB) (int, error) {
	var v int
	err := db.QueryRow(`SELECT coalesce(max(version), 0) FROM schema_migrations`).Scan(&v)
	return v, err
}

// migrate applies every pending migration in one transaction: all of them land, or none.
func migrate(db *sql.DB, ms []Migration) error {
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS schema_migrations (
		version    INTEGER PRIMARY KEY,
		applied_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')))`); err != nil {
		return fmt.Errorf("database: %w", err)
	}
	current, err := SchemaVersion(db)
	if err != nil {
		return fmt.Errorf("database: %w", err)
	}
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, m := range ms {
		if m.Version <= current {
			continue
		}
		if _, err := tx.Exec(m.SQL); err != nil {
			return fmt.Errorf("database migration %d failed, nothing was changed: %w", m.Version, err)
		}
		if _, err := tx.Exec(`INSERT INTO schema_migrations (version) VALUES (?)`, m.Version); err != nil {
			return err
		}
	}
	return tx.Commit()
}
