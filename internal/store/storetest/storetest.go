// Package storetest gives each test a fresh, migrated database (PRD FR-45).
package storetest

import (
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/margarit/printpipe/internal/store"
)

// Open opens a fresh migrated database in a temp dir, closed when the test ends.
func Open(t testing.TB) *sql.DB {
	t.Helper()
	db, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}
