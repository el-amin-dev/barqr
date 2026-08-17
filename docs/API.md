# API reference

barqr speaks one versioned surface: `/v1`. Everything below is what exists **today**
(milestone M0). Endpoints scheduled for later milestones are listed at the bottom so
that integrators can design against the shape before it ships.

Base URL in examples: `http://localhost:3000`.

---

## Conventions

| | |
|---|---|
| Content type | `application/json; charset=utf-8` unless an image format is requested |
| Versioning | every route lives under `/v1`; `/v1` will not break once `v1.0.0` is tagged |
| Authentication | `X-API-Key: <key>` or `Authorization: Bearer <key>` when `BARQR_AUTH_MODE=required` |
| Errors | one shape, always — see [Errors](#errors) |

---

## Operational endpoints

### `GET /v1/healthz`

Liveness. Answers `200` for as long as the process can serve requests at all,
**including while draining**, because a failing liveness probe means *restart me*, not
*stop sending me traffic*.

```bash
curl -s localhost:3000/v1/healthz
```

```json
{ "status": "ok" }
```

| Status | Meaning |
|---|---|
| `200` | Process is alive. |

---

### `GET /v1/readyz`

Readiness. Reports whether *this replica* wants traffic. It flips to `503` the moment
shutdown begins — before connections are cut — so a load balancer drains the replica
instead of dropping in-flight renders.

```bash
curl -s localhost:3000/v1/readyz
```

```json
{ "status": "ready" }
```

| Status | Body | Meaning |
|---|---|---|
| `200` | `{"status":"ready"}` | Accepting traffic. |
| `503` | `{"status":"draining"}` | Shutting down, or not yet serving. |

---

### `GET /v1/version`

Build identity of the running binary. Useful for confirming which revision a replica
is actually running after a rollout.

```bash
curl -s localhost:3000/v1/version
```

```json
{
  "version": "v1.2.3",
  "commit": "a530461",
  "date": "2026-08-17T10:55:21Z",
  "go": "go1.26.6",
  "platform": "linux/amd64",
  "name": "barqr"
}
```

| Field | Meaning |
|---|---|
| `version` | Release tag, or `dev` for an unreleased build. |
| `commit` | Git revision the binary was built from. |
| `date` | RFC 3339 build timestamp. |
| `go` | Go runtime version. |
| `platform` | `GOOS/GOARCH`. |
| `name` | Always `barqr`. |

---

## Errors

Every error — from any endpoint, at any layer — uses one shape. Stack traces, file
paths, environment values, and internal type names are never included.

```json
{
  "error": {
    "code": "DATA_TOO_LONG",
    "message": "data exceeds the capacity of this symbology",
    "field": "data",
    "expected": "12–13 digits",
    "got": "11",
    "hint": "use code128 for arbitrary-length data",
    "request_id": "01JC8W4K3M9QZ7"
  }
}
```

| Field | Always present | Meaning |
|---|---|---|
| `code` | yes | Stable machine-readable identifier. Switch on this, never on `message`. |
| `message` | yes | Human-readable summary. |
| `field` | no | The request field at fault, in dot notation (`output.scale`). |
| `expected` / `got` | no | What was required versus what arrived. |
| `hint` | no | The next thing to try. |
| `request_id` | yes | Correlates with the server log line. Quote it in bug reports. |

Unknown request fields are rejected with `400` and a closest-match suggestion rather
than silently ignored — a typo in `output.format` must not quietly produce a PNG.

The full error catalogue is generated from code once the request layer lands (M2).

---

## Not yet implemented

The surface below is designed and scheduled. It is documented here so integrations can
be planned, not because it works today.

| Milestone | Endpoint | Purpose |
|---|---|---|
| M1 | `GET\|POST /v1/qr` | Render a QR code. All encode, style, and output options. |
| M2 | `GET /v1/symbologies` | Machine-readable capability matrix — honest about what this build supports. |
| M2 | `POST /v1/validate` | Diagnostics with zero rendering. |
| M2 | `GET /v1/openapi.json` | Generated OpenAPI document. |
| M3 | `GET\|POST /v1/build/{type}` | Builder only — payload in, raw encoded string out. |
| M4 | `GET\|POST /v1/barcode/{symbology}` | 1D and 2D symbologies beyond QR. |
| M7 | `POST /v1/decode` | Image in, data out, with optional payload parsing. |
| M8 | `POST /v1/batch` | Many codes at once: `items` or CSV in, ZIP/PDF/JSON out. |
| M8 | `POST /v1/sheet` | Label-grid layout to PDF. |
| M8 | `GET\|POST /v1/preset/{name}` | Named, server-side option bundles. |
| M9 | `GET /metrics` | Prometheus exposition (already gated by `BARQR_METRICS`). |
| M4+ | `/v1/dynamic/*` | Opt-in redirect module, off by default. |

### Planned request shape

One struct, three transports. The same field names work as nested JSON, as
dot-notation query parameters, and as multipart form fields; a single decoder produces
the same request from all three.

```json
{
  "type": "wifi",
  "payload": { "ssid": "Lobby", "password": "guest2026", "auth": "WPA" },
  "data": "raw string, used when no type is given",
  "symbology": "qr",
  "encode": { "ecc": "H", "version": "auto", "mask": "auto", "quiet_zone": 4 },
  "style":  { "module": "dot", "fg": "#111", "bg": "transparent", "logo": "…" },
  "output": { "format": "png", "scale": 10, "dpi": 300, "unit": "px" },
  "meta":   { "filename": "q.png", "attachment": false }
}
```

As a query string:

```
/v1/qr?type=wifi&payload.ssid=Lobby&style.module=dot&output.format=svg
```

### Planned builders (M3)

`text` · `url` · `email` · `tel` · `sms` · `whatsapp` · `vcard` · `mecard` · `wifi` ·
`geo` · `event` · `otp` · `crypto` · `epc` · `bookmark` · `app` · `raw`

Each builder round-trips: `Build(payload)` produces the encoded string, `Parse(raw)`
recovers the payload, and a test asserts
`Build → Encode → Render → Write(png) → Decode → Parse == payload`.

### Planned symbologies (M4)

**2D** — `qr` · `datamatrix` · `aztec` · `pdf417`
**1D** — `code128` · `code39` · `code93` · `codabar` · `ean13` · `ean8` · `upca` ·
`upce` · `itf` · `itf14` · `2of5`

Symbologies that need the optional `full` build tag are registered as unavailable with
a reason, so `/v1/symbologies` never lies about what this binary can do.

### Planned output formats (M1–M6)

`png` · `svg` · `jpeg` · `webp` · `pdf` · `ascii` · `unicode` · `ansi` · `json` ·
`datauri` · `txt`

`ascii` and `ansi` are first-class: a headless operator must be able to *see* a code
without leaving the terminal.
