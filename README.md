<div align="center">

# barqr

**QR codes and barcodes over HTTP. One stateless container. No GUI, no database, no SaaS.**

*Gotenberg is to documents what barqr is to codes.*

[![CI](https://github.com/el-amin-dev/barqr/actions/workflows/ci.yml/badge.svg)](https://github.com/el-amin-dev/barqr/actions/workflows/ci.yml)
[![CodeQL](https://github.com/el-amin-dev/barqr/actions/workflows/codeql.yml/badge.svg)](https://github.com/el-amin-dev/barqr/actions/workflows/codeql.yml)
[![Go Report Card](https://goreportcard.com/badge/github.com/el-amin-dev/barqr)](https://goreportcard.com/report/github.com/el-amin-dev/barqr)
[![License](https://img.shields.io/badge/license-Apache--2.0-blue.svg)](LICENSE)
[![Image](https://img.shields.io/badge/image-9.3%20MB-brightgreen)](Dockerfile)

</div>

---

> **Project status — milestone M0 of 10 shipped.**
> The service skeleton, configuration layer, security gate, container image, and CI
> pipeline are live and tested. Code *rendering* (`/v1/qr`) lands in M1.
> See the [roadmap](#roadmap) for exactly what exists today.

---

## Quickstart

```bash
make docker-build                                                    # 9.3 MB image
docker run --rm -p 3000:3000 -e BARQR_BIND=0.0.0.0 -e BARQR_API_KEYS=dev-key barqr:dev
curl -s localhost:3000/v1/version | jq
```

```json
{
  "version": "dev",
  "commit": "a530461",
  "date": "2026-08-17T10:55:21Z",
  "go": "go1.26.6",
  "platform": "linux/amd64",
  "name": "barqr"
}
```

No Go toolchain installed? `make` runs the compiler, linter, and tests in containers
automatically. A fresh clone needs nothing but Docker.

## Why barqr

Every project eventually needs a QR code, and every project solves it differently:
a JavaScript library in the frontend, a Python script in a cron job, a paid API with a
rate limit and a privacy policy. barqr replaces all three with one endpoint.

|  | |
|---|---|
| **Ultra fast** | Pure Go, no CGO, no subprocess, no disk I/O. Boot in under 100 ms. |
| **Stateless** | No database, no session, no local writes. Any replica answers any request. |
| **Scales flat** | Add replicas. Concurrency is bounded per process by a semaphore, so a burst queues instead of thrashing. |
| **Drop-in microservice** | One container, one port, env-only config. Fits a compose file, a Nomad job, or a k8s Deployment without adapters. |
| **curl-first** | Every option is reachable as a query param *or* a JSON body field. Paste a URL into an `<img src>` and you are done. |
| **Deny by default** | Binds loopback, demands an API key, refuses to boot if that combination would expose it. |
| **Small** | 9.3 MB distroless image, no shell, no package manager, unprivileged user, read-only rootfs. |

Non-goals, on purpose: no web UI, no accounts, no billing, no image editing, no
"AI" anything. It renders codes.

## "Why not just use a library?"

Fair question. Generating a QR code *is* three lines in every language. The three
lines are not the work.

**The work is everything around them.** Input validation and the error taxonomy that
tells a caller *why* their 11-digit string is not a valid EAN-13. Size caps so a
`scale=9999` request cannot allocate a gigabyte. Decompression-bomb guards on decode.
SSRF defence when someone wants their logo fetched from a URL. Content negotiation,
ETags, caching. Scannability analysis, so a designer's low-contrast gradient fails
review instead of failing in the wild on a printed poster.

**And the long tail is where the estimate breaks.** EAN-13 check digits. ITF-14 bearer
bars. GS1 application-identifier parsing. PDF417 error-correction levels. HRI text
placement and font metrics. Quiet-zone rules that differ per symbology. Each one is a
day you did not plan for, discovered after the feature shipped.

| Instead of… | You get |
|---|---|
| A QR library in each of your 4 services, in 4 languages | One implementation, one dependency graph, one CVE surface to patch |
| A 60 KB JavaScript encoder shipped to every visitor | An `<img src>` URL — and a code that renders in email clients and PDFs, where JS does not run |
| A frontend library that cannot produce print output | `pdf`/`eps` at 300 DPI with real millimetre sizing |
| A hosted QR API with a rate limit and a privacy policy | Your container, your network, your data — Wi-Fi passwords and TOTP secrets never leave the box |
| "We'll add barcodes later" | 15 symbologies behind the same request shape |

**When you should *not* use barqr:** one code, one service, one language, no styling,
no print output, no decode. Call the library. Adding a network hop to a solved problem
is not architecture. barqr earns its place the moment there is a *second* consumer —
or the first design review.

## Security

> [!WARNING]
> **Do not expose barqr directly to the internet.**
> It is designed to sit on an internal network and answer requests from *your* code.
> There is no tenancy model, no quota system, and no abuse protection beyond rate
> limiting — because that is not what it is for.

barqr enforces this rather than documenting it. It **refuses to start** when:

| Configuration | Result |
|---|---|
| Non-loopback bind + `AUTH_MODE=required` + no API keys | `exit 1` — the service would be unreachable-by-key yet exposed |
| `AUTH_MODE=open` + wildcard bind, without `BARQR_I_UNDERSTAND_OPEN_BIND=true` | `exit 1` — unauthenticated on every interface |

Try it:

```bash
$ docker run --rm -e BARQR_BIND=0.0.0.0 barqr:dev
barqr: configuration error, refusing to start
  - insecure configuration: BARQR_BIND="0.0.0.0" is not loopback and BARQR_AUTH_MODE=required,
    but BARQR_API_KEYS is empty; set BARQR_API_KEYS to one or more keys, or bind to 127.0.0.1
$ echo $?
1
```

API keys are SHA-256 hashed at boot — the plaintext never reaches the config struct —
and compared with `subtle.ConstantTimeCompare`. Payloads may contain Wi-Fi passwords
and TOTP secrets, so they are never logged. Full model in
[`docs/SECURITY.md`](docs/SECURITY.md).

## Configuration

Environment variables only. No config files, ever.

```bash
docker run --rm barqr:dev check-config    # validate and print the effective config
```

```
BARQR_BIND=127.0.0.1
BARQR_PORT=3000
BARQR_AUTH_MODE=required
BARQR_API_KEYS=<redacted: 0 key(s)>
BARQR_MAX_BODY=2MB
...
```

Secrets are redacted in that output by design — it is safe to paste into a bug report.
Invalid values are fatal, never silently defaulted; unknown `BARQR_*` variables warn at
boot so a typo like `BARQR_API_KEY` (singular) cannot silently leave you unauthenticated.

Full reference: [`docs/DEPLOY.md`](docs/DEPLOY.md).

## API

Live today:

| Endpoint | Purpose |
|---|---|
| `GET /v1/healthz` | Liveness. `200` while the process can serve at all. |
| `GET /v1/readyz` | Readiness. `503` the instant shutdown begins, so load balancers drain cleanly. |
| `GET /v1/version` | Build identity: version, commit, build date, Go version, platform. |

Full reference and the planned surface: [`docs/API.md`](docs/API.md).

### Coming in M1

```bash
curl "localhost:3000/v1/qr?data=https://example.com&output.format=ansi"
```

…renders the code as ANSI blocks straight into your terminal. `ascii` and `ansi` are
first-class output formats, not afterthoughts: a headless user must be able to *see*
the code without leaving the shell.

## Integrating barqr

barqr is a plain HTTP service with no client library and no SDK to keep up to date —
integration is whatever your stack already uses to make a GET request. Snippets below
marked ⏳ use `/v1/qr`, which arrives in M1; the transport contract they rely on is
already fixed.

### 1. As an image URL — no code at all ⏳

Because every option is a query parameter, a barqr URL *is* the image.

```html
<img src="https://barqr.internal/v1/qr?data=https://example.com&output.scale=8"
     width="240" height="240" alt="Open example.com">
```

This works anywhere a URL works: an email template, a Markdown file, a Google Doc, a
Notion page, a PDF generated by a reporting tool. It is the reason non-technical
teammates can be handed a link instead of a ticket.

### 2. From your backend ⏳

<details open>
<summary><b>curl</b></summary>

```bash
curl -sS -H "X-API-Key: $BARQR_KEY" \
  -H 'Content-Type: application/json' \
  -d '{"type":"wifi","payload":{"ssid":"Lobby","password":"guest2026","auth":"WPA"},
       "style":{"module":"dot"},"output":{"format":"png","scale":10}}' \
  http://barqr:3000/v1/qr -o wifi.png
```
</details>

<details>
<summary><b>Go</b></summary>

```go
req, _ := http.NewRequestWithContext(ctx, http.MethodGet,
    "http://barqr:3000/v1/qr?data="+url.QueryEscape(payload), nil)
req.Header.Set("X-API-Key", os.Getenv("BARQR_KEY"))

resp, err := client.Do(req)
if err != nil {
    return fmt.Errorf("rendering code: %w", err)
}
defer resp.Body.Close()
png, err := io.ReadAll(resp.Body)
```
</details>

<details>
<summary><b>Python</b></summary>

```python
png = httpx.get(
    "http://barqr:3000/v1/qr",
    params={"data": payload, "output.format": "png", "output.scale": 10},
    headers={"X-API-Key": os.environ["BARQR_KEY"]},
    timeout=5,
).content
```
</details>

<details>
<summary><b>Node</b></summary>

```js
const res = await fetch("http://barqr:3000/v1/qr?" + new URLSearchParams({
  data: payload, "output.format": "svg",
}), { headers: { "X-API-Key": process.env.BARQR_KEY } });
const svg = await res.text();
```
</details>

Note the dot-notation query keys (`output.format`, `style.module`). The same names work
as nested JSON, as multipart fields, and as query parameters — one decoder produces the
same request struct from all three transports, so switching between them is never a
rewrite.

### 3. Docker Compose — internal network only

`expose:` publishes the port to sibling containers but **not** to the host. This is the
intended deployment: barqr is reachable by your app and by nothing else.

```yaml
services:
  app:
    build: .
    environment:
      BARQR_URL: http://barqr:3000
      BARQR_KEY: ${BARQR_KEY:?set BARQR_KEY}
    depends_on: [barqr]

  barqr:
    image: barqr:dev          # ghcr.io/el-amin-dev/barqr:latest once published
    expose: ["3000"]          # NOT ports: — keep it off the host
    environment:
      BARQR_BIND: 0.0.0.0     # inside the container network namespace only
      BARQR_API_KEYS: ${BARQR_KEY:?set BARQR_KEY}
      BARQR_RATE_LIMIT: 600/min
    read_only: true
    cap_drop: [ALL]
    security_opt: ["no-new-privileges:true"]
```

### 4. Kubernetes — ClusterIP sidecar or shared service

```yaml
livenessProbe:
  httpGet: { path: /v1/healthz, port: 3000 }
readinessProbe:
  httpGet: { path: /v1/readyz, port: 3000 }
```

`/v1/readyz` flips to `503` the instant `SIGTERM` arrives and *before* connections are
cut, so a rolling update drains cleanly instead of dropping in-flight renders. Expose it
as a `ClusterIP` Service with a `NetworkPolicy` allowing ingress only from labelled
pods — no `Ingress` object. Ready-made manifests land in `deploy/k8s` at M9.

### 5. Behind a gateway or CDN ⏳

`GET /v1/qr` is pure and idempotent: the same query always yields the same bytes.
Responses carry an `ETag`, so any reverse proxy, API gateway, or CDN in front of barqr
turns repeat renders into `304`s for free. If you are serving the same product code on
a million page views, barqr renders it once.

When authentication is on, responses are marked `Cache-Control: private` so a shared
cache cannot serve one tenant's code to another.

### Scaling it

Every replica is identical and holds no state, so scaling is `replicas: N` behind any
load balancer — no sticky sessions, no shared volume, no coordination. Inside each
process, `BARQR_CONCURRENCY` bounds the worker semaphore: a burst queues instead of
thrashing the scheduler, and `BARQR_REQUEST_TIMEOUT` bounds how long a queued request
will wait before it is shed. Boot is under 100 ms, so a scale-up is useful immediately
and a spot-instance eviction costs nothing.

## Roadmap

| | Milestone | Contents |
|---|---|---|
| ✅ | **M0** skeleton | module, Makefile, lint, CI, config + security gate, `/healthz` `/readyz` `/version` |
| ⏳ | **M1** core path | Matrix/Canvas, QR encoder, square renderer, png/svg/ascii/ansi writers, `/v1/qr` |
| | **M2** request layer | one decoder for query/JSON/multipart, error shape, full middleware, ETag, metrics |
| | **M3** builders | 16 payload builders (wifi, vcard, otp, geo, …) with round-trip `Parse` |
| | **M4** barcodes | code128, ean13, itf, datamatrix, aztec, pdf417 + HRI text |
| | **M5** style engine | module/eye shapes, gradients, logo excavation, frames, scannability scoring |
| | **M6** print | pdf/eps writers, mm/inch/dpi sizing |
| | **M7** decode | `/v1/decode` — image in, data out, round-trip tested |
| | **M8** bulk | `/v1/batch` (csv/json → zip/pdf), `/v1/sheet` label grids, presets |
| | **M9** hardening | fuzzing, SBOM, cosign signing, v1.0.0 |
| | **M10** optional | zint `full` build tag, dynamic-code module |

## Development

```bash
make help          # every target, and which toolchain it will use
make ci            # build · vet · lint · test -race · docker build · smoke
make test          # race detector + coverage profile
make lint          # golangci-lint: errcheck, govet, staticcheck, gosec, revive
make smoke         # start the image and exercise it over HTTP
```

`make ci` is the gate: it is exactly what the pull-request workflow runs, so a green
laptop and a green CI mean the same thing. The Go toolchain and linter run in pinned
containers when they are not installed locally.

Details in [`docs/RUNBOOK.md`](docs/RUNBOOK.md). Design decisions and their trade-offs
are recorded in [`docs/DECISIONS.md`](docs/DECISIONS.md).

## Author

**Mohamed El Amin BOUCHAREB** — [@el-amin-dev](https://github.com/el-amin-dev)

barqr is a portfolio project built in the open: twelve-factor from the first commit,
security gates that fail the build rather than fill a backlog, and a milestone plan
that ships working software at every boundary. If you are reviewing it as a work
sample, [`docs/DECISIONS.md`](docs/DECISIONS.md) is the fastest way to see *why*
things are the way they are.

## License

[Apache-2.0](LICENSE) © 2026 Mohamed El Amin BOUCHAREB
