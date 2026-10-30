---
PLAN: "test: create test tables with ddl.Sync — CreateTable is leaving ddl's public API"
EXECUTOR: jules
REVIEWER: none
---

> Read AGENTS.md at the repo root before any change.
> **Finish by yourself:** when the delivery check passes, commit, push and open the pull request. Never ask whether to
> proceed or finalize — nobody answers, and a session that waits ends without your work.

# Test tables with `ddl.Sync`

## Context

`ddl.DB.CreateTable` compiles to `CREATE TABLE IF NOT EXISTS`: against an existing table it does nothing, so a model's
new field never reached an installed database. Every library and module now migrates with `ddl.DB.Sync`, which creates
a missing table **and** adds missing columns; the next `ddl` release stops exporting `CreateTable` so nobody can go
back. This repo still calls it, only in tests: `tests/sqlite_test.go`. Those calls must become `Sync` first, or this repo stops
compiling when it takes the new `ddl`. For a table that does not exist yet — the case in every one of these tests —
`Sync` does exactly what `CreateTable` did. Flow: https://github.com/webtyp/ddl/blob/main/docs/diagrams/SYNC_FLOW.md

## Tools

```bash
go install webtyp.com/devflow/cmd/gotest@latest
```

Environment: `GOPRIVATE=webtyp.com/*,github.com/veltylabs/*`, `GOFLAGS=-mod=mod`.

## APIs used

| Symbol | Exact signature | Tag |
|---|---|---|
| `(*ddl.DB).Sync` | `func (d *DB) Sync(models ...model.Model) error` | `webtyp.com/ddl` v0.0.36 (already required) |
| `(*ddl.DB).CreateTable` | `func (d *DB) CreateTable(m model.Model) error` — the call being replaced | v0.0.36 |

## Stage 1 — replace the calls

In `tests/sqlite_test.go`, replace every `.CreateTable(<model>)` with `.Sync(<model>)` on the same receiver, with the same argument
and the same error handling. Nothing else changes: not the test names, not their assertions. If a test's **name or
message** says "CreateTable" (for example a failure text `"CreateTable failed"`), change that word to `Sync`.
`grep -rn "CreateTable(" --include=*.go .` → empty afterwards.

Run `gotest`: everything that passed before still passes. Commit after this stage.

## Delivery check

1. `gotest` passes. A PR that does not compile, or whose tests could not run, is a failed delivery.
2. `grep -rn "CreateTable(" --include=*.go .` → empty.
3. `git diff --stat main` lists only `tests/sqlite_test.go`.

## Delivery rules

- Commit after the stage. Never run `git reset`, `git restore`, `git checkout -- <file>` or `git stash` on your own work.
- Stuck for 30 minutes: open the PR anyway, listing what fails.
- Do not change non-test code or any other repository.

## Stages

| # | Files | Result |
|---|---|---|
| 1 | `tests/sqlite_test.go` | `Sync` instead of `CreateTable`; green |
