package storage

import (
	"context"
	"strings"
	"testing"
	"testing/fstest"

	"yuanfa-official/backend/internal/coordination"
	"yuanfa-official/backend/migrations"
)

func testLock(t *testing.T) *coordination.DataDirLock {
	t.Helper()
	lock, err := coordination.AcquireDataDir(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { lock.Close() })
	return lock
}
func TestTransactionsAndConsumerConstraints(t *testing.T) {
	ctx := context.Background()
	store, err := Open(ctx, testLock(t))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	db := store.DB()
	for pragma, want := range map[string]string{"journal_mode": "wal", "foreign_keys": "1", "busy_timeout": "5000"} {
		var got string
		if err = db.QueryRow("PRAGMA " + pragma).Scan(&got); err != nil || got != want {
			t.Fatalf("%s=%q, err=%v", pragma, got, err)
		}
	}
	if _, err = db.Exec(`INSERT INTO sessions(secret_hash,admin_id,created_at,expires_at) VALUES(?, 'missing','now','later')`, strings.Repeat("a", 64)); err == nil {
		t.Fatal("orphan session accepted")
	}
	if _, err = db.Exec(`INSERT INTO products VALUES('p_a','a','F30M',1,'{}',NULL,'now','now')`); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`INSERT INTO products VALUES('p_b','b','f30m',1,'{}',NULL,'now','now')`); err == nil {
		t.Fatal("case-insensitive duplicate model accepted")
	}
	if _, err = db.Exec(`INSERT INTO reserved_routes VALUES('a','product','p_a',1,'now')`); err != nil {
		t.Fatal(err)
	}
	for _, query := range []string{`DELETE FROM reserved_routes WHERE slug='a'`, `UPDATE reserved_routes SET owner_id='p_b' WHERE slug='a'`, `UPDATE reserved_routes SET ever_published=0 WHERE slug='a'`} {
		if _, err = db.Exec(query); err == nil {
			t.Fatalf("permanent route invariant violated: %s", query)
		}
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(`UPDATE workspace SET revision=revision+1 WHERE id=1`); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(`UPDATE products SET version=version+1 WHERE id='p_a'`); err != nil {
		t.Fatal(err)
	}
	if err = tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	var revision, version int
	if err = db.QueryRow(`SELECT revision FROM workspace WHERE id=1`).Scan(&revision); err != nil {
		t.Fatal(err)
	}
	if err = db.QueryRow(`SELECT version FROM products WHERE id='p_a'`).Scan(&version); err != nil {
		t.Fatal(err)
	}
	if revision != 0 || version != 1 {
		t.Fatalf("rollback lost atomicity: revision=%d version=%d", revision, version)
	}
}
func TestFailedMigrationRollsBackAndRefusesStore(t *testing.T) {
	ctx := context.Background()
	lock := testLock(t)
	initial, err := migrations.Files.ReadFile("001_initial.sql")
	if err != nil {
		t.Fatal(err)
	}
	bad := fstest.MapFS{
		"001_initial.sql": &fstest.MapFile{Data: initial},
		"002_broken.sql":  &fstest.MapFile{Data: []byte(`CREATE TABLE should_rollback(id INTEGER); INSERT INTO workspace(id,revision,dictionaries_json) VALUES(2,0,'{}');`)},
	}
	if store, err := open(ctx, lock, bad); err == nil || store != nil {
		t.Fatal("bad migration exposed writable Store")
	}
	store, err := Open(ctx, lock)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	var count int
	if err = store.DB().QueryRow(`SELECT count(*) FROM sqlite_master WHERE name='should_rollback'`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("partial DDL survived: %d %v", count, err)
	}
	if _, err = store.DB().Exec(`UPDATE schema_migrations SET sha256='wrong' WHERE version=1`); err != nil {
		t.Fatal(err)
	}
	if err = migrate(ctx, store.DB(), migrations.Files); err == nil {
		t.Fatal("tampered migration checksum accepted")
	}
	if err = store.DB().QueryRow(`SELECT revision FROM workspace WHERE id=1`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("failed history check wrote workspace: %d %v", count, err)
	}
	t.Log("bad SQL rolled back DDL and ledger; mismatched applied checksum refused before pending writes")
}
