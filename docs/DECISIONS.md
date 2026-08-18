# Architecture Decision Records

> Append-only. One entry per significant decision. Newest on top.
> "Significant" = affects architecture, data model, security posture, or is hard to reverse.

<!-- append ADRs below, newest first -->

## ADR-017 — Phone normalisation is opt-in by region, never assumed (2026-08-19)

- status: accepted
- context: a user built a vCard from a customer record held nationally
  (`0664108852`). Nothing in the payload said Algeria, so the importing contacts
  app guessed a country from the *device* and displayed North-American grouping.
  It was reported as a data error; the data was right and the payload was
  ambiguous. A number with no country code also cannot be dialled from abroad.
- decision: add an optional `payload.phone_region` (ISO 3166-1 alpha-2) to the
  four phone-carrying builders. A number already in `+` form passes through and
  the region is never consulted; a national number plus a region becomes E.164
  and is validated; an unknown region, or a number invalid for it, is a 400. **A
  national number with no region is passed through unchanged.**
- alternatives:
  - *Reject region-less national numbers* — v0.1.0 has shipped and callers pass
    them today. It would break working integrations to prevent a mistake the
    caller can already avoid, and the numbers it would break are legitimate:
    domestic printing is a real use, and short codes (why `phoneMinDigits` is 3)
    are national by definition.
  - *Default the region from configuration or a locale* — barqr is stateless,
    has no `Accept-Language` contract and no geo-IP. Any default is a guess that
    converts a *visibly* local number into an *invisibly* wrong one: E.164,
    plausible, and dialling the wrong country. Strictly worse than the status quo.
  - *Normalise with a small in-repo calling-code table* — half the feature.
    Rejecting what cannot be dialled is the part that prevents the reported
    failure, and that needs per-region validity, not a prefix table.
- consequences: the image grows 2.7 MB for libphonenumber's metadata, and
  `MAX_IMAGE_BYTES` rises from 20 to 24 MB. That is a deliberate reversal of
  ADR-009's "no dependency for PDF" instinct, and the distinction is worth
  stating: hand-writing a PDF is bounded work that stays correct, whereas
  hand-writing the world's dialling rules is unbounded work that is wrong
  somewhere from the first day. `Parse` never recovers `phone_region` — the
  built string is already international, so a region would be ignored on rebuild
  and claiming one would break ADR-008's round trip. Opt-in strictness leaves
  room for a future `BARQR_REQUIRE_E164`; that would supersede this ADR rather
  than quietly contradict it. The field is `phone_region`, not `region`, because
  `vcard` already has a postal `region` and the collision would have been silent.

## ADR-016 — One type family per generic name, honoured by every format (2026-08-19)

- status: accepted
- context: the human-readable line under a linear code was unreadable for
  alphanumeric payloads — `0` and `O` differed by three pixels, glyphs touched —
  and there was no way to make it bigger or change its face. Adding
  `style.hri_font` ran into a real asymmetry: the raster path had exactly one
  embedded bitmap face and no font loader, while SVG, PDF and EPS hand text to a
  font engine.
- decision: `style.hri_font` is a closed enum of generic families — `mono` and
  `sans` — each mapping to a genuinely different real face in *every* output
  format. The raster path gained a second face, converted at init from
  `x/image/font/basicfont`, which was already a dependency.
- alternatives:
  - *Capability-gate it* — accept the family for the formats that can draw it
    and return 400 for the rest. Rejected because `output.format` is the option
    people vary last: the same code, PNG for the web and PDF for print. A gate
    would fire at exactly that moment, and the request that worked yesterday
    fails today for a reason that names an unrelated field.
  - *Accept a font name* — an unvalidated pass-through into a `font-family` and
    a `/BaseFont`, silently substituted by whatever is installed. The same
    accepted-and-ignored bug one layer down.
  - *Offer `serif` too* — free on the vector paths, impossible in a bitmap cell
    this small. Three families where one is a lie is worse than two that are not.
- consequences: the default PDF and EPS face moves from Helvetica to Courier — a
  visible change, restored with `hri_font=sans` — because a printed code's HRI
  is monospaced and it is the only default the three paths agree on. Holding the
  *borrowed* face to the same legibility bar as the hand-drawn one found that it
  had the same defect, and four of its glyphs are redrawn; that test runs over
  every registered face precisely so a face nobody typed cannot smuggle the bug
  back in. Adding a family now means adding a real face to the rasteriser, and a
  test fails if the enum and the face table disagree.

## ADR-015 — A registry outage is waited out, never engineered around (2026-08-17)

- status: accepted
- context: during the `v0.1.0` release, GitHub was in a Partial System Outage with
  Actions in major outage and ~20% error rates on web and API traffic. Every CI
  failure was a `429`/`503` from `codeload.github.com` while downloading the
  SHA-pinned actions, inside `Set up job` — before any project code was evaluated.
  Six consecutive runs failed this way over ~35 minutes. The obvious "fixes" were
  all available: unpin the action SHAs to mutable tags, vendor the actions into the
  repo, or wrap each `uses:` in a retry shim.
- decision: change nothing in the repository in response to a provider incident.
  Retry the runs, classify each failure as transient-or-real (so a genuine defect
  is never buried under retries), and wait. Action SHAs stay pinned. The only
  in-repo change permitted during the incident was an unrelated, independently
  justified defect found on the way (`codeql.yml` had no concurrency group).
- alternatives:
  - *Unpin to mutable tags* — trades a two-hour incident for a permanent
    supply-chain hole, since a tag can be repointed at malicious code by a
    compromised maintainer. This is exactly what SHA-pinning exists to prevent.
  - *Vendor the actions* — moves a third-party update problem into the repo and
    silently freezes security fixes to the actions themselves.
  - *Retry shims per `uses:`* — permanent complexity in every workflow to paper
    over a transient condition; also cannot help, because the failure is in the
    runner's own action-fetch phase, ahead of the first step.
  - *Hand-push the image from a workstation to beat the outage* — rejected
    outright: it would arrive unsigned, un-attested and SBOM-less, while
    `docs/DEPLOY.md` instructs users to `cosign verify` it. Shipping an artifact
    that fails your own documented verification is worse than shipping late.
- consequences: releases are hostage to provider availability, and that is
  accepted — `barqr` has no release deadline that outranks its supply-chain
  guarantees. The operational cost is paid in the runbook instead: the
  Troubleshooting table names this failure signature so the next person recognises
  it in seconds rather than debugging their own workflow. Corollary: a failure in
  `Set up job` is *never* a project defect, and that boundary is the cheapest
  triage rule available.

## ADR-014 — Documentation is checked against the code, not maintained beside it (2026-08-17)

- status: accepted
- context: every list in a README rots. The capability tables, the environment
  reference, and the error catalogue are the three most-read parts of this
  project's documentation and the three most likely to fall behind the binary,
  because nothing fails when they do.
- decision: two mechanisms, meeting in the middle. What the *service* serves —
  `/v1/symbologies`, `/v1/openapi.json`, and every page of the browser
  documentation — is generated from the same registries the router dispatches
  on, so it cannot describe an endpoint or format this build does not have.
  What is *written* — `README.md`, `docs/API.md`, `docs/DEPLOY.md`,
  `docs/DOCKERHUB.md` — is parsed by `internal/doccheck` and asserted against
  those registries, `config.Keys()`, and `httpapi.Codes()`. Adding a symbology
  without listing it fails the build; documenting a variable the binary does
  not read fails the build.
- alternatives: generate the markdown too (a README wants prose and judgement,
  not a dump); a review checklist (this is precisely what review forgets).
- consequences: the counts in the README headline are load-bearing and a test
  will tell you when they are wrong. The cost is that a deliberate wording
  change to a table can fail the build, which is the intended trade.

## ADR-013 — Three deliberate deviations from the project brief (2026-08-17)

- status: accepted
- context: the brief specifies a repository layout and a testing approach. Three
  items were built differently, and an unexplained absence looks like an
  oversight rather than a decision.
- decision and reasoning:
  - **`api/openapi.yaml` is not committed.** The document is generated at
    request time from the live registries and served at `/v1/openapi.json`. A
    committed snapshot would be a second source of truth that can disagree with
    the running service, which is the failure the generation exists to prevent.
    A release artefact can be produced from a running container when one is
    genuinely needed.
  - **No `testdata/golden` byte-exact fixtures.** The writers assert properties
    of their output instead: the PDF writer parses its own xref table and checks
    each offset lands on an object header, the SVG writer parses with
    `encoding/xml`, the rasteriser compares every pixel against `Canvas.At`, and
    several tests decode the rendered image back with `internal/decoder` to
    prove it still scans. A golden file proves "unchanged"; these prove
    "correct", and they do not have the failure mode where a regenerated blob
    nobody looked at silently becomes the new truth.
  - **`internal/dynamic` is not implemented.** It is M10 and explicitly
    optional. `BARQR_DYNAMIC` parses and warns at boot that the module is
    absent, rather than accepting the setting silently.
- consequences: someone comparing the tree to the brief finds three gaps. They
  are recorded here and in `.claude/PROGRESS.md` so the answer is one file away.
  If a visual regression ever slips past the structural assertions, golden files
  are the right response and this ADR should be superseded.


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
