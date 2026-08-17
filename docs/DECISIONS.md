# Architecture Decision Records

> Append-only. One entry per significant decision. Newest on top.
> "Significant" = affects architecture, data model, security posture, or is hard to reverse.

<!-- append ADRs below, newest first -->

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
