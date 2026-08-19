<div align="center">

# barqr

**QR codes and barcodes over HTTP. One stateless container. No GUI, no database, no SaaS.**

*Gotenberg is to documents what barqr is to codes.*

[![CI](https://github.com/el-amin-dev/barqr/actions/workflows/ci.yml/badge.svg)](https://github.com/el-amin-dev/barqr/actions/workflows/ci.yml)
[![CodeQL](https://github.com/el-amin-dev/barqr/actions/workflows/codeql.yml/badge.svg)](https://github.com/el-amin-dev/barqr/actions/workflows/codeql.yml)
[![Go Report Card](https://goreportcard.com/badge/github.com/el-amin-dev/barqr)](https://goreportcard.com/report/github.com/el-amin-dev/barqr)
[![License](https://img.shields.io/badge/license-Apache--2.0-blue.svg)](LICENSE)
[![Image](https://img.shields.io/badge/image-21.2%20MB-brightgreen)](Dockerfile)

**15 symbologies · 18 payload builders · 12 output formats · 4 ms per code**

</div>

---

## Quickstart

```bash
docker run --rm -p 3000:3000 -e BARQR_BIND=0.0.0.0 -e BARQR_API_KEYS=dev-key \
  ghcr.io/el-amin-dev/barqr:edge
curl -H 'X-API-Key: dev-key' 'localhost:3000/v1/qr?data=https://barqr.dev' -o qr.png
```

`edge` is the last commit of `main` that passed CI; `sha-<short>` beside it is the
immutable handle to pin. `:latest` and the semver tags appear with the first tagged
release.

That is the whole integration. Every option is a query parameter, so a barqr URL
*is* the image — paste one into an `<img src>` and you are done.

### See it without leaving the terminal

```bash
curl -H 'X-API-Key: dev-key' \
  'localhost:3000/v1/qr?data=https://barqr.dev&output.format=unicode'
```

```
                             
  █▀▀▀▀▀█  █▄█ █▄█  █▀▀▀▀▀█  
  █ ███ █ █▀▄▄█▀▄▄█ █ ███ █  
  █ ▀▀▀ █ █▄  █  ▄█ █ ▀▀▀ █  
  ▀▀▀▀▀▀▀ █ █ ▀ █▄█ ▀▀▀▀▀▀▀  
  █ █▀█▀▀▄  ██▀▄▄▀▄ ▀█▀▀▀▄   
    ██▀ ▀▀█ ▀ ██▀█▄▄█ ▀▀ ▀█  
  ▄▄▄▄▀█▀▄▀▄▀ ▀ ▀▀▄█▀▄▀▄▀█▀  
  █ ▄███▀ ▀▀ █▀ ▄▀▄ ▀██▀ ▀█  
  ▀ ▀▀  ▀ █ █▀▄▄▄▄█▀▀▀█▄▀    
  █▀▀▀▀▀█ ▄▀  ▄█▀▄█ ▀ █▄▀██  
  █ ███ █ █▀█ ▀ ▀████▀█▄█▄█  
  █ ▀▀▀ █ ▀█▀█▀ ▄▀▄▀▄▄▄█▀ █  
  ▀▀▀▀▀▀▀ ▀ ▀▀     ▀▀▀▀▀▀▀▀  
                             
```

That is real output, not a mock-up. `ascii`, `unicode`, and `ansi` are first-class
formats, not afterthoughts: a headless operator must be able to *see* a code — and
scan it off the screen — without a browser. `ansi` paints true-colour cells and
honours `style.fg`/`style.bg`.

No Go toolchain installed? `make` runs the compiler, linter, and tests in pinned
containers automatically. A fresh clone needs nothing but Docker.

### Or open it in a browser

```
http://localhost:3000/            the landing page — public, no key
http://localhost:3000/v1/docs     an interactive request builder
http://localhost:3000/v1/docs/swagger
http://localhost:3000/v1/docs/redoc
http://localhost:3000/v1/openapi.json
```

The dashboard builds a request by clicking and shows the URL, the curl line, the
JSON body and a live preview of the code, side by side. Swagger UI and ReDoc are
bundled in the image, not fetched from a CDN — the container is distroless and
may have no egress at all, and documentation that needs the internet is not
documentation you can rely on at three in the morning.

Every page renders itself from the live registries, so it cannot describe an
endpoint, symbology, or output format this build does not have.

The documentation asks for your API key once and remembers it for the session.
`BARQR_DOCS=false` removes the whole surface.


## Why barqr

Every project eventually needs a QR code, and every project solves it differently:
a JavaScript library in the frontend, a Python script in a cron job, a paid API with
a rate limit and a privacy policy. barqr replaces all three with one endpoint.

|  | |
|---|---|
| **Fast** | 4 ms for a PNG end to end, 2 µs to render, boot under 100 ms. Pure Go, no CGO, no subprocess, no disk I/O. |
| **Stateless** | No database, no session, no local writes. Any replica answers any request. |
| **Scales flat** | Add replicas. Concurrency is bounded per process by a semaphore, so a burst queues instead of thrashing. |
| **Cacheable** | `GET` is pure and carries a strong `ETag`. Any proxy or CDN in front turns repeat renders into `304`s. |
| **Drop-in microservice** | One container, one port, env-only config. Fits a compose file, a Nomad job, or a k8s Deployment without adapters. |
| **curl-first** | Every option reachable as a query param *or* a JSON body field *or* a multipart form field — one decoder, three transports. |
| **Deny by default** | Binds loopback, demands an API key, and refuses to boot if that combination would expose it. |
| **Small** | 21.2 MB distroless image: no shell, no package manager, unprivileged user, read-only rootfs. |

Non-goals, on purpose: no accounts, no billing, no image editing, no "AI"
anything. It renders codes.

The one HTML surface is the documentation, and it is deliberately scoped to
that: there is no dashboard for *making* codes for end users, no saved history,
nothing stateful. `BARQR_DOCS=false` removes it entirely.

### What it does

| | |
|---|---|
| **2D** | `qr` · `datamatrix` · `aztec` · `pdf417` |
| **1D** | `code128` · `code39` · `code93` · `codabar` · `ean13` · `ean8` · `upca` · `upce` · `itf` · `itf14` · `2of5` |
| **Payloads** | `url` · `wifi` · `vcard` · `mecard` · `email` · `tel` · `sms` · `whatsapp` · `geo` · `location` · `event` · `otp` · `crypto` · `epc` · `bookmark` · `app` · `text` · `raw` |
| **Output** | `png` · `svg` · `pdf` · `eps` · `jpeg` · `webp` · `ascii` · `unicode` · `ansi` · `json` · `datauri` · `txt` |
| **Style** | 7 module shapes, 5 eye shapes, gradients, logo embedding with excavation, frames, captions |
| **Also** | image decoding, batch (CSV/JSON → ZIP), label sheets, 8 layout presets and 30 themes, scannability scoring, a browser docs UI |

Twenty more symbologies are registered as *unavailable with a reason* rather than
omitted, so `/v1/symbologies` never lies about what a given build can do.

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
| "We'll add barcodes later" | 15 symbologies behind one request shape, and 20 more listed as unavailable rather than pretended away |

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

| Endpoint | Purpose |
|---|---|
| `GET\|POST /v1/qr` | Render a QR code. Every encode, style, and output option. |
| `GET\|POST /v1/barcode/{symbology}` | Render any of the other 14 symbologies. |
| `GET\|POST /v1/build/{type}` | Run a payload builder only — structured fields in, raw string out. |
| `POST /v1/validate` | Diagnose a request without rendering: scannability score, findings, fixes. |
| `POST /v1/decode` | Image in, data out. `parse=true` returns structured fields. |
| `POST /v1/batch` | Many codes at once: `items` or CSV in, ZIP or JSON out. |
| `POST /v1/sheet` | A grid of labels laid out on a page, as print-ready PDF. |
| `GET /v1/preset` · `GET\|POST /v1/preset/{name}` | Named option bundles — 8 layouts and 30 themes; the request overrides them. |
| `GET /v1/symbologies` | Machine-readable capability matrix for *this* build. |
| `GET /v1/openapi.json` | OpenAPI 3.1, generated from the live registries. |
| `GET /v1/healthz` · `/readyz` · `/version` | Liveness, readiness, build identity. Unauthenticated. |
| `GET /metrics` | Prometheus exposition. |

One request shape, three transports. The same field names work as nested JSON, as
dot-notation query parameters, and as multipart form fields, because a single
decoder produces the same struct from all three — switching between them is never a
rewrite.

```bash
# these three are the same request
curl 'localhost:3000/v1/qr?type=wifi&payload.ssid=Lobby&style.module=dot&output.format=svg'

curl -H 'Content-Type: application/json' localhost:3000/v1/qr -d '{
  "type": "wifi", "payload": {"ssid": "Lobby"},
  "style": {"module": "dot"}, "output": {"format": "svg"}}'

curl -F type=wifi -F payload.ssid=Lobby -F style.module=dot -F output.format=svg \
  localhost:3000/v1/qr
```

Unknown fields are rejected with the closest match rather than ignored — a silently
dropped `output.formt` produces the wrong image and no clue why:

```json
{"error":{"code":"UNKNOWN_FIELD","message":"unknown field \"output.formt\"",
          "field":"output.formt","hint":"did you mean \"output.format\"?",
          "request_id":"AGG4P7RB67RZ0CYP"}}
```

Full reference: [`docs/API.md`](docs/API.md).

### Point a code at a place

The `location` builder takes whatever a person actually types — an address in any
script, coordinates in any format, a plus code, a `geo:` URI, or a link they
pasted from Google, Apple, Waze, OSM or Bing — works out what it is, and turns it
into a map link.

```bash
curl -sG --data-urlencode 'payload.location=35.95277, 5.53753' \
  localhost:3000/v1/build/location
```

```json
{
  "type": "location",
  "data": "https://www.google.com/maps/search/?api=1&query=35.9527700%2C5.5375300",
  "detected": { "kind": "coordinates", "confidence": 1, "lat": 35.95277, "lng": 5.53753 }
}
```

`detected.kind` is one of `coordinates` · `address` · `plus_code` · `geo_uri` ·
`map_link` · `link` · `directions`, so a caller can act on what was understood
rather than guessing. Swap the endpoint and the same payload becomes the code
itself, in whatever format you asked for:

```bash
curl -G --data-urlencode 'payload.location=45 Rue Didouche Mourad, Alger' \
  'localhost:3000/v1/qr?type=location&output.format=svg' -o place.svg
```

It also does directions — `from Setif to Algiers`, or `Setif -> Algiers` — and
honours `origin`, `mode`, `zoom`, `language` and `region`.

### Make it look like your product

`GET /v1/preset/{name}` names a bundle of options, and there are two kinds of
bundle because callers ask two different questions. A **layout** — `default`
`print` `terminal` `web` `ticket` `label` `dark` `sticker` — answers *where is
this code going*, and sets format, resolution and error correction. A **theme**
answers *what should it look like*, and sets nothing but the six appearance keys.
So a caller has three levels of choice and not one: take `default`, name a
ready-made theme, or write `style.*` out in full.

```bash
curl -H 'X-API-Key: dev-key' \
  'localhost:3000/v1/preset/obsidian?data=https://barqr.dev&output.format=svg' -o code.svg
```

Thirty themes ship built in. Because a theme touches no output field, that is the
`obsidian` palette at whatever format, scale and error correction you ask for —
the request wins on every field it sets.

One rule runs through all thirty, and it is why they are safe as defaults: **the
accent lives in `eye_fg` only.** A scanner locates the three finder patterns
before it reads a single data module, so that is where colour can be spent for
free. The data modules stay near-neutral against their background, which is what
holds the contrast. A theme that tinted every module would look bolder on screen
and fail on a phone camera in a shop.

They are machine-checked rather than eyeballed. `TestEveryThemeScans` renders
every theme through the real encoder and renderer and puts it through the real
scannability report: it must pass, with module *and* finder contrast at 4.5:1 or
better — not the 3:1 the report treats as fatal. Several accents were deepened
one shade from their design-system source for exactly that reason. Fifteen themes
are dark-on-light and grade `excellent`; the fifteen light-on-dark ones grade
`good` and carry an `INVERTED` warning, which is a warning and not an error
because plenty of scanners cope and the caller chose it.

The full grouped list is in [`docs/API.md`](docs/API.md#presets), and
`GET /v1/preset` returns it from the running build.

### Will it actually scan?

`POST /v1/validate` answers that before you print ten thousand of them. The expensive
failure is not an invalid code — it is a *valid* code no scanner can read.

```bash
curl -s localhost:3000/v1/validate -H 'Content-Type: application/json' \
  -d '{"data":"https://barqr.dev","style":{"fg":"#e8e8e8"}}' | jq .scannability
```

```json
{
  "score": 30, "grade": "unscannable", "contrast_ratio": 1.2, "quiet_zone": 4,
  "issues": [{
    "code": "LOW_CONTRAST", "severity": "error",
    "message": "contrast between modules and background is 1.2:1",
    "hint": "use at least 4.5:1; a dark module colour on a light background is safest"
  }]
}
```

It checks contrast, inversion, quiet zone, transparent backgrounds, finder-pattern
legibility, gradients that fade out, and logos that eat more error correction than
the level can spare. `BARQR_STRICT_SCANNABILITY=strict` turns findings into
rejections.

## Integrating barqr

barqr is a plain HTTP service with no client library and no SDK to keep up to date —
integration is whatever your stack already uses to make a GET request.

### 1. As an image URL — no code at all

Because every option is a query parameter, a barqr URL *is* the image.

```html
<img src="https://barqr.internal/v1/qr?data=https://example.com&output.scale=8"
     width="240" height="240" alt="Open example.com">
```

This works anywhere a URL works: an email template, a Markdown file, a Google Doc, a
Notion page, a PDF generated by a reporting tool. It is the reason non-technical
teammates can be handed a link instead of a ticket.

### 2. From your backend

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
intended deployment: barqr is reachable by your app and by nothing else. The complete
file, commented line by line, is [`deploy/docker-compose.yml`](deploy/docker-compose.yml).

```yaml
services:
  app:
    image: your-app:latest    # whatever calls barqr
    environment:
      BARQR_URL: http://barqr:3000
      BARQR_KEY: ${BARQR_KEY:?set BARQR_KEY}
    depends_on: [barqr]

  barqr:
    image: barqr:dev          # or ghcr.io/el-amin-dev/barqr:edge
    expose: ["3000"]          # NOT ports: — keep it off the host
    environment:
      BARQR_BIND: 0.0.0.0     # inside the container network namespace only
      BARQR_API_KEYS: ${BARQR_KEY:?set BARQR_KEY}
      BARQR_RATE_LIMIT: 600/min
      BARQR_MAX_CANVAS_PX: 4000000   # the 25 MP default wants ~100 MB per request
      BARQR_SHUTDOWN_GRACE: 20s
    user: "65532:65532"
    read_only: true
    cap_drop: [ALL]
    security_opt: ["no-new-privileges:true"]
    stop_grace_period: 30s    # must exceed the drain window; it defaults to 10s
```

`stop_grace_period` is the one people leave out. Compose's default is 10 seconds —
shorter than even the default 15s drain — so without it every `compose up` of a new
image kills requests that were still being served.

### 4. Kubernetes — ClusterIP sidecar or shared service

```yaml
livenessProbe:
  httpGet: { path: /v1/healthz, port: 3000 }
readinessProbe:
  httpGet: { path: /v1/readyz, port: 3000 }
```

`/v1/readyz` flips to `503` the instant `SIGTERM` arrives and *before* connections are
cut, so a rolling update drains cleanly instead of dropping in-flight renders. Liveness
must not point at it for that same reason — a draining pod would be declared dead and
killed mid-drain. Expose it as a `ClusterIP` Service with a `NetworkPolicy` allowing
ingress only from labelled pods — no `Ingress` object. Applyable manifests are in
[`deploy/k8s`](deploy/k8s) (`kubectl apply -k deploy/k8s`); the reasoning is in
[`docs/DEPLOY.md`](docs/DEPLOY.md).

### 5. Behind a gateway or CDN

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

## Project status

Built to a ten-milestone plan, committed at every boundary. M0 through M9 are
done and tested. M10 is explicitly optional and not started.

| | Milestone | Contents |
|---|---|---|
| ✅ | **M0** skeleton | module, Makefile, lint, CI, config + startup security gate, probes |
| ✅ | **M1** core path | Matrix/Canvas, QR encoder, renderer, png/svg/ascii/ansi, `/v1/qr` |
| ✅ | **M2** request layer | one decoder for three transports, error shape, full middleware, ETag, metrics, OpenAPI |
| ✅ | **M3** builders | 17 payload builders with round-trip `Parse`, fuzzed |
| ✅ | **M4** barcodes | 14 further symbologies, check digits, HRI text |
| ✅ | **M5** style engine | module and eye shapes, gradients, logo excavation, frames, scannability |
| ✅ | **M6** print | pdf and eps writers, mm/inch/dpi sizing |
| ✅ | **M7** decode | `/v1/decode`, header-first bomb guards, round-trip tested |
| ✅ | **M8** bulk | `/v1/batch`, `/v1/sheet`, presets |
| ✅ | **M9** hardening | fuzzing, gosec, adversarial security review, SSRF-guarded egress, browser docs UI, the `location` builder (the 18th), 30 themes, `deploy/` manifests, SBOM + cosign + Trivy, continuous delivery to GHCR |
| | **M10** optional | zint `full` build tag, dynamic-code module — not started, see [ADR-013](docs/DECISIONS.md) |

### Benchmarks

Measured on an i7-1265U, `make bench`. A plain QR at ten pixels per module:

| | |
|---|---|
| Encode | 1.23 ms |
| Render (matrix → canvas) | 2.2 µs · 2 allocs |
| Full HTTP round trip to PNG | **4.0 ms** |

The PNG path uses an indexed palette whenever the image fits 256 colours, which a
plain code always does — measured at 0.8 ms and 772 bytes against 4.9 ms and 1592 for
true colour. Repeat renders of the same URL are `304`s.

### Test coverage

| Package | | Package | |
|---|---|---|---|
| `sheet` | 96.2% | `mapsurl` | 95.0% |
| `fetch` | 96.3% | `builder` | 94.5% |
| `encoder` | 94.5% | `batch` | 94.2% |
| `decoder` | 93.8% | `render` | 93.2% |
| `preset` | 93.0% | `writer` | 92.1% |
| `cmd/barqr` | 97.6% | `config` | 87.2% |
| `httpapi` | 82.7% | `version` | 100% |

Plus fuzz targets on the request decoder and every builder's `Parse`, and
`internal/doccheck`, which parses this README and the reference docs and
asserts them against the live registries — so the tables above cannot drift
from what the binary does.

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
