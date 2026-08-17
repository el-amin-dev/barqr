# Security model

## Threat model

**The container must remain safe even if someone learns its host and port.** Only the
intended runner may use it. barqr has no tenancy model, no quota system, and no abuse
protection beyond rate limiting — so the design goal is not "survive the open
internet", it is "never end up on the open internet by accident".

Where a control can be enforced by the program instead of documented in a runbook, it
is enforced by the program. Two configurations are startup-fatal for exactly this
reason.

Status legend: ✅ implemented · ⏳ scheduled for the milestone shown.

---

## Layer 1 — network

| Control | Status |
|---|---|
| Default bind `127.0.0.1:3000` — unreachable from another host unless deliberately changed | ✅ |
| Compose uses `expose:`, never `ports:` — reachable by sibling containers only | ✅ documented |
| Kubernetes `ClusterIP` + `NetworkPolicy` restricting ingress to labelled pods | ⏳ M9 manifests |
| No `Ingress` object is shipped, ever | ✅ by omission |

---

## Layer 2 — authentication

| Control | Status |
|---|---|
| `BARQR_AUTH_MODE` = `required` (default) or `open` | ✅ |
| `X-API-Key: <key>` or `Authorization: Bearer <key>` | ⏳ M2 middleware |
| Keys SHA-256 hashed at boot; plaintext never stored on the config struct | ✅ |
| Comparison via `subtle.ConstantTimeCompare`, evaluated against *every* key so neither the outcome nor which key matched leaks through timing | ✅ |
| `Cache-Control: private` on responses when auth is on | ⏳ M2 |

### Startup-fatal invariants ✅

barqr refuses to boot — `exit 1`, with an explicit message — in two cases:

1. **Exposed but unusable.** Bind is not loopback **and** `AUTH_MODE=required` **and**
   `BARQR_API_KEYS` is empty. The service would be reachable from the network yet
   configured to reject everything; far more often this means the operator forgot the
   keys than that they wanted a brick.

2. **Open on every interface.** `AUTH_MODE=open` **and** the bind is a wildcard
   (`0.0.0.0`, `::`, or empty), without `BARQR_I_UNDERSTAND_OPEN_BIND=true`. The
   acknowledgement variable exists so that the dangerous configuration is possible but
   never accidental.

```console
$ docker run --rm -e BARQR_BIND=0.0.0.0 barqr:dev
barqr: configuration error, refusing to start
  - insecure configuration: BARQR_BIND="0.0.0.0" is not loopback and
    BARQR_AUTH_MODE=required, but BARQR_API_KEYS is empty; set BARQR_API_KEYS to one
    or more keys, or bind to 127.0.0.1
$ echo $?
1
```

Both invariants are covered by tests in `internal/config/config_test.go` and by the
`make smoke` gate, which asserts the container actually exits non-zero. A regression
here fails the build, not a review.

---

## Layer 3 — input

| Control | Default | Status |
|---|---|---|
| Request body limit | `BARQR_MAX_BODY=2MB` | ⏳ M2 |
| Request timeout | `BARQR_REQUEST_TIMEOUT=10s` | ⏳ M2 |
| Per-key rate limit | `BARQR_RATE_LIMIT=120/min` | ⏳ M2 |
| Global concurrency semaphore | `BARQR_CONCURRENCY=8` | ⏳ M2 |
| Render canvas pixel cap | `BARQR_MAX_CANVAS_PX=25000000` | ⏳ M1 |
| Batch item cap | `BARQR_MAX_BATCH_ITEMS=1000` | ⏳ M8 |
| Decode pixel-count cap and decompression-bomb guard | — | ⏳ M7 |

Server-level deadlines are already in place: `ReadHeaderTimeout` (5s, Slowloris
defence), `ReadTimeout`, `WriteTimeout`, and `IdleTimeout` are all set on the
`http.Server` rather than left at Go's unlimited defaults. ✅

---

## Layer 4 — egress and SSRF

Remote fetching of logos and background images is **off by default**
(`BARQR_ALLOW_REMOTE_FETCH=false`). ✅

When enabled, the fetcher must (⏳ M5):

- accept `https` only;
- match the host against `BARQR_FETCH_ALLOWLIST` — an empty allowlist means nothing is
  fetchable, not everything;
- refuse redirects that resolve to private, loopback, or link-local addresses;
- pin the resolved IP and dial *that*, closing the DNS-rebinding window between the
  allowlist check and the connection;
- cap both size (`BARQR_FETCH_MAX_BYTES=2MB`) and time (`BARQR_FETCH_TIMEOUT=3s`).

---

## Layer 5 — runtime

| Control | Status |
|---|---|
| `gcr.io/distroless/static-debian12:nonroot` base — no shell, no package manager, no libc | ✅ |
| `USER 65532:65532` — asserted by `make smoke`, not merely written in the Dockerfile | ✅ |
| Static binary, `CGO_ENABLED=0` | ✅ |
| `-trimpath` — no absolute build paths in the binary | ✅ |
| Read-only root filesystem supported; barqr is stateless and writes nothing to disk, so no `tmpfs` is required | ✅ |
| `cap_drop: ALL`, `no-new-privileges` | ✅ documented |
| SBOM, provenance, cosign keyless signing, Trivy gate on HIGH/CRITICAL | ⏳ M9 |

---

## Logging and secrets

Payloads routinely contain **Wi-Fi passwords and TOTP secrets**. Therefore:

- payload contents are never logged at `info` level; ✅ policy, ⏳ M2 enforcement
- `password`, `secret`, and `pw` fields are redacted in every log record, always;
- `barqr check-config` and `serve --print-config` render `BARQR_API_KEYS` as
  `<redacted: N key(s)>`, so their output is safe to paste into a bug report — this is
  asserted by a test that fails if the plaintext key ever appears; ✅
- errors returned to clients never leak stack traces, file paths, environment values,
  or internal type names. ✅ policy

---

## Reporting a vulnerability

Open a [private security advisory](https://github.com/el-amin-dev/barqr/security/advisories/new)
on the repository. Please do not open a public issue for an exploitable finding.
