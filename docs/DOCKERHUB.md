# barqr

**QR codes and barcodes over HTTP. One stateless container. No GUI, no database, no SaaS.**

*Gotenberg is to documents what barqr is to codes.*

`21.2 MB` · `distroless` · `no shell` · `no CGO` · `runs as 65532` · `linux/amd64` · `linux/arm64`

Maintained by **[Mohamed El Amin BOUCHAREB](https://github.com/el-amin-dev)** ·
Source: **<https://github.com/el-amin-dev/barqr>** · Apache-2.0

---

> **Status: M0–M9 complete**, `v1.0.0` not yet tagged. Every green push to `main`
> publishes `edge`; released tags carry an SBOM, build provenance, and a keyless
> cosign signature. M10 (a `full` build tag adding zint symbologies, and a dynamic-code
> module) is explicitly optional and not started.

---

## Quick start

```bash
docker run --rm -p 3000:3000 \
  -e BARQR_BIND=0.0.0.0 \
  -e BARQR_API_KEYS=dev-key \
  ghcr.io/el-amin-dev/barqr:edge

curl -s localhost:3000/v1/version
curl -H 'X-API-Key: dev-key' 'localhost:3000/v1/qr?data=https://barqr.dev' -o qr.png
```

`edge` is the last commit of `main` that passed CI; `sha-<short>` beside it is the
immutable handle to pin. `latest` and the semver tags appear with the first tagged
release.

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

## Documentation, in the image

```
http://localhost:3000/            landing page — public, no key
http://localhost:3000/v1/docs     interactive request builder
http://localhost:3000/v1/docs/swagger
http://localhost:3000/v1/docs/redoc
http://localhost:3000/v1/openapi.json
```

Swagger UI and ReDoc are bundled, not fetched from a CDN, so they work on a
host with no egress. Every page is generated from the live registries, so it
describes exactly what this image can do. The docs ask for your API key once.
`BARQR_DOCS=false` removes the whole surface.

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
| `BARQR_MAX_CANVAS_PX` | `25000000` | Render size cap. **Lower it if you set a memory limit** — see below. |
| `BARQR_ALLOW_REMOTE_FETCH` | `false` | Remote logo/background fetching (SSRF surface). |
| `BARQR_METRICS` | `true` | Prometheus endpoint. |
| `BARQR_LOG_LEVEL` | `info` | `debug` · `info` · `warn` · `error`. |
| `BARQR_DOCS` | `true` | The browser documentation at `/` and `/v1/docs`. |
| `BARQR_STRICT_SCANNABILITY` | `warn` | `off` · `warn` · `strict`; strict refuses an unreadable design. |

The `BARQR_MAX_CANVAS_PX` default is sized for a machine with memory to spare: 25 MP is
a ~100 MB pixel buffer for one request, and `BARQR_CONCURRENCY` of those in flight is
~800 MB. If you give the container a memory limit, set the cap alongside it —
`4000000` is 16 MB per render and 128 MB across the semaphore, which is what the
shipped manifests use. Also pair `BARQR_SHUTDOWN_GRACE` with the platform's kill
deadline: Compose's `stop_grace_period` defaults to 10s, shorter than the 15s drain, so
the default combination cuts live requests on every restart.

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
| `GET\|POST /v1/qr` · `/v1/barcode/{symbology}` | Render a code. 15 symbologies, 12 output formats. |
| `GET\|POST /v1/build/{type}` | Run a payload builder only — structured fields in, raw string out. |
| `POST /v1/validate` · `/v1/decode` | Scannability report without rendering; image in, data out. |
| `POST /v1/batch` · `/v1/sheet` | Many codes at once; a print-ready sheet of labels. |
| `GET /v1/preset` · `GET\|POST /v1/preset/{name}` | 8 layout presets and 30 themes. A theme sets only shapes and colours, so `?data=x&output.format=svg` works on any of them, and every theme is checked against the real scannability report before it ships. |
| `GET /v1/symbologies` · `/v1/openapi.json` | Capability matrix and OpenAPI 3.1, generated from the live registries. |
| `GET /v1/healthz` · `/v1/readyz` · `/v1/version` | Liveness, readiness, build identity. **The only unauthenticated routes** — `/metrics` needs a key. |
| `GET /metrics` | Prometheus exposition. |

Full reference:
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

Liveness must point at `/v1/healthz`, never `/v1/readyz`: `readyz` answers `503` the
moment `SIGTERM` arrives, so a liveness probe on it would declare every draining pod
dead and `SIGKILL` it mid-drain. Set `terminationGracePeriodSeconds` strictly above
`BARQR_SHUTDOWN_GRACE` for the same reason.

Complete manifests — Deployment, PodDisruptionBudget, Service, NetworkPolicy, and a
Compose file — are in
[`deploy/`](https://github.com/el-amin-dev/barqr/tree/main/deploy).

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
- **Ready-made manifests** — [`deploy/`](https://github.com/el-amin-dev/barqr/tree/main/deploy)

Apache-2.0 © 2026 Mohamed El Amin BOUCHAREB
