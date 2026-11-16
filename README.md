# lake-sqlite

**One SQLite database behind a small HTTP API with a bearer token: the service behind lake's SQLite
services.**

Latest release: [v0.2.0](docs/releases/v0.2.0.md).

lake-sqlite is one static Go binary that serves one SQLite database file over HTTP. A workspace
creates a SQLite service in lake; the Orama operator runs one `lake-sqlite` pod for it, on its own
volume, and lake hands out the service's address and token. The driver is SQLite translated to pure
Go, so there is no C toolchain, no libc and nothing in the image but the binary.

Part of [Orama Lake](https://github.com/oramasearch/lake). MIT-licensed.

## What it does

- **Queries**: `POST /v1/query` answers `{columns, rows}`, at most 10,000 rows, with `truncated`
  set when cut. [Docs](docs/api.md)
- **Writes**: `POST /v1/exec` answers `{rows_affected, last_insert_id}`. [Docs](docs/api.md)
- **Batches**: `POST /v1/batch` runs several statements in one transaction and rolls all of them
  back on any error. [Docs](docs/api.md)
- **Health**: `GET /healthz` answers 200 when the database answers `SELECT 1`; no token needed.
  [Docs](docs/api.md)
- **Bounded**: 16 MiB request bodies, 30 s per request with the SQL interrupted at the deadline,
  the Go heap and SQLite's allocator sized from the container memory limit. [Docs](docs/api.md)
- **Durable commits**: WAL journal, `synchronous=FULL`, a checkpoint on shutdown; one copy only
  (see Known limits). [Docs](docs/api.md)

## Quickstart

You need Go (the version in [`go.mod`](go.mod)).

```sh
git clone https://github.com/oramasearch/lake-sqlite.git
cd lake-sqlite
make run    # serves ./local.db on :8080 with the throwaway token local-dev-token
```

In a second terminal:

```sh
curl -s -H 'Authorization: Bearer local-dev-token' -d '{"sql":"SELECT 1 AS one"}' localhost:8080/v1/query
```

You should see `{"columns":["one"],"rows":[[1]]}`.

Or run the published image:

```sh
docker run --rm -p 8080:8080 -e LAKE_SQLITE_TOKEN=local-dev-token -e LAKE_SQLITE_PATH=/tmp/main.db \
  oramasearch/lake-sqlite:0.2.0
```

### Build from source

```sh
go build ./... && go vet ./... && go test ./...
docker buildx build --platform linux/amd64 -t lake-sqlite:local .
```

The image runs `FROM scratch` as user `65532:65532`. It reads its token from `LAKE_SQLITE_TOKEN`
and keeps its database at `/data/main.db` unless `LAKE_SQLITE_PATH` says otherwise.

## How it fits the stack

lake declares the SQLite service kind and hands its address and token to the workspace. The Orama
operator runs one lake-sqlite StatefulSet per service, mounts its volume at `/data` and its token
from the service's Secret. Clients talk to it over HTTP with that token.

| Piece | What it does | Relation to this repo |
|---|---|---|
| [lake](https://github.com/oramasearch/lake) | The AI workspace | Declares the SQLite service kind and its API contract |
| [orama-operator](https://github.com/oramasearch/orama-operator) | Runs the stack on Kubernetes | Runs this image, one pod per SQLite service |

## Known limits

- **One copy.** One pod on one `ReadWriteOnce` volume, with no replication and no backup yet. A lost
  volume loses the database, and the service is down while its pod restarts or moves.
- A `sql` holding several statements separated by `;` runs all of them, and `/v1/query` answers
  only the last one's rows. Use `/v1/batch` for several statements.
- No quotas inside the database beyond the volume size; a full volume answers `507 disk_full`.

## Documentation

- [`docs/api.md`](docs/api.md): the HTTP API, configuration, limits, errors, build and local run.
- [`docs/releases/`](docs/releases/README.md): release notes.
- [`docs/README.md`](docs/README.md): the docs index.

## Community

- Bugs and requests: [issues](https://github.com/oramasearch/lake-sqlite/issues)
- Security reports: privately, per [SECURITY.md](SECURITY.md). Please don't open a public issue.

## Contributing

Contributions are welcome. Read [CONTRIBUTING.md](CONTRIBUTING.md) for how to build, test and open a
pull request (against `dev`). Every contributor signs the
[Contributor License Agreement](CLA.md) once, before their first pull request is merged.

Everyone taking part follows the [code of conduct](CODE_OF_CONDUCT.md).

## License

MIT. See [LICENSE](LICENSE). Third-party components keep their own licenses, listed in
[THIRD_PARTY_LICENSES](THIRD_PARTY_LICENSES).
