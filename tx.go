package changelog

import (
	"webtyp.com/storage"
)

type trackedTx struct {
	inner   storage.TxBoundExecutor
	log     *Log
	locked  bool
	txHead  int64
	done    bool
}

func (l *Log) BeginTx() (storage.TxBoundExecutor, error) {
	inner, err := l.conn.BeginTx()
	if err != nil {
		return nil, err
	}
	return &trackedTx{
		inner: inner,
		log:   l,
	}, nil
}

func (tx *trackedTx) Exec(query string, args ...any) error {
	if len(args) == 1 {
		if pw, ok := args[0].(*pendingWrite); ok {
			if !tx.locked {
				tx.log.mu.Lock()
				tx.locked = true
				tx.txHead = tx.log.head.Load()
			}
			return doTrackedWrite(tx.inner, tx.log.conn, pw, tx.log.tracked, &tx.txHead)
		}
	}
	return tx.inner.Exec(query, args...)
}

func (tx *trackedTx) QueryRow(query string, args ...any) storage.Scanner {
	return tx.inner.QueryRow(query, args...)
}

func (tx *trackedTx) Query(query string, args ...any) (storage.Rows, error) {
	return tx.inner.Query(query, args...)
}

func (tx *trackedTx) Commit() error {
	if tx.done {
		return nil
	}
	err := tx.inner.Commit()
	if err == nil && tx.locked {
		tx.log.head.Store(tx.txHead)
	}
	if tx.locked {
		tx.log.mu.Unlock()
		tx.locked = false
	}
	tx.done = true
	return err
}

func (tx *trackedTx) Rollback() error {
	if tx.done {
		return nil
	}
	err := tx.inner.Rollback()
	if tx.locked {
		tx.log.mu.Unlock()
		tx.locked = false
	}
	tx.done = true
	return err
}

func (tx *trackedTx) Close() error {
	return tx.inner.Close()
}
