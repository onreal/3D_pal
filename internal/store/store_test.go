package store_test

import (
	"path/filepath"
	"testing"

	"github.com/margarit/printpipe/internal/store"
	"github.com/margarit/printpipe/internal/store/storetest"
)

func TestOpenAppliesMigrationsOnce(t *testing.T) {
	path := filepath.Join(t.TempDir(), "printpipe.db")
	db, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	v1, _ := store.SchemaVersion(db)
	db.Close()

	db, err = store.Open(path) // second start must not re-run anything
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	v2, _ := store.SchemaVersion(db)
	if v1 == 0 || v1 != v2 {
		t.Fatalf("schema version %d then %d", v1, v2)
	}
	for _, table := range []string{"settings", "printers", "projects", "versions", "estimates", "jobs", "runs"} {
		var n int
		if err := db.QueryRow(`SELECT count(*) FROM sqlite_master WHERE type='table' AND name=?`, table).Scan(&n); err != nil || n != 1 {
			t.Errorf("table %s missing", table)
		}
	}
}

func TestPragmas(t *testing.T) {
	db := storetest.Open(t)
	var mode string
	var fk, timeout int
	db.QueryRow(`PRAGMA journal_mode`).Scan(&mode)
	db.QueryRow(`PRAGMA foreign_keys`).Scan(&fk)
	db.QueryRow(`PRAGMA busy_timeout`).Scan(&timeout)
	if mode != "wal" || fk != 1 || timeout != 5000 {
		t.Fatalf("journal_mode=%s foreign_keys=%d busy_timeout=%d", mode, fk, timeout)
	}
}

func TestFailedMigrationLeavesDatabaseUnchanged(t *testing.T) {
	path := filepath.Join(t.TempDir(), "printpipe.db")
	bad := []store.Migration{
		{Version: 1, SQL: `CREATE TABLE a (x INTEGER);`},
		{Version: 2, SQL: `CREATE TABLE b (x INTEGER); THIS IS NOT SQL;`},
	}
	if _, err := store.OpenWith(path, bad); err == nil {
		t.Fatal("want error from broken migration")
	}
	db, err := store.OpenWith(path, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var n int
	db.QueryRow(`SELECT count(*) FROM sqlite_master WHERE name IN ('a','b')`).Scan(&n)
	if v, _ := store.SchemaVersion(db); n != 0 || v != 0 {
		t.Fatalf("partial migration kept: tables=%d version=%d", n, v)
	}
}
