# CLAUDE.md — lake-sqlite

- **What it is:** one Go binary (`cmd/lake-sqlite`) serving one SQLite database over HTTP
  (`internal/server`). It is the web service behind lake's SQLite service kind. The API contract is
  lake's `docs/control-plane/services.md` (the `sqlite` row); what is built is
  [`docs/api.md`](docs/api.md). Change both together.
- **Driver:** `modernc.org/sqlite` (pure Go, BSD-3-Clause). Keep CGO off and the runtime
  distroless/static. Stay on a driver release that supports the Go version in `go.mod`.
- **Never log** SQL, parameters, bodies or the token.
- **Build and verify:** `go build ./... && go vet ./... && go test ./...`.
- **Images** are built for `linux/amd64` natively (not under emulation) and tagged with an
  immutable semantic version. A new image version is adopted by bumping the operator's default
  (`chart/values.yaml` `lakeServices.sqliteImage`, `cmd/main.go` `--lake-sqlite-image`).

## Rules

- **Branches:** branch from `dev`, and open every pull request against `dev`. `main` holds the last
  release. Pull requests are merged with merge commits, never squashed.
- **Documentation in the same change:** `README.md` and the pages under `docs/` change in the same
  commit as the code they describe.
- **No AI attribution** in commits, pull requests, tags or release notes: no `Co-Authored-By`
  trailers for an assistant, no "Generated with" lines.
- **Never write the word "seam"**; name the mechanism (interface, extension point, injected
  dependency, boundary).
- **No time or size estimates**; report what was done.
- Report vulnerabilities privately, as `SECURITY.md` describes, never in a public issue.
