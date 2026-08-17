# API reference

Base URL in examples: `http://localhost:3000`. Every route lives under `/v1`, which
will not break once `v1.0.0` is tagged.

The live, machine-readable version of this document is served at
`GET /v1/openapi.json`, generated from the same registries the server dispatches on —
so it cannot drift from the implementation. `GET /v1/symbologies` reports what *this
build* can actually do.

---

## Contents

- [One request, three transports](#one-request-three-transports)
- [Request fields](#request-fields)
- [Rendering](#rendering) — `/v1/qr`, `/v1/barcode/{symbology}`
- [Building payloads](#building-payloads) — `/v1/build/{type}`
- [Validating](#validating) — `/v1/validate`
- [Decoding](#decoding) — `/v1/decode`
- [Bulk](#bulk) — `/v1/batch`, `/v1/sheet`
- [Presets](#presets) — `/v1/preset`
- [Capabilities](#capabilities) — `/v1/symbologies`, `/v1/openapi.json`
- [Operational](#operational) — `/v1/healthz`, `/v1/readyz`, `/v1/version`, `/metrics`
- [Errors](#errors)
- [Caching](#caching)
- [Authentication](#authentication)

---

## One request, three transports

The same field names work as a dot-notation query string, as nested JSON, and as
multipart form fields. A single decoder produces the same struct from all three, so
moving between them is never a rewrite.

```bash
# query
curl 'localhost:3000/v1/qr?type=wifi&payload.ssid=Lobby&style.module=dot&output.format=svg'

# json
curl -H 'Content-Type: application/json' localhost:3000/v1/qr -d '{
  "type": "wifi",
  "payload": {"ssid": "Lobby"},
  "style":   {"module": "dot"},
  "output":  {"format": "svg"}
}'

# multipart — file parts override string fields of the same name
curl -F type=wifi -F payload.ssid=Lobby -F style.module=dot \
     -F output.format=svg -F style.logo=@logo.png localhost:3000/v1/qr
```

**Precedence.** Query parameters are applied first, then the body, so on a `POST`
carrying both, the body wins — it is the more specific statement of intent. Within a
multipart form, file parts are applied last.

**Unknown fields are rejected**, never ignored, with the closest match as a hint. A
silently dropped `output.formt` produces the wrong image and no clue why.

A `POST` body with no `Content-Type` is taken as the raw data to encode, which is
what `curl --data-binary @file` sends.

---

## Request fields

### Top level

| Field | Type | Meaning |
|---|---|---|
| `type` | string | Selects a payload builder. With this set, `payload` is its input. |
| `payload` | object | The builder's fields. In a query: `payload.<field>=<value>`. |
| `data` | string | The raw string to encode, when no `type` is given. |
| `symbology` | string | Which code to produce. Defaults to the endpoint's own. |

Setting both `data` and `type` is an error rather than a silent precedence rule:
guessing wrong produces a perfectly valid code containing the wrong thing.

### `encode` — symbology options

| Field | Default | Meaning |
|---|---|---|
| `encode.ecc` | `BARQR_DEFAULT_ECC` (`M`) | Error correction: `L` `M` `Q` `H`. |
| `encode.version` | `auto` | Symbology version. Automatic in the default build. |
| `encode.mask` | `auto` | Data-mask pattern. Automatic in the default build. |
| `encode.quiet_zone` | the symbology's own | Margin in modules. |

`version` and `mask` accept an integer or the literal `"auto"`.

### `style` — appearance

| Field | Default | Meaning |
|---|---|---|
| `style.module` | `square` | `square` `dot` `rounded` `diamond` `classy` `vertical` `horizontal` |
| `style.eye` | `square` | Finder-pattern frame: `square` `circle` `rounded` `leaf` `shield` |
| `style.eye_ball` | `square` | Finder-pattern centre, same set. |
| `style.fg` | `#000000` | Module colour. |
| `style.bg` | `#ffffff` | Background. `transparent` is legal. |
| `style.eye_fg` | — | Colours the finder patterns differently. |
| `style.gradient` | — | `linear(45deg,#000,#00f)` or `radial(#000,#333)`. Replaces `fg`. |
| `style.logo` | — | A `data:` URI. Remote URLs need `BARQR_ALLOW_REMOTE_FETCH`. |
| `style.logo_scale` | `0.2` | Logo width as a fraction of the code, `0.05`–`0.35`. |
| `style.excavate` | `false` | Clear the modules behind the logo. Costs error correction. |
| `style.frame` | — | `border` `rounded` `banner` `bubble` |
| `style.caption` | — | Text beneath the code. |
| `style.bar_height` | auto | Linear codes only, in modules. |
| `style.hri` | `true` | Human-readable text under a linear code. |

Colours accept `#rgb`, `#rgba`, `#rrggbb`, `#rrggbbaa`, or a name
(`black` `white` `transparent` `red` `green` `blue` `yellow` `cyan` `magenta` `gray`).

### `output` — serialisation

| Field | Default | Meaning |
|---|---|---|
| `output.format` | `BARQR_DEFAULT_FORMAT` (`png`) | See [formats](#output-formats). |
| `output.scale` | `10` | Pixels per module. |
| `output.size` | — | Total width in `unit`. Overrides `scale`. |
| `output.unit` | `px` | `px` `mm` `in` |
| `output.dpi` | `300` | Converts physical units to pixels; written into the file. |
| `output.quality` | `92` | JPEG quality, 1–100. |

Sizing always resolves to a **whole** number of pixels per module. A fractional module
width is the most common cause of a code that looks fine and scans badly, because the
rasteriser rounds different modules differently.

### `meta` — response shaping

| Field | Default | Meaning |
|---|---|---|
| `meta.filename` | the symbology | Download name. The extension is forced to match the format. |
| `meta.attachment` | `false` | `Content-Disposition: attachment` instead of `inline`. |

---

## Rendering

### `GET|POST /v1/qr`

Renders a QR code.

```bash
curl 'localhost:3000/v1/qr?data=https://barqr.dev&output.scale=8' -o qr.png
```

### `GET|POST /v1/barcode/{symbology}`

Renders any other registered symbology.

```bash
curl 'localhost:3000/v1/barcode/ean13?data=590123412345' -o barcode.png
curl 'localhost:3000/v1/barcode/code128?data=SHIP-00417&style.bar_height=40' -o ship.png
```

**2D** — `qr` · `datamatrix` · `aztec` · `pdf417`
**1D** — `code128` · `code39` · `code93` · `codabar` · `ean13` · `ean8` · `upca` ·
`upce` · `itf` · `itf14` · `2of5`

Length and alphabet rules are enforced with specific errors: an EAN-13 with a wrong
check digit is told which digit was expected, and an ITF with an odd digit count is
told to pad with a leading zero.

Twenty further symbologies are registered as **unavailable with a reason** rather than
omitted, so `/v1/symbologies` never claims something does not exist when it merely is
not compiled in.

### Output formats

| Format | MIME | Notes |
|---|---|---|
| `png` | `image/png` | Default. Indexed palette when the image fits 256 colours. |
| `svg` | `image/svg+xml` | One path for all modules; `shape-rendering="crispEdges"`. |
| `pdf` | `application/pdf` | Hand-written, millimetre-accurate, Flate-compressed. |
| `eps` | `application/postscript` | Same geometry as the PDF writer. |
| `jpeg` | `image/jpeg` | No alpha; composited over the background first. |
| `webp` | `image/webp` | Lossless (VP8L); `quality` is accepted but has no effect. |
| `ascii` / `txt` | `text/plain` | Two characters per module. |
| `unicode` | `text/plain` | Half-blocks: two matrix rows per terminal row. |
| `ansi` | `text/plain` | True-colour cells, honours `style.fg`/`style.bg`. |
| `json` | `application/json` | The module grid as `0`/`1` strings, plus metadata. |
| `datauri` | `text/plain` | A base64 `data:image/png` URI. |

---

## Building payloads

### `GET|POST /v1/build/{type}`

Runs a builder and returns the raw string, with no rendering. Useful for inspecting
exactly what would be encoded — and for using barqr's payload formats from another
tool.

```bash
curl 'localhost:3000/v1/build/wifi?payload.ssid=Lobby&payload.password=guest2026&payload.auth=WPA'
```

```json
{ "type": "wifi", "data": "WIFI:T:WPA;S:Lobby;P:guest2026;;", "length": 32 }
```

| Type | Produces |
|---|---|
| `text` | the text verbatim |
| `url` | a normalised URL; adds `https://` when no scheme is given |
| `email` | `mailto:` with subject and body |
| `tel` | `tel:` |
| `sms` | `SMSTO:` — one interpretation everywhere, unlike RFC 5724 `sms:` |
| `whatsapp` | `https://wa.me/…?text=` |
| `vcard` | vCard 3.0 |
| `mecard` | `MECARD:` |
| `wifi` | `WIFI:` — including ZXing's hex-quoting rule most generators miss |
| `geo` | `geo:lat,lon` |
| `event` | iCalendar `VEVENT` |
| `otp` | `otpauth://totp` / `hotp` |
| `crypto` | BIP-21 for bitcoin, ethereum, litecoin |
| `epc` | SEPA credit transfer ("GiroCode") |
| `bookmark` | `MEBKM:` |
| `app` | a store or intent link |
| `raw` | the string with no validation at all |

Every builder round-trips: `Parse(Build(payload))` returns an equal payload, asserted
across the whole registry and fuzzed.

> **Payloads are secrets.** A `wifi` or `otp` payload carries a network key or an
> authenticator seed. barqr never logs payload contents, and both payload types
> redact themselves when formatted.

---

## Validating

### `POST /v1/validate`

Runs build, encode, and render, then reports — without serialising an image. Cheap
enough to run over a whole catalogue before printing it.

```bash
curl -s localhost:3000/v1/validate -H 'Content-Type: application/json' \
  -d '{"data":"https://barqr.dev","style":{"fg":"#e8e8e8"}}'
```

```json
{
  "valid": true,
  "symbology": "qr",
  "matrix": { "cols": 33, "rows": 33, "quiet_zone": 4, "dark_ratio": 0.44 },
  "scannability": {
    "score": 30, "grade": "unscannable", "contrast_ratio": 1.2,
    "issues": [{
      "code": "LOW_CONTRAST", "severity": "error",
      "message": "contrast between modules and background is 1.2:1",
      "hint": "use at least 4.5:1; a dark module colour on a light background is safest"
    }]
  }
}
```

Grades are `excellent` · `good` · `risky` · `unscannable`. A single `error` finding
caps the grade regardless of score.

| Code | Severity | Fires when |
|---|---|---|
| `LOW_CONTRAST` | error | below 3:1 — a camera cannot separate the modules |
| `MARGINAL_CONTRAST` | warn | 3:1 to 4.5:1 — device-dependent |
| `LOW_EYE_CONTRAST` | error | the finder patterns are washed out |
| `QUIET_ZONE_TOO_SMALL` | warn / error | below the symbology's margin; error at zero |
| `INVERTED` | warn | light modules on dark; many scanners assume the opposite |
| `TRANSPARENT_BACKGROUND` | warn | real contrast depends on what sits behind it |
| `GRADIENT_LOW_CONTRAST` | error | the darkest stop is unreadable |
| `GRADIENT_FADES_OUT` | warn | the lightest stop fades towards the background |
| `LOGO_TOO_LARGE` | error | above ~25% of the symbol area |
| `LOGO_EXCEEDS_ECC` | warn | above ~8% with error correction below `H` |
| `DEGENERATE_MATRIX` | error | almost every module is one colour |

`BARQR_STRICT_SCANNABILITY=strict` turns findings into rejections at render time.

---

## Decoding

### `POST /v1/decode`

Image in, data out.

```bash
curl -s localhost:3000/v1/decode?parse=true \
     -H 'Content-Type: application/octet-stream' --data-binary @code.png
curl -s localhost:3000/v1/decode -F image=@photo.jpg -F try_harder=true -F multi=true
```

```json
{ "count": 1, "results": [
  { "symbology": "qr", "data": "WIFI:T:WPA;S:Lobby;P:guest2026;;",
    "type": "wifi", "payload": {"ssid":"Lobby","password":"guest2026","auth":"WPA"} }
]}
```

| Option | Default | Meaning |
|---|---|---|
| `try_harder` | `false` | Slower, more thorough scan. |
| `multi` | `false` | Find every code in the image, not just the first. |
| `symbologies` | all | Comma-separated restriction. |
| `parse` | `false` | Run the result back through the builders for structured fields. |

Accepts `png` `jpeg` `gif` `webp` `bmp` `tiff`, as a raw body, a multipart `image`
part, or a `data:` URI in a JSON `image` field.

**Guards.** Dimensions are read from the image header and rejected before a single
pixel is decoded — a hundred-byte PNG can declare fifty thousand pixels square.
Byte and pixel caps cannot be switched off. `pdf417` decoding is not available and
says so rather than silently missing codes.

---

## Bulk

### `POST /v1/batch`

Many codes in one request.

```bash
curl -s localhost:3000/v1/batch -H 'Content-Type: application/json' -d '{
  "items": [
    {"id": "sku-1", "data": "https://example.com/1"},
    {"id": "sku-2", "type": "wifi", "payload": {"ssid": "Lobby", "password": "guest2026"}}
  ],
  "defaults": {"output.format": "svg", "output.scale": "6"},
  "output": "zip"
}' -o codes.zip

# or a spreadsheet export, straight in
curl -s localhost:3000/v1/batch?output=zip -H 'Content-Type: text/csv' \
     --data-binary @products.csv -o codes.zip
```

CSV takes a header row; `id`, `data`, `type` map onto the item, and any
`style.*` / `encode.*` / `output.*` / `payload.*` column becomes a per-item option.
Errors name the row.

`output` is `zip` (one file per item plus a `results.json` manifest) or `json`
(bodies base64-encoded). One item failing does not fail the batch — its result
carries the reason and the rest are returned. `MAX_BATCH_ITEMS` bounds the size.

### `POST /v1/sheet`

A grid of labels as print-ready PDF.

```bash
curl -s localhost:3000/v1/sheet -H 'Content-Type: application/json' -d '{
  "template": "avery-l7160",
  "caption": true,
  "skip": 2,
  "items": [{"id": "A-001", "data": "https://example.com/a"}]
}' -o labels.pdf
```

| Field | Meaning |
|---|---|
| `template` | Named label stock — see `GET /v1/sheet/templates`. |
| `layout` | Custom grid in millimetres, for stock you measured yourself. |
| `skip` | Leave the first N positions blank, to reuse a part-used sheet. |
| `caption` | Draw each item's id beneath its code. |
| `items` / `csv` | The same shapes `/v1/batch` accepts. |

Codes are rasterised at a size derived from their cell, not stretched from a
screen-sized image. A failed row leaves a blank label and sets `X-Sheet-Failed`.

---

## Presets

### `GET /v1/preset` · `GET|POST /v1/preset/{name}`

A preset is a saved bundle of options. The request overrides it, so a preset is a
starting point rather than a straitjacket.

```bash
curl 'localhost:3000/v1/preset/print?data=https://barqr.dev' -o label.pdf
curl 'localhost:3000/v1/preset/print?data=hi&output.format=svg'   # print, but SVG
```

Built in: `default` · `print` · `terminal` · `web` · `ticket` · `label` · `dark` ·
`sticker`. `BARQR_PRESETS_PATH` adds JSON files; one with a built-in's name overrides
it. A malformed file is a boot warning, not a failure.

---

## Capabilities

### `GET /v1/symbologies`

What *this build* can encode, render, and write — including what it cannot, and why.

```json
{
  "symbologies": [
    {"name": "qr", "title": "QR Code", "kind": "2d", "available": true,
     "ecc_levels": ["L","M","Q","H"], "quiet_zone": 4, "max_length": 2953,
     "notes": "version and mask are selected automatically; pinning either requires the full build"},
    {"name": "maxicode", "title": "MaxiCode", "kind": "2d", "available": false,
     "reason": "requires the full build (zint)"}
  ],
  "formats":  [{"name": "png", "mime": "image/png", "extension": "png", "binary": true}],
  "builders": [{"name": "wifi", "fields": [{"name": "ssid", "required": true, "…": "…"}]}],
  "styles":   {"module": ["classy","diamond","dot","…"], "eye": ["circle","…"]},
  "defaults": {"symbology": "qr", "format": "png", "ecc": "M", "scale": 10},
  "limits":   {"max_canvas_px": 25000000, "max_body_bytes": 2097152, "max_batch_items": 1000}
}
```

### `GET /v1/openapi.json`

OpenAPI 3.1, generated at request time from the live registries and the request
struct's own tags. Enums list exactly what this instance accepts.

---

## Operational

These three sit outside authentication: a probe cannot be expected to hold an API
key, and they disclose nothing sensitive.

| Endpoint | Answers |
|---|---|
| `GET /v1/healthz` | `200` while the process can serve at all, **including while draining**. A failing liveness probe means "restart me". |
| `GET /v1/readyz` | `200` when accepting traffic; `503` the instant shutdown begins, so a load balancer drains before connections are cut. |
| `GET /v1/version` | Version, commit, build date, Go version, platform. |
| `GET /metrics` | Prometheus exposition, when `BARQR_METRICS=true`. |

Metrics: `barqr_http_requests_total`, `barqr_http_request_duration_seconds`,
`barqr_renders_total{symbology,format}`, `barqr_output_bytes_total`,
`barqr_requests_in_flight`, `barqr_rate_limited_total`, plus the Go and process
collectors. The route label is the route *template*, never the raw path, so query
strings cannot explode label cardinality.

---

## Errors

One shape, from every endpoint and every layer.

```json
{
  "error": {
    "code": "DATA_TOO_LONG",
    "message": "data too long for this symbology: 3000 bytes exceeds the 2953-byte capacity of a QR symbol",
    "field": "data",
    "hint": "use code128 for arbitrary-length data",
    "request_id": "01JC8W4K3M9QZ7"
  }
}
```

| Field | Always | Meaning |
|---|---|---|
| `code` | yes | Stable identifier. **Switch on this, never on `message`.** |
| `message` | yes | Human-readable summary. |
| `field` | no | The offending field, in dot notation. |
| `expected` / `got` | no | What was required versus what arrived. |
| `hint` | no | The next thing to try. |
| `request_id` | yes | Matches the server log line. Quote it in a bug report. |

Codes are mapped from each package's sentinel errors rather than message text, so
rewording an error can never break a client.

`BAD_REQUEST` · `UNKNOWN_FIELD` · `INVALID_VALUE` · `MISSING_DATA` · `UNKNOWN_TYPE` ·
`INVALID_PAYLOAD` · `UNKNOWN_SYMBOLOGY` · `SYMBOLOGY_UNAVAILABLE` · `DATA_TOO_LONG` ·
`INVALID_DATA` · `UNSUPPORTED_OPTION` · `UNKNOWN_FORMAT` · `UNKNOWN_SHAPE` ·
`INVALID_COLOR` · `CANVAS_TOO_LARGE` · `UNSCANNABLE` · `BODY_TOO_LARGE` ·
`UNAUTHORIZED` · `RATE_LIMITED` · `TIMEOUT` · `OVERLOADED` · `NOT_FOUND` ·
`METHOD_NOT_ALLOWED` · `INTERNAL`

A response never contains a stack trace, a file path, an environment value, an
internal type name, or a pointer. That is asserted by a test, not a convention.

---

## Caching

`GET` renders are pure: the same query always produces the same bytes. Every rendered
response carries a strong `ETag`, so a proxy, gateway, or CDN in front of barqr turns
repeat renders into `304`s.

```bash
etag=$(curl -sD - -o /dev/null 'localhost:3000/v1/qr?data=hi' | awk '/^etag:/{print $2}')
curl -s -o /dev/null -w '%{http_code}\n' -H "If-None-Match: $etag" 'localhost:3000/v1/qr?data=hi'
# 304
```

With authentication on, responses are `Cache-Control: private` so a shared cache
cannot serve one caller's code to another. Errors are always `no-store`, and decode,
batch, and sheet responses are never cacheable.

Rendered responses also carry `Content-Security-Policy: default-src 'none'; sandbox`,
`X-Content-Type-Options: nosniff`, `X-Frame-Options: DENY`, and
`Referrer-Policy: no-referrer` — SVG can carry script, and it is served inline.

---

## Authentication

When `BARQR_AUTH_MODE=required` (the default), every endpoint except the probes needs
a key:

```bash
curl -H 'X-API-Key: your-key'        localhost:3000/v1/qr?data=hi
curl -H 'Authorization: Bearer your-key' localhost:3000/v1/qr?data=hi
```

Keys are SHA-256 hashed at boot — the plaintext never reaches the configuration — and
compared with `subtle.ConstantTimeCompare` against every configured key, so neither
the outcome nor which key matched leaks through timing. Logs and rate-limit buckets
identify a key by its position (`k1`, `k2`), never by its value.

Rate limiting is per key (`BARQR_RATE_LIMIT`, default `120/min`) and answers `429`
with `Retry-After`. Beyond that, `BARQR_CONCURRENCY` bounds work in flight: a burst
queues rather than thrashes, and a request that waits past its own deadline is shed
with `503 OVERLOADED` rather than starting work nobody is waiting for.

See [`SECURITY.md`](SECURITY.md) for the full model.
