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

Remote fetching of logos is **off by default** (`BARQR_ALLOW_REMOTE_FETCH=false`),
and `style.logo` accepts only `data:` URIs until it is switched on. ✅

`style.logo` naming a URL makes barqr dereference a destination an untrusted
caller chose, which is a request-forgery primitive: the caller picks the target,
and the server brings a routing table, a position inside the network perimeter,
and often an identity the caller does not have. The guards are therefore
deny-by-default and layered, all in `internal/fetch`: ✅

| Guard | Why |
|---|---|
| `https` only | no `file:`, `gopher:`, or plaintext port is reachable |
| Exact host allowlist, empty by default | nothing is fetchable until an operator names it; `evil-cdn.example` does not match `cdn.example` |
| Resolve, vet, then dial **the vetted address** | closes the DNS-rebinding window between the check and the connection |
| Reject loopback, private, link-local, unique-local, multicast and unspecified addresses, including IPv4-mapped forms | the whole point: the caller must not reach inside the perimeter |
| No redirects at all | following one means re-running every check on a new target; refusing is far easier to get right |
| `BARQR_FETCH_MAX_BYTES`, checked twice | on `Content-Length` before reading, and again through a `LimitReader`, because a host can lie or omit it |
| `BARQR_FETCH_TIMEOUT` | a host that answers slowly is a denial-of-service amplifier |
| Sniffed content type must be `image/*` | regardless of what the server claimed |

Errors returned to the caller never carry the underlying network error or the
resolved address — that address is precisely what these guards exist to keep
from them.

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
