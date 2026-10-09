package tests

import (
	"testing"

	"webtyp.com/changelog"
	"webtyp.com/changelog/migrate"
	"webtyp.com/ddl"
	"webtyp.com/model"
	"webtyp.com/orm"
	"webtyp.com/sqlite"
	"webtyp.com/storage"
	"webtyp.com/storage/conformance"
)

// A real SQL backend: mem hides that a transaction-bound executor is NOT a Compiler, that a
// read must select every column it scans, and that rows must be closed before the next
// statement on the same transaction. Postgres behaves like this; mem does not.
func setupSQLite(t *testing.T) (*changelog.Log, *orm.DB) {
	t.Helper()
	conn, err := sqlite.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	dc, ok := sqlite.DDLCompiler(conn)
	if !ok {
		t.Fatal("sqlite conn is not a ddl.Compiler")
	}
	if err := migrate.Migrate(conn, dc); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if err := ddl.New(conn, dc).CreateTable(&conformance.Widget{}); err != nil {
		t.Fatalf("create widget table: %v", err)
	}
	log, err := changelog.New(conn.(changelog.TxConn), func() model.Model { return &conformance.Widget{} })
	if err != nil {
		t.Fatalf("changelog.New: %v", err)
	}
	return log, orm.New(log)
}

func TestSQLite_CreateUpdateDeleteAreRecorded(t *testing.T) {
	log, db := setupSQLite(t)

	for _, id := range []string{"a", "b", "c"} {
		if err := db.Create(&conformance.Widget{Id: id, Name: id, Qty: 1, Active: true}); err != nil {
			t.Fatalf("create %s: %v", id, err)
		}
	}
	if err := db.Update(&conformance.Widget{Id: "a", Name: "a2", Qty: 2, Active: true}, storage.Eq("id", "a")); err != nil {
		t.Fatalf("update: %v", err)
	}
	if err := db.Delete(&conformance.Widget{}, storage.Eq("qty", int64(1))); err != nil {
		t.Fatalf("delete by non-PK condition: %v", err)
	}

	changes, err := log.Since(0, 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(changes) != 3 {
		t.Fatalf("want 3 entries (a upsert, b delete, c delete), got %d: %+v", len(changes), changes)
	}
	got := map[string]string{}
	for _, c := range changes {
		got[c.RowId] = c.Op
	}
	if got["a"] != string(changelog.OpUpsert) || got["b"] != string(changelog.OpDelete) || got["c"] != string(changelog.OpDelete) {
		t.Fatalf("unexpected ops: %v", got)
	}
	if log.Head() != 6 {
		t.Fatalf("head: want 6, got %d", log.Head())
	}
}

func TestSQLite_TxCommitAndRollback(t *testing.T) {
	log, db := setupSQLite(t)
	_ = db.Tx(func(tx *orm.DB) error {
		if err := tx.Create(&conformance.Widget{Id: "x", Name: "x", Qty: 1, Active: true}); err != nil {
			t.Fatalf("create in tx: %v", err)
		}
		return errRollback
	})
	if log.Head() != 0 {
		t.Fatalf("rolled back tx must not move head, got %d", log.Head())
	}
	if err := db.Tx(func(tx *orm.DB) error {
		return tx.Create(&conformance.Widget{Id: "y", Name: "y", Qty: 1, Active: true})
	}); err != nil {
		t.Fatalf("committed tx: %v", err)
	}
	if log.Head() != 1 {
		t.Fatalf("committed tx: want head 1, got %d", log.Head())
	}
}

type rollbackErr string

func (e rollbackErr) Error() string { return string(e) }

const errRollback rollbackErr = "rollback on purpose"
