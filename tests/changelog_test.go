package tests

import (
	"fmt"
	"sync"
	"testing"

	"webtyp.com/changelog"
	"webtyp.com/model"
	"webtyp.com/orm"
	"webtyp.com/storage"
	"webtyp.com/storage/conformance"
	"webtyp.com/storage/mem"
)

type Untracked struct {
	Id string
}
func (u *Untracked) ModelName() string { return "untracked" }
func (u *Untracked) Schema() []model.Field {
	return []model.Field{{Name: "id", Type: model.Text(), DB: &model.FieldDB{PK: true}}}
}
func (u *Untracked) Pointers() []any { return []any{&u.Id} }
func (u *Untracked) IsNil() bool { return u == nil }
func (u *Untracked) EncodeFields(w model.FieldWriter) {}
func (u *Untracked) DecodeFields(r model.FieldReader) {}
func (u *Untracked) Validate(action byte) error { return nil }

func setupTestDB(t *testing.T) (*changelog.Log, *orm.DB) {
	t.Helper()
	m := mem.New()
	txm := m.(changelog.TxConn)

	plan, _ := m.Compile(storage.Query{Action: storage.ActionCreate, Table: "change_log", Columns: []string{"version", "table_name", "row_id", "op"}, Values: []any{int64(0), "", "", ""}}, &changelog.Change{})
	m.Exec(plan.Query, plan.Args...)
	delPlan, _ := m.Compile(storage.Query{Action: storage.ActionDelete, Table: "change_log", Conditions: []storage.Condition{storage.Eq("version", int64(0))}}, &changelog.Change{})
	m.Exec(delPlan.Query, delPlan.Args...)

	log, err := changelog.New(txm, func() model.Model { return &conformance.Widget{} })
	if err != nil {
		t.Fatalf("failed to create log: %v", err)
	}

	db := orm.New(log)
	return log, db
}

func setupTestDBWithRaw(t *testing.T) (changelog.TxConn, *changelog.Log, *orm.DB) {
	t.Helper()
	m := mem.New()
	txm := m.(changelog.TxConn)
	plan, _ := m.Compile(storage.Query{Action: storage.ActionCreate, Table: "change_log", Columns: []string{"version", "table_name", "row_id", "op"}, Values: []any{int64(0), "", "", ""}}, &changelog.Change{})
	m.Exec(plan.Query, plan.Args...)
	delPlan, _ := m.Compile(storage.Query{Action: storage.ActionDelete, Table: "change_log", Conditions: []storage.Condition{storage.Eq("version", int64(0))}}, &changelog.Change{})
	m.Exec(delPlan.Query, delPlan.Args...)

	log, err := changelog.New(txm, func() model.Model { return &conformance.Widget{} })
	if err != nil {
		t.Fatalf("failed to create log: %v", err)
	}

	db := orm.New(log)
	return txm, log, db
}

func TestChangelog_Create3Widgets(t *testing.T) {
	log, db := setupTestDB(t)

	err := db.Create(&conformance.Widget{Id: "w1", Name: "Widget 1"})
	if err != nil { t.Fatal(err) }

	err = db.Create(&conformance.Widget{Id: "w2", Name: "Widget 2"})
	if err != nil { t.Fatal(err) }

	err = db.Create(&conformance.Widget{Id: "w3", Name: "Widget 3"})
	if err != nil { t.Fatal(err) }

	if h := log.Head(); h != 3 {
		t.Fatalf("expected head 3, got %d", h)
	}

	changes, err := log.Since(0, 100)
	if err != nil { t.Fatal(err) }

	if len(changes) != 3 {
		t.Fatalf("expected 3 changes, got %d", len(changes))
	}

	for i, c := range changes {
		if c.Version != int64(i+1) {
			t.Errorf("expected version %d, got %d", i+1, c.Version)
		}
		if c.Op != string(changelog.OpUpsert) {
			t.Errorf("expected op upsert, got %s", c.Op)
		}
		if c.RowId != fmt.Sprintf("w%d", i+1) {
			t.Errorf("expected row_id w%d, got %s", i+1, c.RowId)
		}
	}
}

func TestChangelog_UpdateCompaction(t *testing.T) {
	log, db := setupTestDB(t)

	w := &conformance.Widget{Id: "w1", Name: "Initial"}
	if err := db.Create(w); err != nil { t.Fatal(err) }
	if err := db.Create(&conformance.Widget{Id: "w2", Name: "w2"}); err != nil { t.Fatal(err) }
	if err := db.Create(&conformance.Widget{Id: "w3", Name: "w3"}); err != nil { t.Fatal(err) }

	// Update twice
	w.Name = "Updated 1"
	if err := db.Update(w, orm.Eq("id", "w1")); err != nil { t.Fatal(err) }
	w.Name = "Updated 2"
	if err := db.Update(w, orm.Eq("id", "w1")); err != nil { t.Fatal(err) }

	changes, err := log.Since(3, 100)
	if err != nil { t.Fatal(err) }

	if len(changes) != 1 {
		t.Fatalf("expected 1 change since version 3, got %d", len(changes))
	}
	if changes[0].Version != 5 || changes[0].RowId != "w1" {
		t.Errorf("unexpected change: %+v", changes[0])
	}

	changesAll, err := log.Since(0, 100)
	if err != nil { t.Fatal(err) }
	if len(changesAll) != 3 {
		t.Fatalf("expected 3 changes total (compaction), got %d", len(changesAll))
	}
}

func TestChangelog_DeleteNonPK(t *testing.T) {
	log, db := setupTestDB(t)

	db.Create(&conformance.Widget{Id: "w1", Name: "Shared"})
	db.Create(&conformance.Widget{Id: "w2", Name: "Shared"})

	headBefore := log.Head()

	if err := db.Delete(&conformance.Widget{}, orm.Eq("name", "Shared")); err != nil { t.Fatal(err) }

	changes, err := log.Since(headBefore, 100)
	if err != nil { t.Fatal(err) }

	if len(changes) != 2 {
		t.Fatalf("expected 2 delete changes, got %d", len(changes))
	}
	for _, c := range changes {
		if c.Op != string(changelog.OpDelete) {
			t.Errorf("expected op delete, got %s", c.Op)
		}
	}
}

func TestChangelog_UntrackedModel(t *testing.T) {
	log, db := setupTestDB(t)
	db.Create(&conformance.Widget{Id: "w1"})
	headBefore := log.Head()

	err := db.Create(&Untracked{Id: "u1"})
	if err != nil { t.Fatal(err) }

	if log.Head() != headBefore {
		t.Fatalf("expected head unchanged, got %d", log.Head())
	}
}

func TestChangelog_Tx(t *testing.T) {
	log, db := setupTestDB(t)
	db.Create(&conformance.Widget{Id: "w0"})

	// Error case
	headBefore := log.Head()
	err := db.Tx(func(tx *orm.DB) error {
		tx.Create(&conformance.Widget{Id: "tx1"})
		tx.Create(&conformance.Widget{Id: "tx2"})
		return fmt.Errorf("rollback")
	})
	if err == nil { t.Fatal("expected error") }
	if log.Head() != headBefore {
		t.Fatalf("expected head unchanged after rollback, got %d", log.Head())
	}

	// Success case
	err = db.Tx(func(tx *orm.DB) error {
		tx.Create(&conformance.Widget{Id: "tx3"})
		tx.Create(&conformance.Widget{Id: "tx4"})
		return nil
	})
	if err != nil { t.Fatal(err) }

	changes, err := log.Since(headBefore, 100)
	if err != nil { t.Fatal(err) }
	if len(changes) != 2 {
		t.Fatalf("expected 2 changes, got %d", len(changes))
	}
}

func TestChangelog_ConcurrentCreates(t *testing.T) {
	log, db := setupTestDB(t)

	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				id := fmt.Sprintf("w-%d-%d", g, j)
				err := db.Create(&conformance.Widget{Id: id})
				if err != nil { panic(err) }
			}
		}(i)
	}
	wg.Wait()

	changes, err := log.Since(0, 1000)
	if err != nil { t.Fatal(err) }

	if len(changes) != 400 {
		t.Fatalf("expected 400 changes, got %d", len(changes))
	}
	for i, c := range changes {
		if c.Version != int64(i+1) {
			t.Errorf("expected version %d, got %d", i+1, c.Version)
		}
	}
}

func TestChangelog_Restart(t *testing.T) {
	raw, log1, db1 := setupTestDBWithRaw(t)

	db1.Create(&conformance.Widget{Id: "w1"})
	db1.Create(&conformance.Widget{Id: "w2"})
	db1.Create(&conformance.Widget{Id: "w3"})

	log2, err := changelog.New(raw, func() model.Model { return &conformance.Widget{} })
	if err != nil { t.Fatal(err) }

	if log1.Head() != log2.Head() {
		t.Fatalf("expected head %d on restart, got %d", log1.Head(), log2.Head())
	}
}

type AutoIncModel struct {
	Id int
}
func (u *AutoIncModel) ModelName() string { return "autoinc" }
func (u *AutoIncModel) Schema() []model.Field {
	return []model.Field{{Name: "id", Type: model.Int(), DB: &model.FieldDB{PK: true, AutoInc: true}}}
}
func (u *AutoIncModel) Pointers() []any { return []any{&u.Id} }
func (u *AutoIncModel) IsNil() bool { return u == nil }
func (u *AutoIncModel) EncodeFields(w model.FieldWriter) {}
func (u *AutoIncModel) DecodeFields(r model.FieldReader) {}
func (u *AutoIncModel) Validate(action byte) error { return nil }

type NoPKModel struct {
	Id int
}
func (u *NoPKModel) ModelName() string { return "nopk" }
func (u *NoPKModel) Schema() []model.Field {
	return []model.Field{{Name: "id", Type: model.Int()}}
}
func (u *NoPKModel) Pointers() []any { return []any{&u.Id} }
func (u *NoPKModel) IsNil() bool { return u == nil }
func (u *NoPKModel) EncodeFields(w model.FieldWriter) {}
func (u *NoPKModel) DecodeFields(r model.FieldReader) {}
func (u *NoPKModel) Validate(action byte) error { return nil }

func TestChangelog_NewErrors(t *testing.T) {
	m := mem.New()
	txm := m.(changelog.TxConn)

	_, err := changelog.New(nil, func() model.Model { return &conformance.Widget{} })
	if err == nil { t.Error("expected error for nil conn") }

	_, err = changelog.New(txm)
	if err == nil { t.Error("expected error for no tracked models") }

	_, err = changelog.New(txm, func() model.Model { return &conformance.Widget{} }, func() model.Model { return &conformance.Widget{} })
	if err == nil { t.Error("expected error for duplicate model") }

	_, err = changelog.New(txm, func() model.Model { return &AutoIncModel{} })
	if err == nil { t.Error("expected error for auto-inc PK") }

	_, err = changelog.New(txm, func() model.Model { return &NoPKModel{} })
	if err == nil { t.Error("expected error for no PK") }
}

func TestChangelog_SinceError(t *testing.T) {
	log, _ := setupTestDB(t)
	_, err := log.Since(0, 0)
	if err == nil {
		t.Fatal("expected error on Since(0, 0)")
	}
}
