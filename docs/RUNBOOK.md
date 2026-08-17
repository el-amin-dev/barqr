# Runbook — barqr

> Commands only. Not rationale. Not architecture.
> Every command here MUST work as-typed on a fresh clone.
> If behavior changes → this file changes in the SAME PR.

## Setup

- Requirements: **Docker**. A local Go toolchain is optional.
- `make help` — list targets and show which toolchain will be used.
- No `.env` file, no database, no migrations. Configuration is environment variables
  only; see [`DEPLOY.md`](DEPLOY.md).

### Toolchain selection

`make` auto-detects Go. If `go` is not on `PATH`, the compiler and linter run in pinned
containers (`golang:1.26`, `golangci/golangci-lint:v2.12.2-alpine`) with caches under
`.cache/` owned by the invoking user.

- `make <target> TOOLCHAIN=local` — force the host Go toolchain.
- `make <target> TOOLCHAIN=docker` — force containers.

## Run

- `make run` — start the server on the defaults (`127.0.0.1:3000`, auth required).
- `make build && ./bin/barqr serve` — run the compiled binary.
- `./bin/barqr version` — print build identity.
- `./bin/barqr help` — usage.

Run the container:

```bash
make docker-build
docker run --rm -p 3000:3000 -e BARQR_BIND=0.0.0.0 -e BARQR_API_KEYS=dev-key barqr:dev
```

## Test

- `make test` — full suite, race detector, coverage profile to `coverage.out`.
- `make cover` — per-function coverage report.
- One package: `make test` then, for a narrower run,
  `go test ./internal/config/...` (add `TOOLCHAIN=docker` prefix rules if Go is not installed).
- One test: `go test -run TestLoadInsecureBindIsFatal ./internal/config/`.

## Database

- _(none — barqr is stateless by design and has no backing store)_

## Lint / Format

- `make lint` — golangci-lint (errcheck, govet, staticcheck, gosec, revive, and more;
  see `.golangci.yml`).
- `make vet` — `go vet ./...`.
- `make fmt` — format the source tree.
- `make tidy` — sync `go.mod` / `go.sum`. CI fails if this produces a diff.

## Smoke checks

`make smoke` runs all of the following against the built image:

| Check | Expected |
|---|---|
| `docker run -e BARQR_BIND=0.0.0.0 barqr:dev` (no keys) | exits non-zero — the security gate |
| `GET /v1/healthz` | `200` `{"status":"ok"}` |
| `GET /v1/readyz` | `200` `{"status":"ready"}` |
| `GET /v1/version` | `200`, body contains `"name":"barqr"` |
| `GET /v1/nope` | `404` |
| `docker inspect --format '{{.Config.User}}'` | `65532:65532` |

Manually, against a running container:

```bash
curl -s localhost:3000/v1/healthz
curl -s localhost:3000/v1/readyz
curl -s localhost:3000/v1/version
```

Validate a configuration without binding a port:

```bash
docker run --rm --env-file prod.env barqr:dev check-config   # exit 0 = good
./bin/barqr serve --print-config                             # same, secrets redacted
```

## Services / Ports

| Service | Port | Start command |
|---|---|---|
| barqr HTTP | `3000` (`BARQR_PORT`) | `make run` / `docker run … barqr:dev` |
| smoke container | `38080` (`SMOKE_PORT`) | `make smoke` |

## Gates

- `make ci` — build · vet · lint · test -race · docker build · smoke. This is exactly
  what the pull-request workflow runs.

## Troubleshooting

| Symptom | Cause | Fix |
|---|---|---|
| `configuration error, refusing to start` + `insecure configuration` | Non-loopback bind with `AUTH_MODE=required` and no keys, or `open` on a wildcard bind | Set `BARQR_API_KEYS`, or bind `127.0.0.1`, or set `BARQR_I_UNDERSTAND_OPEN_BIND=true` |
| `configuration error` + `invalid configuration value` | A `BARQR_*` value failed to parse | The message names the variable, what it got, and what was expected. All problems are listed at once. |
| `warning: unknown environment variable BARQR_…` | Typo in a variable name | Compare against [`DEPLOY.md`](DEPLOY.md); the value is being ignored |
| Container starts but nothing can reach it | Default bind is `127.0.0.1`, which inside a container means the container only | Set `BARQR_BIND=0.0.0.0` and publish/expose the port |
| `make lint` fails to pull the linter image | Registry timeout | `docker pull golangci/golangci-lint:v2.12.2-alpine` and retry |
| `-race requires cgo` | `TOOLCHAIN=docker` with an Alpine Go image | `make test` uses `golang:1.26` (Debian) for this reason; do not override `GO_IMAGE` to an Alpine tag |
| Root-owned files in `.cache/` | An older container run without `--user` | `make clean` |
