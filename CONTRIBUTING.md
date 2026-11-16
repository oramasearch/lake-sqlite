# Contributing to lake-sqlite

lake-sqlite is one Go binary (`cmd/lake-sqlite`) serving one SQLite database over HTTP
(`internal/server`). Its API contract is the `sqlite` row of lake's
`docs/control-plane/services.md`; what is built is [`docs/api.md`](docs/api.md). Change both
together.

## Build and test

```sh
go build ./... && go vet ./... && go test ./...
```

## Rules for changes

- **Driver:** `modernc.org/sqlite` (pure Go, BSD-3-Clause). Keep CGO off and the runtime
  distroless/static. Stay on a driver release that supports the Go version in `go.mod`.
- **Never log** SQL, parameters, bodies or the token.
- Documentation changes in the same pull request as the code.
- Branch from `dev` and open the pull request against `dev`; `main` holds the last release.
- Images are built for `linux/amd64` and pushed with an immutable `<semver>` tag; the operator's
  default image (`lakeServices.sqliteImage` in its chart) moves to the new tag after the push.

## Security

Report a vulnerability privately, as [`SECURITY.md`](SECURITY.md) describes, never in a public issue.
