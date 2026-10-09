---
PLAN: "feat: changelog — storage.Conn decorator that records every write to tracked tables, with Since(cursor)"
TAG: v0.1.0
EXECUTOR: jules
REVIEWER: none
STATUS: review
SESSION: 11764440257243267384
PR: https://github.com/webtyp/changelog/pull/1
---

> This plan is dispatched via the CodeJob workflow. See skill: agents-workflow.

# Plan — `changelog`: record every write, read back "what changed since X"

## 0. Context (read first)

This repository is new and empty (created with `gonew`; module `webtyp.com/changelog`).

An offline-first wave (master plan, in Spanish:
<https://github.com/veltylabs/mjosefa-cms/blob/main/docs/OFFLINE_FIRST_MASTER_PLAN.md>) keeps a
local copy of the database in every browser of a clinic. After a browser was offline, it asks
the server **"what changed since my cursor?"** and applies the answer locally. This library is
the server half of that question, and nothing else:

- It wraps a `storage.Conn` (the backend contract of `webtyp.com/storage`; `webtyp.com/orm`
  sits on top). Every **write** (create / update / delete) to a **tracked** table is recorded in
  a `change_log` table **in the same transaction**, with a strictly increasing `version`.
- `Since(cursor, limit)` returns the changes with `version > cursor`, oldest first: one entry per
  row (its **latest** change only), naming the table, the row id and whether it now exists
  (`upsert`) or not (`delete`). It does **not** store row contents: the sync library reads the
  current row when it needs it.

It knows nothing about browsers, users, HTTP or sync. A separate library (`webtyp/dbsync`) uses it.

Relevant contracts (from `webtyp.com/storage` v0.1.3 and `webtyp.com/orm` v0.12.8; read them
in the module cache after `go get`):

```go
// storage
type Conn interface { Executor; Compiler }
type Executor interface {
    Exec(query string, args ...any) error
    QueryRow(query string, args ...any) Scanner
    Query(query string, args ...any) (Rows, error)
    Close() error
}
type Compiler interface { Compile(q Query, m model.Model) (Plan, error) }
type Plan struct { Mode Action; Query string; Args []any }
type Query struct { Action Action; Table string; Columns []string; Values []any;
                    Conditions []Condition; OrderBy []Order; GroupBy []string; Limit, Offset int }
// Actions: ActionCreate, ActionReadOne, ActionUpdate, ActionDelete, ActionReadAll
type TxBoundExecutor interface { Executor; Commit() error; Rollback() error }
type TxExecutor interface { Executor; BeginTx() (TxBoundExecutor, error) }

// orm: how a write reaches the conn
//   plan, _ := conn.Compile(q, m); conn.Exec(plan.Query, plan.Args...)
// orm.DB.Tx(fn): bound, _ := conn.(storage.TxExecutor).BeginTx();
//   the tx DB uses boundConn{TxBoundExecutor: bound, Compiler: conn}
//   → inside a transaction, Compile still goes to the ORIGINAL conn and Exec goes to `bound`.
```

## Design gate

### 1. Prior art
- **CouchDB `_changes` feed**: every document write gets a sequence number; `?since=<seq>`
  returns the latest revision of each changed document, compacted (one entry per document).
  This plan copies that shape exactly (one entry per row, latest wins, cursor = sequence).
- **CockroachDB changefeeds / Debezium (CDC)**: change data capture from the engine's log.
  Rejected here: ties the solution to one engine (Postgres WAL), and the same code must also run
  over `storage/mem` in tests and possibly SQLite.
- **Replicache / Zero "version" per space** and **PowerSync `op_id`**: a server-assigned
  monotonic number per write, which clients use as their pull cursor. Same role as our `version`.
- **Transactional outbox (microservices)**: write the event row in the same transaction as the
  business row so they cannot diverge. We use that guarantee for `change_log`.

### 2. Novice-name test
`changelog.New(conn, ...)` → "a change log over this connection". `log.Since(cursor, 500)` →
"changes since cursor, at most 500". `log.Head()` → "the newest version". `changelog.OpUpsert`
/ `OpDelete` → "the row exists now" / "the row is gone".

### 3. Complexity ledger
```
Concepts the developer must learn   +3 (Log, Change, cursor)
Files they must touch to do X        +1 (wrap the server conn at the composition root)
Lines at the call site               +2 (New + pass it to orm.New)
Ways to do the same thing            0 (nothing in the ecosystem records changes today)
```

### 4. Where it belongs
A decorator over the storage port, so **no domain module changes** and every backend that
supports transactions works. It is its own repository because `storage` is the contract leaf
and must not grow a sync concern, and `dbsync` must stay usable with another change source.

### 5. What this deletes
Nothing: genuinely new capability.

## 1. Target API (exactly this is exported)

```go
package changelog

// TxConn is what New needs: a storage backend that supports transactions, so the change
// row commits atomically with the write it describes.
type TxConn interface {
    storage.Conn
    BeginTx() (storage.TxBoundExecutor, error)
}

// NewModel returns a fresh, empty instance of a tracked model. It is used to read the rows an
// update or delete is about to touch (their ids), never to return data to the caller.
type NewModel func() model.Model

// Op says what a row is after the change.
type Op string

const (
    OpUpsert Op = "upsert" // the row exists (created or updated)
    OpDelete Op = "delete" // the row no longer exists
)

// Log is a storage.Conn (and storage.TxExecutor): hand it to orm.New in place of the raw conn.
type Log struct { /* unexported */ }

// New wraps conn. Every create/update/delete on a table of one of the tracked models is
// recorded; other tables pass through untouched. The change_log table must already exist
// (see package changelog/migrate).
func New(conn TxConn, tracked ...NewModel) (*Log, error)

// Since returns the changes with version > cursor, ordered by version ascending, at most
// limit entries. limit must be > 0.
func (l *Log) Since(cursor int64, limit int) ([]Change, error)

// Head is the newest committed version (0 when nothing was ever recorded).
func (l *Log) Head() int64

// storage.Conn + storage.TxExecutor methods: Compile, Exec, QueryRow, Query, Close, BeginTx.
```

`Change` is generated by `ormc` from this definition in `model.go`:

```go
var ChangeModel = model.Definition{
    Name: "change_log",
    Fields: model.Fields{
        {Name: "version", Type: model.Int(), DB: &model.FieldDB{PK: true}},
        {Name: "table_name", Type: model.Text(), NotNull: true},
        {Name: "row_id", Type: model.Text(), NotNull: true},
        {Name: "op", Type: model.Text(), NotNull: true},
    },
}
```

Package `webtyp.com/changelog/migrate`:

```go
// Migrate creates the change_log table (and an index on (table_name, row_id)).
// Run it on the RAW backend conn, never on a *Log.
func Migrate(conn ddl.Execer, compiler ddl.Compiler) error
```
(Mirror the convention used by domain modules, e.g.
<https://github.com/veltylabs/clinical_encounter/blob/main/migrate/migrate.go>:
`return ddl.New(conn, compiler).CreateTable(&changelog.Change{})`; add the index the way
`webtyp.com/ddl` supports it — read its API; if `ddl` cannot express a non-unique index, skip the
index and say so in the README.)

## 2. Behaviour (normative)

### 2.1 New
Errors (unexported typed error constants, `type logError string` + `Error()`; never
`errors.New`), returned in this order of checks:
- `conn == nil` → `changelog: conn is required`
- `len(tracked) == 0` → `changelog: at least one tracked model is required`
- a factory returns nil → `changelog: tracked model factory returned nil`
- two factories with the same `ModelName()` → `changelog: model <name> tracked twice`
- a model without a PK field, or whose PK field `IsAutoInc()` →
  `changelog: tracked model <name> needs a caller-minted primary key` (an auto-increment id is
  unknown until the backend assigns it, so the change could not name its row)
- read the current head: a `ReadAll` on `change_log` ordered `storage.Desc("version")`,
  `Limit: 1`; empty → head 0. If the table does not exist, return that backend error wrapped as
  `changelog: change_log table missing, run changelog/migrate first: <err>`.

### 2.2 Writes outside a caller transaction
`Compile(q, m)`:
- `q.Action` is `ActionCreate`, `ActionUpdate` or `ActionDelete` **and** `m.ModelName()` is
  tracked → compile on the inner conn, then return
  `storage.Plan{Mode: inner.Mode, Query: inner.Query, Args: []any{&pendingWrite{query: q, model: m, plan: inner}}}`
  (`pendingWrite` is unexported).
- anything else → return the inner plan unchanged.

`Exec(query, args...)`:
- if `len(args) == 1` and `args[0]` is a `*pendingWrite` → tracked write (below).
- otherwise → `inner.Exec(query, args...)`.

Tracked write, all under one `sync.Mutex` held by the `Log` (it serialises **all** tracked writes
of this process, which is what makes versions commit in order — see §2.5):
1. `tx, err := inner.BeginTx()`; `defer tx.Rollback()` (safe after Commit by contract).
2. Row ids:
   - Create: the value at the index of the PK column in `q.Columns` / `q.Values`, formatted as
     text with `webtyp.com/fmt` `Convert(v).String()`. PK column missing → error
     `changelog: create on <table> has no primary key value`.
   - Update / Delete: build a fresh instance with the table's `NewModel`, compile
     `storage.Query{Action: storage.ActionReadAll, Table: q.Table, Conditions: q.Conditions}`
     on the inner conn, run it with `tx.Query`, `Scan` each row into the fresh instance's
     `Pointers()`, and read the PK with `model.ReadValues(schema, ptrs)[pkIndex]`. Collect ids.
     Zero rows → nothing to record (the write still executes).
3. `tx.Exec(plan.Query, plan.Args...)` with the **inner** plan.
4. For each id: `version = head + n` (n = 1, 2, …); delete the existing `change_log` rows with
   `table_name = q.Table AND row_id = id`; insert `Change{Version, TableName: q.Table, RowId: id,
   Op}` with `Op = OpDelete` for deletes and `OpUpsert` otherwise. Build both statements with
   `inner.Compile` on `&Change{}` and run them with `tx.Exec`.
5. `tx.Commit()`. Only after it succeeds, `head` = the last version used. On any error, head is
   unchanged and the error is returned.

### 2.3 Writes inside a caller transaction (`orm.DB.Tx`)
`BeginTx()` returns an unexported `*trackedTx` that wraps the inner `TxBoundExecutor`:
- `Exec` with a `*pendingWrite` → on the first tracked write, lock the `Log` mutex (held until
  `Commit`/`Rollback`); then steps 2–4 above against the inner tx, versions continuing from a
  tx-local counter that starts at `head`.
- `Commit` → inner commit; on success publish the tx-local counter to `head`; unlock if locked.
- `Rollback` → inner rollback; head unchanged; unlock if locked. Calling `Rollback` after
  `Commit` is a no-op (storage contract).
- `Exec` without `*pendingWrite`, `Query`, `QueryRow` → delegate to the inner tx.

### 2.4 Since / Head
`Since(cursor, limit)`: `limit <= 0` → `changelog: limit must be positive`. Read `change_log`
with `Gt("version", cursor)` and `Lte("version", head)` ordered ascending, `Limit: limit`, into
`[]Change`. `Head()` returns the in-memory head (read under the mutex or with an atomic).

### 2.5 Documented constraint
One process writes a given database through a `Log`. Versions are allocated in memory under a
mutex; a second writer process would allocate the same versions. Say this in the README and in
the `New` doc comment. (A multi-writer allocator would be a second implementation of an
allocation contract — out of scope.)

## 3. Stages

| Stage | Files | Content |
|---|---|---|
| 1 | `go.mod`, `model.go`, `model_orm.go` (generated) | `go get webtyp.com/storage webtyp.com/model webtyp.com/fmt webtyp.com/ddl webtyp.com/orm`; write `ChangeModel`; install and run the generator at the repo root: `go install webtyp.com/ormc/cmd/ormc@latest && ormc`. Commit the generated file. Delete the placeholder `changelog.go` content gonew created if it declares nothing useful |
| 2 | `log.go` | `TxConn`, `NewModel`, `Op`, `Log`, `New`, `Head`, `Since`, `Compile`, `Exec`, `QueryRow`, `Query`, `Close` |
| 3 | `tx.go` | `BeginTx`, `trackedTx` |
| 4 | `errors.go` | the typed error constants of §2 |
| 5 | `migrate/migrate.go` | `Migrate` |
| 6 | `tests/*.go` | §4 |
| 7 | `README.md`, `docs/ARCHITECTURE.md` | what it is, the "I want X → use Y" table, the one-writer constraint, the example of §5 |

## 4. Tests (`tests/`, external package; run with `gotest`)

Use `webtyp.com/storage/mem` as the backend (`mem.New()` implements `BeginTx`) and the exported
test model `webtyp.com/storage/conformance.Widget` (text PK `id`) as the tracked model. Create the
`change_log` table the way `mem` expects (it auto-creates on first insert; if not, call
`migrate.Migrate` with what `mem` supports, or insert-then-delete a `Change` row in the test
helper). All through **`orm.New(log)`** — a consumer-shaped test, never by calling `Exec` by hand.

1. Create 3 widgets → `Since(0, 100)` returns versions 1, 2, 3, `OpUpsert`, their ids, in order;
   `Head() == 3`.
2. Update one widget twice → `Since(3, 100)` returns **one** entry for it with the latest
   version; `Since(0, 100)` has 3 entries total (compaction).
3. Delete by a non-PK condition matching 2 rows → 2 `OpDelete` entries.
4. A write to an untracked model (declare a second tiny test model) → no entry, head unchanged.
5. `orm.DB.Tx` with 2 creates then return an error → no entries, head unchanged; the same with
   success → 2 entries.
6. 8 goroutines × 50 creates → `Since(0, 1000)` returns exactly 400 entries with versions 1..400,
   no gaps, ascending.
7. Restart: build a second `Log` over the same `mem` conn → `Head()` equals the first one's.
8. `New` errors: nil conn, no tracked models, duplicate model, auto-increment PK model.
9. `Since(0, 0)` → error.

## 5. README example

```go
raw, _ := postgres.Open(dsn)                   // any storage backend with BeginTx
_ = migrate.Migrate(raw, raw)                  // once, at deploy time
log, err := changelog.New(raw,
    func() model.Model { return &booking.Reservation{} },
    func() model.Model { return &patients.Patient{} },
)
db := orm.New(log)                             // modules get this db as usual
changes, _ := log.Since(cursor, 500)           // what changed since the client's cursor
```

## 6. Code rules (non-negotiable)
- `webtyp.com/fmt` for errors/formatting in library code; no `errors`, `strconv`, `strings`.
  `sync` is allowed.
- No string literal repeated in logic: table and column names (`"change_log"`, `"version"`,
  `"table_name"`, `"row_id"`) are unexported constants (or the generated ones).
- `any` appears only where the `storage` contract already uses it (`Exec`/`Query` args, `Values`).
- No exported symbol beyond §1. Tests in `tests/`; never export a symbol for a test.

## 7. Acceptance criteria
- `gotest ./...` green.
- `grep -rn "TODO\|FIXME" --include=*.go .` → empty.
- `go doc webtyp.com/changelog` lists exactly: `TxConn`, `NewModel`, `Op`, `OpUpsert`,
  `OpDelete`, `Log`, `New`, `Change`, `ChangeModel` (+ ormc-generated helpers of `Change`), and
  the `Log` methods of §1.
