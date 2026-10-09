# Architecture

`changelog` operates as a decorator over the `storage.Conn` port (specifically `TxConn`). It intercepts compilation and execution of writes (`ActionCreate`, `ActionUpdate`, `ActionDelete`).

## How it works

When a tracked write is intercepted outside of a transaction:
1. It opens a transaction on the inner connection.
2. It executes a query to read the IDs of the rows about to be affected (if it's an update/delete). For creates, it extracts the ID from the query parameters.
3. It executes the inner write plan.
4. It computes the next version numbers based on the in-memory `head` atomic counter.
5. It deletes any existing changes for those rows, and inserts the new changes (`upsert` or `delete`) with the incremented version numbers into the `change_log` table.
6. It commits the transaction and updates the in-memory head.

If a transaction is already active (`orm.DB.Tx`), the `changelog` hooks into the `BeginTx` execution to start tracking inside a `trackedTx` wrapper.

## Why

| I want... | Use... |
|---|---|
| To record changes for a client sync | `changelog` |

### Design choices
- **Novice-name test**: `changelog.New` returns "a change log over this connection". `changelog.OpUpsert` means "the row exists now".
- **Complexity ledger**: +3 concepts (Log, Change, cursor), +1 file touched (composition root), +2 lines at call site.
- **Where it belongs**: A decorator over the storage port, so no domain module changes are necessary and any backend that supports transactions works.
