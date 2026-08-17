# Architecture Decision Records

> Append-only. One entry per significant decision. Newest on top.
> "Significant" = affects architecture, data model, security posture, or is hard to reverse.

<!-- append ADRs below, newest first -->

## ADR-012 — Decode guards run on the image header, before any pixel (2026-08-17)

- status: accepted
- context: `POST /v1/decode` accepts an arbitrary image from the network. A
  hundred-byte PNG can declare 50000x50000 pixels; decoding it allocates ten
  gigabytes before any cap on the *decoded* size could fire.
- decision: `internal/decoder` reads dimensions with `image.DecodeConfig` and
  rejects anything over the pixel cap **before** decoding a single pixel, and
  rejects an oversized input before touching it at all. Zero or negative caps
  normalise to the defaults rather than meaning "unlimited" — unlike
  `writer.OutputOpts`, there is no way to switch the decode guards off. Only
  six decoders are registered, and a `recover` wraps the third-party call as a
  backstop against a library bug.
- alternatives: cap the decoded image (too late); a timeout (the allocation
  happens faster than any useful timeout); trust the library (its own history
  says otherwise).
- consequences: an unusual but legitimate large scan is rejected. That is the
  right trade for the single most exposed surface in the service.

## ADR-011 — The request decoder is built by reflection over one struct (2026-08-17)

- status: accepted
- context: every option must be reachable as a query parameter, a JSON field,
  and a multipart field. Three hand-maintained mappings would drift, and the
  drift would be silent: a field reachable in JSON but not in the query looks
  like it works right up until someone tries the other transport.
- decision: `Request` is the single source of truth. `internal/httpapi` walks
  its JSON tags once at init and builds a dot-notation index that drives all
  three transports and the OpenAPI parameter list. A new field is reachable
  everywhere the moment it is declared.
- alternatives: three explicit mappings (drift); code generation (a build step
  for something reflection does once at startup).
- consequences: reflection in the request path, confined to a startup-built
  index and a type switch per field. Unknown fields are rejected with a
  closest-match suggestion instead of being ignored, because a silently dropped
  `output.formt` produces the wrong image and no clue why.

## ADR-010 — One error shape, mapped from sentinels rather than message text (2026-08-17)

- status: accepted
- context: five packages produce errors that must reach a client as something
  stable enough to switch on, without leaking internals.
- decision: every package exports sentinel errors; `asFault` maps them onto a
  flat `Fault` with a stable `code`, the offending `field` in dot notation, and
  a `hint`. Anything unmapped becomes a generic 500 with the detail logged, not
  returned. A test asserts no response ever contains a file path, a Go type
  name, a pointer, or a stack frame.
- alternatives: HTTP status codes alone (too coarse — six distinct 400s);
  matching on message text (rewording an error would break clients).
- consequences: a new error class needs a `case` in one function. Forgetting it
  degrades to a 500, which is loud enough to be caught, and safe by default.

## ADR-009 — PDF, EPS, and SVG are hand-written, with no dependency (2026-08-17)

- status: accepted
- context: print output needs millimetre-accurate PDF and EPS. The obvious
  answer is a PDF library, which brings a large dependency and its own CVE
  surface into an image that is otherwise 9 MB and has no shell.
- decision: all three vector writers are hand-rolled against the stdlib. They
  share page geometry, run merging, and colour flattening through
  `internal/writer/vector.go` so the formats cannot describe the same canvas
  differently. Horizontally adjacent modules merge into one rectangle, which
  cuts a typical content stream by an order of magnitude, and the stream is
  zlib-compressed.
- alternatives: gofpdf (a dependency, and more than we need); rasterise and
  embed a PNG (loses the vector output that is the whole point of print).
- consequences: we own the xref-offset arithmetic, so the tests parse the
  writer's own output. Verified externally with `mutool`, Ghostscript, and by
  decoding the rasterised result back with gozxing.

## ADR-008 — Payload builders must round-trip (2026-08-17)

- status: accepted
- context: barqr both writes and reads codes. A `Build` with no matching
  `Parse` means `/v1/decode?parse=true` cannot return structured fields, and a
  `Parse` that disagrees with `Build` is worse than none.
- decision: every builder implements `Build`, `Parse`, and `Fields`, and a
  single table test asserts `Parse(Build(p)) == p` across the whole registry.
  A fuzz target drives every `Parse` over arbitrary input.
- alternatives: build-only (halves the product); per-builder tests only (a new
  builder added later would not be covered by anything).
- consequences: the fuzz target immediately found a real bug — a builder that
  defaulted a field on `Build` but omitted it on `Parse`, so the round trip was
  not idempotent. That class of bug is invisible to example-based tests.

## ADR-007 — Scannability is a report, never a render failure (2026-08-17)

- status: accepted
- context: the expensive failure is not an invalid code, it is a *valid* code
  no scanner can read: brand colours that die under a shop camera, a quiet zone
  trimmed to fit a layout, a logo that eats the error-correction budget.
- decision: `render.Scannability` grades a canvas and returns findings with a
  severity, a message in the terms a designer thinks in, and a fix. It never
  fails. Whether a finding becomes a rejection is `BARQR_STRICT_SCANNABILITY`'s
  decision, one layer up, and `POST /v1/validate` exposes the report without
  rendering so a whole catalogue can be checked cheaply.
- alternatives: refuse to render a risky design (breaks legitimate uses and
  makes barqr an opinion rather than a tool); say nothing (the failure surfaces
  on printed material, which is the most expensive place to find it).
- consequences: thresholds are judgement calls and are documented as such in
  the code. Being wrong is a warning a caller can ignore, not a blocked render.


## ADR-006 — Containerised toolchain as the default fallback (2026-08-17)

- status: accepted
- context: `make ci` must produce the same result on a laptop and in CI, but a
  contributor's machine may not have Go, `golangci-lint`, or `gosec` installed — and a
  version skew between them is a silent source of "works on my machine".
- decision: `make` detects `go` on `PATH`. When it is absent, the Go and lint
  toolchains run in version-pinned containers (`golang:1.26`,
  `golangci/golangci-lint:v2.12.2-alpine`), with module and build caches under
  `.cache/` bind-mounted and owned by the invoking user. `TOOLCHAIN=local|docker`
  forces either mode.
- alternatives: require a local Go install (excludes contributors, invites version
  skew); commit a devcontainer only (does not help `make ci` from a plain shell);
  `go run` the linter from source (slow, unpinned).
- consequences: a fresh clone needs nothing but Docker. The dev image is Debian-based
  rather than Alpine because `go test -race` requires cgo and a C compiler — the
  *runtime* image stays minimal because the shipped binary is `CGO_ENABLED=0`. Two Go
  images are therefore pulled on a cold machine.

## ADR-005 — Smoke tests assert the security posture, not just the happy path (2026-08-17)

- status: accepted
- context: the security controls that matter most — the startup gate, the unprivileged
  image user — are exactly the ones that silently rot, because nothing fails when they
  regress.
- decision: `make smoke` asserts that the container **exits non-zero** when given a
  wildcard bind with no API keys, and that `Config.User` is `65532:65532`, alongside
  the endpoint checks. CI runs the same target against the image it built.
- alternatives: document the controls in `SECURITY.md` and trust review (they rot);
  unit tests only (would not catch a Dockerfile regression).
- consequences: deleting the `USER` line or weakening the config gate fails the build.
  The smoke target grows one assertion per security-relevant control rather than
  staying a liveness check.

## ADR-004 — API keys are hashed at boot and never retrievable (2026-08-17)

- status: accepted
- context: `--print-config`, structured logs, and error messages all risk echoing
  configuration. Payloads already carry Wi-Fi passwords and TOTP secrets; the API keys
  must not add to that surface.
- decision: `config.Load` digests each key with SHA-256 and discards the plaintext.
  `Config` exposes only `APIKeyCount()` and `AuthorizeKey(presented) bool`, which
  compares against every configured key with `subtle.ConstantTimeCompare` so neither
  the outcome nor which key matched is observable through timing. `Redacted()` renders
  `BARQR_API_KEYS` as `<redacted: N key(s)>`.
- alternatives: store plaintext and redact at print time (one missed call site leaks
  it); bcrypt/argon2 (these are high-entropy machine credentials, not passwords — the
  cost would be paid per request for no gain).
- consequences: keys cannot be echoed back for operator convenience, by construction. A
  test asserts the plaintext never appears in `Redacted()` output.

## ADR-003 — Invalid configuration is fatal; unknown variables warn (2026-08-17)

- status: accepted
- context: a service that silently ignores a misconfigured value fails in the worst
  possible way — looking healthy while behaving differently than the operator believes.
  A silently ignored `BARQR_AUTH_MODE` is a security incident.
- decision: any `BARQR_*` value that fails to parse or falls outside its range is
  fatal. `Load` aggregates *every* problem and reports them together, so a broken
  deployment is fixed in one pass rather than one restart per variable. An unrecognised
  `BARQR_*` variable is a warning, catching typos like `BARQR_API_KEY` (singular).
- alternatives: fall back to defaults with a warning (the failure mode above); fail on
  the first bad value (turns a five-variable mistake into five restarts).
- consequences: a typo in a non-critical variable takes the process down. This is the
  intended trade: barqr boots in under 100 ms and is trivially rolled back, so a loud,
  immediate failure costs far less than a quiet, wrong one.

## ADR-002 — Two configurations are startup-fatal (2026-08-17)

- status: accepted
- context: barqr has no tenancy model and no abuse protection beyond rate limiting. The
  realistic threat is not a determined attacker but an operator who binds `0.0.0.0` to
  "test something" and forgets. Documentation does not prevent this.
- decision: the process exits 1, with an explicit message, when (a) the bind is not
  loopback, `AUTH_MODE=required`, and no API keys are set, or (b) `AUTH_MODE=open` on a
  wildcard bind without `BARQR_I_UNDERSTAND_OPEN_BIND=true`.
- alternatives: warn and continue (ignored in practice); refuse only in "production"
  (no reliable signal for what production is); rely on network policy alone (defence in
  depth means the process defends itself too).
- consequences: the dangerous configuration remains *possible* — via an explicit
  acknowledgement variable — but never accidental. Rule (a) can annoy someone
  deliberately running a keyless brick; that is judged an acceptable false positive.

## ADR-001 — Registries over switch statements for the four core contracts (2026-08-17)

- status: accepted
- context: barqr will accumulate ~17 builders, ~15 symbologies, ~11 output formats, and
  a growing set of renderers. Any design where the HTTP layer knows their names turns
  every addition into a change across several files.
- decision: `Builder`, `Encoder`, `Renderer`, and `Writer` are interfaces backed by
  string-keyed registries populated in `init()` and read-only afterwards. Adding a
  symbology is one file plus one registration; the HTTP layer only ever looks up a key.
  Capabilities are data (`Capabilities{Available, Reason}`) so `/v1/symbologies` can be
  honest about what a given build supports.
- alternatives: a switch in the handler (touches the HTTP layer for every addition,
  and the compiler cannot tell you what you forgot); code generation (premature at this
  size).
- consequences: registries are the only global mutable state in the program, and the
  quality bar forbids adding more. Registration order must not matter, and a missing
  key is a normal `404`-class error rather than a panic.
