# changelog
<img src="docs/img/badges.svg">

`changelog` is a storage.Conn decorator for `webtyp.com/storage` that records every write (create, update, delete) to tracked tables, and provides a `Since(cursor)` method to retrieve what changed.

It is designed for offline-first synchronization where clients ask the server "what changed since my cursor?".

## How to use

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

## Constraints

One process writes a given database through a `Log`. Versions are allocated in memory under a mutex; a second writer process would allocate the same versions, breaking the strictly increasing sequence requirement.

The `change_log` table must be created using the provided `migrate` package. If `ddl` cannot express a non-unique index, skip the index and say so in the README. Currently, index creation is skipped as it is not supported by `ddl`.
