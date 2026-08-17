# barqr

**QR codes and barcodes over HTTP. One stateless container. No GUI, no database, no SaaS.**

*Gotenberg is to documents what barqr is to codes.*

`9.3 MB` · `distroless` · `no shell` · `no CGO` · `runs as 65532` · `linux/amd64` · `linux/arm64`

Maintained by **[Mohamed El Amin BOUCHAREB](https://github.com/el-amin-dev)** ·
Source: **<https://github.com/el-amin-dev/barqr>** · Apache-2.0

---

> **Status: milestone M0 of 10.** The service skeleton, configuration layer, security
> gate, and image are live and tested. Code rendering (`/v1/qr`) arrives in M1, and
> published tags begin at that point. Until then, build locally with `make docker-build`.

---

## Quick start

```bash
docker run --rm -p 3000:3000 \
  -e BARQR_BIND=0.0.0.0 \
  -e BARQR_API_KEYS=dev-key \
  barqr:dev

curl -s localhost:3000/v1/version
```

## ⚠️ Read this before you publish a port

barqr is built to sit on an **internal network** and answer requests from your own
code. It has no tenancy model and no abuse protection beyond rate limiting, because
that is not what it is for.

It enforces this rather than trusting you to remember it. The container **exits 1** on
boot when:

- it binds a non-loopback address with `BARQR_AUTH_MODE=required` and **no API keys**, or
- it binds a wildcard address with `BARQR_AUTH_MODE=open` and no explicit
  `BARQR_I_UNDERSTAND_OPEN_BIND=true`.

In Compose, prefer `expose:` over `ports:`. In Kubernetes, use a `ClusterIP` Service
with a `NetworkPolicy` — no `Ingress`.

## Configuration

Environment variables only; there are no config files. Invalid values are fatal rather
than silently defaulted, and an unrecognised `BARQR_*` variable warns at boot so a typo
cannot quietly leave you unauthenticated.

| Variable | Default | Purpose |
|---|---|---|
| `BARQR_BIND` | `127.0.0.1` | Listen address. Set `0.0.0.0` inside a container. |
| `BARQR_PORT` | `3000` | Listen port. |
| `BARQR_AUTH_MODE` | `required` | `required` or `open`. |
| `BARQR_API_KEYS` | — | Comma-separated. Hashed at boot; never logged. |
| `BARQR_MAX_BODY` | `2MB` | Request body cap. |
| `BARQR_REQUEST_TIMEOUT` | `10s` | Per-request deadline. |
| `BARQR_RATE_LIMIT` | `120/min` | Per-key allowance. |
| `BARQR_CONCURRENCY` | `8` | Worker semaphore; bursts queue rather than thrash. |
| `BARQR_SHUTDOWN_GRACE` | `15s` | Drain window on `SIGTERM`. |
| `BARQR_MAX_CANVAS_PX` | `25000000` | Render size cap. |
| `BARQR_ALLOW_REMOTE_FETCH` | `false` | Remote logo/background fetching (SSRF surface). |
| `BARQR_METRICS` | `true` | Prometheus endpoint. |
| `BARQR_LOG_LEVEL` | `info` | `debug` · `info` · `warn` · `error`. |

Full table: [`docs/DEPLOY.md`](https://github.com/el-amin-dev/barqr/blob/main/docs/DEPLOY.md).
Print the effective configuration, secrets redacted:

```bash
docker run --rm barqr:dev check-config
```

## Commands

The image entrypoint is `/barqr serve`. Override it to run the admin subcommands:

| Command | Purpose |
|---|---|
| `serve` | Run the HTTP server (default). |
| `serve --print-config` | Print the effective config, redacted, and exit. |
| `check-config` | Validate the environment and exit non-zero if it is wrong. |
| `version` | Print build identity. |

`check-config` is designed for an init container: catch a bad deployment before it
takes traffic.

## Endpoints

| Endpoint | Purpose |
|---|---|
| `GET /v1/healthz` | Liveness. |
| `GET /v1/readyz` | Readiness — `503` the instant shutdown begins, so rollouts drain. |
| `GET /v1/version` | Build identity. |

Rendering endpoints (`/v1/qr`, `/v1/barcode/{symbology}`, `/v1/decode`, `/v1/batch`)
land across milestones M1–M8. See
[`docs/API.md`](https://github.com/el-amin-dev/barqr/blob/main/docs/API.md).

## Kubernetes probes

The image is distroless and has no shell, so `HEALTHCHECK` cannot run inside it. Use
HTTP probes:

```yaml
livenessProbe:
  httpGet: { path: /v1/healthz, port: 3000 }
readinessProbe:
  httpGet: { path: /v1/readyz, port: 3000 }
```

## Hardened run

```bash
docker run --rm -p 127.0.0.1:3000:3000 \
  --read-only --cap-drop ALL --security-opt no-new-privileges \
  -e BARQR_BIND=0.0.0.0 -e BARQR_API_KEYS="$BARQR_KEY" \
  barqr:dev
```

The image already ships with no shell, no package manager, and `USER 65532:65532`.
barqr is stateless and writes nothing to disk, so a read-only root filesystem needs no
`tmpfs` mount.

## Links

- **Source & issues** — <https://github.com/el-amin-dev/barqr>
- **API reference** — [`docs/API.md`](https://github.com/el-amin-dev/barqr/blob/main/docs/API.md)
- **Security model** — [`docs/SECURITY.md`](https://github.com/el-amin-dev/barqr/blob/main/docs/SECURITY.md)
- **Deployment** — [`docs/DEPLOY.md`](https://github.com/el-amin-dev/barqr/blob/main/docs/DEPLOY.md)

Apache-2.0 © 2026 Mohamed El Amin BOUCHAREB
