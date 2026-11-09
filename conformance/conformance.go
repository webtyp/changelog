package conformance

import (
	"testing"

	"webtyp.com/changelog"
	"webtyp.com/changelog/migrate"
	"webtyp.com/ddl"
	"webtyp.com/model"
	"webtyp.com/orm"
	"webtyp.com/storage"
	sc "webtyp.com/storage/conformance"
)

type Factory struct {
	Name string
	New  func(t *testing.T) (conn storage.Conn, compiler ddl.Compiler)
}

func Run(t *testing.T, f Factory) {
	t.Run("create_update_delete_are_recorded", func(t *testing.T) {
		log, db := setupLogAndDB(t, f)
		for _, id := range []string{"a", "b", "c"} {
			if err := db.Create(&sc.Widget{Id: id, Name: id, Qty: 1, Active: true}); err != nil {
				t.Fatalf("create %s: %v", id, err)
			}
		}
		if err := db.Update(&sc.Widget{Id: "a", Name: "a2", Qty: 2, Active: true}, storage.Eq("id", "a")); err != nil {
			t.Fatalf("update: %v", err)
		}
		if err := db.Delete(&sc.Widget{}, storage.Eq("qty", int64(1))); err != nil {
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
	})

	t.Run("tx_commit_and_rollback", func(t *testing.T) {
		log, db := setupLogAndDB(t, f)
		_ = db.Tx(func(tx *orm.DB) error {
			if err := tx.Create(&sc.Widget{Id: "x", Name: "x", Qty: 1, Active: true}); err != nil {
				t.Fatalf("create in tx: %v", err)
			}
			return errRollback
		})
		if log.Head() != 0 {
			t.Fatalf("rolled back tx must not move head, got %d", log.Head())
		}
		if err := db.Tx(func(tx *orm.DB) error {
			return tx.Create(&sc.Widget{Id: "y", Name: "y", Qty: 1, Active: true})
		}); err != nil {
			t.Fatalf("committed tx: %v", err)
		}
		if log.Head() != 1 {
			t.Fatalf("committed tx: want head 1, got %d", log.Head())
		}
	})
}

func setupLogAndDB(t *testing.T, f Factory) (*changelog.Log, *orm.DB) {
	t.Helper()
	conn, dc := f.New(t)
	if dc != nil {
		if err := migrate.Migrate(conn, dc); err != nil {
			t.Fatalf("migrate: %v", err)
		}
		if err := ddl.New(conn, dc).Sync(&sc.Widget{}); err != nil {
			t.Fatalf("create widget table: %v", err)
		}
	} else {
		// No DDL compiler, create tables manually
		plan, _ := conn.Compile(storage.Query{Action: storage.ActionCreate, Table: "change_log", Columns: []string{"version", "table_name", "row_id", "op"}, Values: []any{int64(0), "", "", ""}}, &changelog.Change{})
		_, _ = conn.Exec(plan.Query, plan.Args...)
		delPlan, _ := conn.Compile(storage.Query{Action: storage.ActionDelete, Table: "change_log", Conditions: []storage.Condition{storage.Eq("version", int64(0))}}, &changelog.Change{})
		_, _ = conn.Exec(delPlan.Query, delPlan.Args...)
	}

	log, err := changelog.New(conn.(changelog.TxConn), func() model.Model { return &sc.Widget{} })
	if err != nil {
		t.Fatalf("changelog.New: %v", err)
	}
	return log, orm.New(log)
}

type rollbackErr string

func (e rollbackErr) Error() string { return string(e) }

const errRollback rollbackErr = "rollback on purpose"
