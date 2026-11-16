# lake-sqlite

The web service behind lake's **SQLite service kind**: one SQLite database behind a small HTTP API
with a bearer token. A customer creates a SQLite service on lake's Services page; the Orama operator
runs one `lake-sqlite` pod per service with its own volume, and lake hands the customer its address
and token.

```sh
make test            # go test ./...
make run             # serves ./local.db on :8080 with token local-dev-token
curl -s -H 'Authorization: Bearer local-dev-token' -d '{"sql":"SELECT 1 AS one"}' localhost:8080/v1/query
```

| Route | Body | Answer |
|---|---|---|
| `POST /v1/query` | `{sql, params}` | `{columns, rows}` (at most 10,000 rows; `truncated` when cut) |
| `POST /v1/exec` | `{sql, params}` | `{rows_affected, last_insert_id}` |
| `POST /v1/batch` | `{statements: [{sql, params}]}` | `{results: [...]}`, all in one transaction |
| `GET /healthz` | — | 200 when the database answers `SELECT 1` (no token) |

The whole API, configuration, limits and errors: [`docs/api.md`](docs/api.md).

The image is `oramasearch/lake-sqlite` on Docker Hub.
The operator side (StatefulSet, Service, credential) is orama-operator
`internal/controller/lake_service_sqlite.go`.

## License

MIT, see [LICENSE](LICENSE). Third-party components and their licenses are in
[THIRD_PARTY_LICENSES](THIRD_PARTY_LICENSES). Contributions are welcome under the
[Contributor License Agreement](CLA.md); see [CONTRIBUTING.md](CONTRIBUTING.md).
