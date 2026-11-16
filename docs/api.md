# lake-sqlite

`lake-sqlite` is the small web service behind lake's **`sqlite` service kind**: one SQLite database
behind an HTTP API with a bearer token. SQLite has no upstream Kubernetes operator, so the Orama
operator runs it itself, one StatefulSet per service (orama-operator [`docs/lake/services.md`](https://github.com/oramasearch/orama-operator/blob/main/docs/lake/services.md)). The contract is the `sqlite` row of lake's
[`docs/control-plane/services.md`](https://github.com/oramasearch/lake/blob/17b4ad57f2571c30516f7ecb85c03a4165e32e59/docs/control-plane/services.md)
and the paragraph after its table; this page is the whole API as built.

| | |
|---|---|
| Source | this repository: [`cmd/lake-sqlite/main.go`](../cmd/lake-sqlite/main.go) and the HTTP API in [`internal/server/`](../internal/server/) |
| Image | `oramasearch/lake-sqlite:<tag>` (Docker Hub), built from [`Dockerfile`](../Dockerfile) for `linux/amd64` |
| Driver | [`modernc.org/sqlite`](https://gitlab.com/cznic/sqlite) v1.38.2 — SQLite translated to pure Go, no cgo; **BSD-3-Clause** (SQLite itself is public domain) |
| Runtime | `scratch`: one static binary, `/etc/passwd` and `/etc/group` naming user `65532:65532`, an empty `/tmp` (mode `1777`) for SQLite temporary files. No shell, no libc, no CA bundle (no outbound TLS), no distribution packages |

## Configuration

| Variable | Default | Meaning |
|---|---|---|
| `LAKE_SQLITE_TOKEN` | — (required) | the bearer token; the operator mounts it from Secret `<name>-credentials`, key `token` |
| `LAKE_SQLITE_PATH` | `/data/main.db` | the database file; its directory must exist and be writable |
| `LAKE_SQLITE_ADDR` | `:8080` | the listen address |
| `LAKE_SQLITE_MEMORY_LIMIT` | unset | the container memory limit in bytes (the operator passes `limits.memory`); the Go heap is held to three quarters of it (`debug.SetMemoryLimit`), leaving the rest to SQLite's allocator, which is bounded by the page cache, the 8 MiB value limit and the 32 MiB response budget (this SQLite build keeps no memory statistics, so heap-limit pragmas would not be enforced) |

The database is opened with `journal_mode=WAL`, `busy_timeout=5000` and `foreign_keys=ON` on every
connection, and a batch's transaction begins `IMMEDIATE` (it takes the write lock at `BEGIN`, so two
batches never deadlock upgrading a read lock). `synchronous` stays SQLite's default, `FULL`: every commit is
on disk before it is answered. The pool holds up to four connections: WAL lets readers run beside the
one writer, and writers wait up to five seconds for the lock. At startup the service opens the
database and runs `SELECT 1`, and exits non-zero if either fails or the token is empty.

## What a statement may do

The customer holds the token and owns the database, so any SQL runs, inside these bounds. Every
connection carries an SQLite authorizer and limits, set before it serves a request
([`internal/server/guard.go`](../internal/server/guard.go)):

- **One file.** `ATTACH` and `DETACH` are refused, and no database can be attached
  (`SQLITE_LIMIT_ATTACHED` is 0). That also refuses `VACUUM` and `VACUUM INTO`, which attach their
  target, so no statement writes a file other than the database and its WAL.
- **Transactions are the server's.** `BEGIN`, `COMMIT`, `END`, `ROLLBACK`, `SAVEPOINT` and `RELEASE`
  are refused in every route: `/v1/exec` and `/v1/query` commit each statement on its own, and
  `/v1/batch` is one transaction the server begins and commits. A connection is checked after every
  request, and a transaction it was left in is rolled back (the connection is discarded if that
  fails), so no request starts on another's state.
- **Pragmas.** Pragmas that read the schema or the file are allowed with any argument
  (`table_info`, `table_list`, `index_list`, `index_info`, `foreign_key_list`, `foreign_key_check`,
  `integrity_check`, `quick_check`, `database_list`, `page_count`, `freelist_count`, `optimize`,
  `wal_checkpoint`, `incremental_vacuum`, and more), as are `user_version`, `application_id` and
  `defer_foreign_keys`, which live in the file or the transaction. The connection's settings
  (`foreign_keys`, `journal_mode`, `synchronous`, `busy_timeout`, `cache_size`, `mmap_size`,
  `query_only`, the heap limits, …) can be read but not set. Every other pragma, `writable_schema`
  and `temp_store_directory` among them, is refused.
- `load_extension` is refused (extension loading is also off in the driver).
- **Value size.** No string, blob or row is over **8 MiB** (`SQLITE_LIMIT_LENGTH`); a statement that
  would make one fails with SQLite's `string or blob too big`.

A refused statement answers `403 not_allowed`. The allowlists are `pragmaAnyArgument` and
`pragmaReadOnly` in `guard.go`.

The driver exposes neither an authorizer nor the connection's `sqlite3` handle, so the guard reads
the handle from the driver's connection by reflection and installs the authorizer through the
driver's C bindings. It checks the layout it expects, and a driver upgrade that changes it makes
every connection, and so startup, fail rather than run unguarded; `guard_test.go` covers each rule.

## API

Every route but `GET /healthz` needs **`Authorization: Bearer <token>`**. The presented and the
configured token are both hashed with SHA-256 and compared in constant time; a missing, malformed
or wrong token is `401` (with `WWW-Authenticate: Bearer`), before the route or method is looked at.

A statement is `{"sql": "...", "params": [...]}`. `params` is positional (`?` or `?NNN`) and may be
omitted. Each parameter binds as:

| JSON | Binds as |
|---|---|
| integer literal (`42`, `-7`) | `INTEGER` (64-bit; a literal that does not fit is refused) |
| number with a fraction or exponent (`2.5`, `1e3`) | `REAL` |
| string | `TEXT` |
| `true` / `false` | `INTEGER` 1 / 0 (SQLite has no boolean) |
| `null` | `NULL` |
| `{"$base64": "<standard base64>"}` | `BLOB` |

Any other array or object is refused with `400 invalid_params`. Request bodies are strict JSON:
unknown fields and trailing data are refused.

### `POST /v1/query`

`{"sql", "params"}` answers `200`:

```json
{"columns": ["id", "name", "score", "avatar", "note"],
 "rows": [[1, "ada", 9.5, {"$base64": "iVBORw0K..."}, null]],
 "truncated": true}
```

Values come back as real JSON types: `INTEGER` as a JSON integer (exact to 64 bits; a JavaScript
client loses precision above 2^53), `REAL` as a JSON number that always has a fraction or exponent
(`1.0`, never `1`) so it is told apart from an integer, `TEXT` as a string, `BLOB` as
`{"$base64": "..."}`, `NULL` as `null`. SQLite can store an infinite `REAL`, which JSON cannot hold:
it is the string `"Infinity"` or `"-Infinity"`. At most **10,000 rows** are returned; when there
were more, the answer stops there and carries `"truncated": true` (the key is absent otherwise). The
values of one answer are also capped at **32 MiB** (a blob counted as its base64 length, a text as
its length): the answer stops at the row that would pass it, with `"truncated": true`, and a first
row alone over it is `413 response_too_large`. A statement that returns no rows still answers
`columns` and an empty `rows`.

Text stored in a column **declared** `DATE`, `DATETIME` or `TIMESTAMP` is read by the driver as a
time when it parses as one, and is returned as text again in SQLite's own format (`2026-09-26`, or
`2026-09-26 10:11:12[.fraction][±hh:mm]`). A value stored as `2026-09-26T10:11:12Z` therefore reads
back as `2026-09-26 10:11:12`. Declare such columns `TEXT` (or store Unix times as `INTEGER`) to have
every byte returned as stored.

### `POST /v1/exec`

`{"sql", "params"}` answers `200` `{"rows_affected": 2, "last_insert_id": 3}`. They are SQLite's
`changes()` and `last_insert_rowid()` for the connection that ran the statement; after a statement
that is not an `INSERT`, `UPDATE` or `DELETE`, SQLite leaves them at their previous values.

### `POST /v1/batch`

`{"statements": [{"sql", "params"}, ...]}` runs every statement **in order, in one transaction**,
and answers `200` `{"results": [...]}`, one result per statement: a statement that returns columns
(a `SELECT`, or an `INSERT ... RETURNING`) answers like `/v1/query` (with the same 10,000-row cap
per statement, and one 32 MiB budget shared by the whole batch), any other like `/v1/exec`. If any
statement fails, the whole transaction is rolled back and the answer is that statement's error, its
message prefixed with `statements[<i>]: `. Parameters are checked before the transaction begins, so
a bad parameter runs nothing. An empty `statements`, or more than **1,000**, is
`400 invalid_request`. A statement cannot end the transaction early: transaction control is refused
(see [What a statement may do](#what-a-statement-may-do)).

### `GET /healthz`

No token. `200` `{"status": "ok"}` only when the database opens and `SELECT 1` answers within two
seconds; otherwise `503` `{"error": {"code": "unavailable", ...}}`. `HEAD` is accepted too. The
operator's startup, readiness and liveness probes all call it, so a service is Ready exactly while
its database answers.

### Errors

Every error is `{"error": {"code": "...", "message": "..."}}`:

| Status | `code` | When |
|---|---|---|
| 400 | `sql_error` | SQLite refused the statement (syntax, missing table, constraint or foreign-key violation, …); the message is SQLite's |
| 400 | `invalid_request` | the body is not the expected JSON, `sql` is empty, or `statements` is empty or over 1,000 |
| 400 | `invalid_params` | a parameter cannot be bound (see the table above) |
| 401 | `unauthorized` | no or a wrong bearer token |
| 404 | `not_found` | an unknown route (with a valid token) |
| 403 | `not_allowed` | the statement is refused by the guard (see [What a statement may do](#what-a-statement-may-do)) |
| 405 | `method_not_allowed` | a route called with the wrong method |
| 413 | `body_too_large` | a body over **16 MiB** |
| 413 | `response_too_large` | one row is over what is left of the 32 MiB response budget |
| 503 | `busy` | the database stayed locked past the 5-second busy timeout |
| 503 | `unavailable` | `/healthz`: the database does not answer |
| 504 | `timeout` | the request ran past its **30-second** limit; the running statement is interrupted |
| 507 | `disk_full` | the volume is full |

### Limits and lifecycle

- Every request is bounded to 30 s, the SQL included (the statement is interrupted when the time
  runs out). The server reads headers within 10 s, and closes idle connections after 2 minutes.
- On `SIGTERM` (or `SIGINT`) the server stops accepting connections, lets in-flight requests finish
  for up to 30 s, then closes the database, which checkpoints the WAL into `main.db`. The pod's
  `terminationGracePeriodSeconds` is 45.
- One JSON line per request on stdout: `method`, `path`, `status`, `bytes`, `request_bytes`,
  `duration_ms`. The SQL, its parameters, the body, headers and the token are **never** logged.

## Build

The image is built from the repository root, for `linux/amd64`:

```sh
docker buildx build --platform linux/amd64 \
  -t oramasearch/lake-sqlite:<tag> .
# or: make docker-build IMG=oramasearch/lake-sqlite:<tag>
```

The builder is the same digest-pinned `golang:1.23` as the operator's own image; the build runs
with `CGO_ENABLED=0`, `-trimpath` and a stripped binary. The chart's default is
`lakeServices.sqliteImage: oramasearch/lake-sqlite:0.1.0`, so that tag must be published before a SQLite service can start; a Lake overrides it with
`spec.services.sqliteImage`. Changing the image rolls every SQLite service's pod once.

## Run locally

```sh
go test ./...
mkdir -p /tmp/lake-sqlite
LAKE_SQLITE_TOKEN=dev LAKE_SQLITE_PATH=/tmp/lake-sqlite/main.db LAKE_SQLITE_ADDR=127.0.0.1:8080 \
  go run ./cmd/lake-sqlite

curl -s localhost:8080/healthz
curl -s -H 'Authorization: Bearer dev' -d '{"sql":"CREATE TABLE t (id INTEGER PRIMARY KEY, v TEXT)"}' localhost:8080/v1/exec
curl -s -H 'Authorization: Bearer dev' \
  -d '{"statements":[{"sql":"INSERT INTO t (v) VALUES (?)","params":["a"]},{"sql":"SELECT * FROM t"}]}' \
  localhost:8080/v1/batch
```

The tests (`internal/server/server_test.go`, table-driven over an `httptest` server
and a temporary directory) cover the token (missing, wrong, prefix, suffix, other schemes, and
`/healthz` without one), every value type in both directions, request errors and their codes, the
16 MiB cap, exec results, WAL and foreign keys, batch commit, rollback on a SQL error and on a bad
parameter, the 10,000-row cap on `/v1/query` and in a batch, the request timeout, `/healthz` on an
open and a closed database, refusing to start without a token or a writable path, and that the
request log never carries the token, the SQL or a parameter. `internal/server/guard_test.go` covers
the guard: no file written outside the database (ATTACH in both forms, VACUUM, VACUUM INTO,
`temp_store_directory`), the pragma allowlists, transaction control refused in a batch (also hidden
in a multi-statement `sql`) and in `/v1/exec`, no state carried between requests and no acknowledged
write lost across a restart, the 8 MiB value limit, the shared response budget and the batch cap.

## Known limits

- **One copy.** One pod on one `ReadWriteOnce` volume, no replication and no backup yet: a lost
  volume loses the database, and the service is down while its pod restarts or moves.
- A `sql` holding several statements separated by `;` runs all of them, each committing on its
  own, and `/v1/query` answers only the last one's rows. Send one statement per `sql`, and use
  `/v1/batch` for several that must commit together.
- No per-customer quotas inside the database beyond the volume size; a full volume answers
  `507 disk_full`.
- `VACUUM` is refused (it attaches a temporary database); `PRAGMA incremental_vacuum` works on a
  database created with `PRAGMA auto_vacuum = INCREMENTAL` set before its first table.
