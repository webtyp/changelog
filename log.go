package changelog

import (
	"sync"
	"sync/atomic"

	"webtyp.com/fmt"
	"webtyp.com/model"
	"webtyp.com/storage"
)

type TxConn interface {
	storage.Conn
	BeginTx() (storage.TxBoundExecutor, error)
}

type NewModel func() model.Model

type Op string

const (
	OpUpsert Op = "upsert" // the row exists (created or updated)
	OpDelete Op = "delete" // the row no longer exists
)

type Log struct {
	conn    TxConn
	tracked map[string]NewModel
	schemas map[string][]model.Field
	pkIdx   map[string]int
	head    atomic.Int64
	mu      sync.Mutex // serialises all tracked writes of this process
}

func New(conn TxConn, tracked ...NewModel) (*Log, error) {
	if conn == nil {
		return nil, errConnRequired
	}
	if len(tracked) == 0 {
		return nil, errAtLeastOneModel
	}

	l := &Log{
		conn:    conn,
		tracked: make(map[string]NewModel),
		schemas: make(map[string][]model.Field),
		pkIdx:   make(map[string]int),
	}

	for _, fn := range tracked {
		if fn == nil {
			return nil, errFactoryReturnedNil
		}
		m := fn()
		if m == nil {
			return nil, errFactoryReturnedNil
		}
		name := m.ModelName()
		if _, ok := l.tracked[name]; ok {
			return nil, logError(fmt.Sprintf(string(errTrackedTwice), name))
		}
		schema := m.Schema()
		var pkIdx = -1
		for i, f := range schema {
			if f.DB != nil && f.DB.PK {
				if f.DB.AutoInc {
					return nil, logError(fmt.Sprintf(string(errNeedsCallerMintedPK), name))
				}
				pkIdx = i
				break
			}
		}
		if pkIdx == -1 {
			return nil, logError(fmt.Sprintf(string(errNeedsCallerMintedPK), name))
		}
		l.tracked[name] = fn
		l.schemas[name] = schema
		l.pkIdx[name] = pkIdx
	}

	// Read current head
	plan, err := conn.Compile(storage.Query{
		Action:  storage.ActionReadAll,
		Table:   ChangeModel.Name,
		OrderBy: []storage.Order{storage.Desc(Change_.Version)},
		Limit:   1,
	}, &Change{})
	if err != nil {
		return nil, logError(fmt.Sprintf(string(errChangeLogMissing), err))
	}
	rows, err := conn.Query(plan.Query, plan.Args...)
	if err != nil {
		return nil, logError(fmt.Sprintf(string(errChangeLogMissing), err))
	}
	defer rows.Close()

	if rows.Next() {
		var c Change
		if err := rows.Scan(c.Pointers()...); err != nil {
			return nil, err
		}
		l.head.Store(c.Version)
	} // else empty -> head 0

	return l, nil
}

func (l *Log) Since(cursor int64, limit int) ([]Change, error) {
	if limit <= 0 {
		return nil, errLimitPositive
	}

	plan, err := l.conn.Compile(storage.Query{
		Action: storage.ActionReadAll,
		Table:  ChangeModel.Name,
		Conditions: []storage.Condition{
			storage.Gt(Change_.Version, cursor),
			storage.Lte(Change_.Version, l.Head()),
		},
		OrderBy: []storage.Order{storage.Asc(Change_.Version)},
		Limit:   limit,
	}, &Change{})
	if err != nil {
		return nil, err
	}

	rows, err := l.conn.Query(plan.Query, plan.Args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var changes []Change
	for rows.Next() {
		var c Change
		if err := rows.Scan(c.Pointers()...); err != nil {
			return nil, err
		}
		changes = append(changes, c)
	}
	return changes, rows.Err()
}

func (l *Log) Head() int64 {
	return l.head.Load()
}

type pendingWrite struct {
	query storage.Query
	model model.Model
	plan  storage.Plan
}

func (l *Log) Compile(q storage.Query, m model.Model) (storage.Plan, error) {
	inner, err := l.conn.Compile(q, m)
	if err != nil {
		return inner, err
	}
	if (q.Action == storage.ActionCreate || q.Action == storage.ActionUpdate || q.Action == storage.ActionDelete) && m != nil {
		if _, ok := l.tracked[m.ModelName()]; ok {
			return storage.Plan{
				Mode:  inner.Mode,
				Query: inner.Query,
				Args:  []any{&pendingWrite{query: q, model: m, plan: inner}},
			}, nil
		}
	}
	return inner, nil
}

func (l *Log) Exec(query string, args ...any) error {
	if len(args) == 1 {
		if pw, ok := args[0].(*pendingWrite); ok {
			return l.execTracked(pw)
		}
	}
	return l.conn.Exec(query, args...)
}

func (l *Log) execTracked(pw *pendingWrite) error {
	l.mu.Lock()
	defer l.mu.Unlock()

	tx, err := l.conn.BeginTx()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	head := l.head.Load()
	err = doTrackedWrite(tx, pw, l.tracked, l.schemas, l.pkIdx, &head)
	if err != nil {
		return err
	}

	if err := tx.Commit(); err != nil {
		return err
	}
	l.head.Store(head)
	return nil
}

func doTrackedWrite(tx storage.TxBoundExecutor, pw *pendingWrite, tracked map[string]NewModel, schemas map[string][]model.Field, pkIdx map[string]int, head *int64) error {
	var ids []string
	name := pw.model.ModelName()

	if pw.query.Action == storage.ActionCreate {
		idx := pkIdx[name]
		if idx >= len(pw.query.Values) {
			return logError(fmt.Sprintf(string(errCreateNoPKValue), pw.query.Table))
		}
		val := pw.query.Values[idx]
		if val == nil {
			return logError(fmt.Sprintf(string(errCreateNoPKValue), pw.query.Table))
		}
		ids = append(ids, fmt.Convert(val).String())
	} else {
		factory := tracked[name]
		fresh := factory()

		var columns []string
		schema := schemas[name]
		idx := pkIdx[name]
		for _, f := range schema {
			if f.DB != nil && !f.Exclude {
				columns = append(columns, f.Name)
			}
		}

		plan, err := tx.(storage.Compiler).Compile(storage.Query{
			Action:     storage.ActionReadAll,
			Table:      pw.query.Table,
			Columns:    columns,
			Conditions: pw.query.Conditions,
		}, fresh)
		if err != nil {
			return err
		}
		rows, err := tx.Query(plan.Query, plan.Args...)
		if err != nil {
			return err
		}
		defer rows.Close()

		for rows.Next() {
			ptrs := fresh.Pointers()
			if err := rows.Scan(ptrs...); err != nil {
				return err
			}
			vals := model.ReadValues(schema, ptrs)
			ids = append(ids, fmt.Convert(vals[idx]).String())
		}
		if err := rows.Err(); err != nil {
			return err
		}
	}

	// Exec original write
	if err := tx.Exec(pw.plan.Query, pw.plan.Args...); err != nil {
		return err
	}

	// For create, we collected ID before Exec.
	// For update/delete, we collected ID before Exec, which matches the condition.

	// Record changes
	op := OpUpsert
	if pw.query.Action == storage.ActionDelete {
		op = OpDelete
	}
	for _, id := range ids {
		*head = *head + 1

		// delete existing change log rows
		delPlan, err := tx.(storage.Compiler).Compile(storage.Query{
			Action: storage.ActionDelete,
			Table: ChangeModel.Name,
			Conditions: []storage.Condition{
				storage.Eq(Change_.TableName, pw.query.Table),
				storage.Eq(Change_.RowId, id),
			},
		}, &Change{})
		if err != nil {
			return err
		}
		if err := tx.Exec(delPlan.Query, delPlan.Args...); err != nil {
			return err
		}

		// insert new change log row
		c := &Change{
			Version: *head,
			TableName: pw.query.Table,
			RowId: id,
			Op: string(op),
		}
		insPlan, err := tx.(storage.Compiler).Compile(storage.Query{
			Action: storage.ActionCreate,
			Table: ChangeModel.Name,
			Columns: []string{Change_.Version, Change_.TableName, Change_.RowId, Change_.Op},
			Values: []any{c.Version, c.TableName, c.RowId, c.Op},
		}, c)
		if err != nil {
			return err
		}
		if err := tx.Exec(insPlan.Query, insPlan.Args...); err != nil {
			return err
		}
	}
	return nil
}

func (l *Log) QueryRow(query string, args ...any) storage.Scanner {
	return l.conn.QueryRow(query, args...)
}

func (l *Log) Query(query string, args ...any) (storage.Rows, error) {
	return l.conn.Query(query, args...)
}

func (l *Log) Close() error {
	return l.conn.Close()
}
