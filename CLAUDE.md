# CLAUDE.md — lake-sqlite

Org-wide conventions are in [`oramasearch/orama-bridge`](https://github.com/oramasearch/orama-bridge)
`CLAUDE.md` and apply in full: no AI attribution, merge commits never squash, commit means push,
private by default, documentation in the same change.

- **What it is:** one Go binary (`cmd/lake-sqlite`) serving one SQLite database over HTTP
  (`internal/server`). The API contract is lake `docs/control-plane/services.md` (the `sqlite` row);
  what is built is [`docs/api.md`](docs/api.md). Change both together.
- **Driver:** `modernc.org/sqlite` (pure Go, BSD-3-Clause). Keep CGO off and the runtime
  distroless/static. Stay on a driver release that supports the Go version in `go.mod`.
- **Never log** SQL, parameters, bodies or the token.
- **Build and verify:** `go build ./... && go vet ./... && go test ./...`.
- **Release:** build `linux/amd64` on the cluster BuildKit (liquid-bridge `.claude/skills/liquid-dev`),
  push `oramasearch/lake-sqlite:<semver>` (an immutable tag), confirm the Docker Hub repository still
  answers 404 anonymously, then bump the operator's default image (`chart/values.yaml`
  `lakeServices.sqliteImage`, `cmd/main.go` `--lake-sqlite-image`).
