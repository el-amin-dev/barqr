# Security model

## Threat model

**The container must remain safe even if someone learns its host and port.** Only the
intended runner may use it. barqr has no tenancy model, no quota system, and no abuse
protection beyond rate limiting — so the design goal is not "survive the open
internet", it is "never end up on the open internet by accident".

Where a control can be enforced by the program instead of documented in a runbook, it
is enforced by the program. Two configurations are startup-fatal for exactly this
reason.

Status legend: ✅ implemented · ⚠️ implemented but dependent on something outside barqr.

---

## Layer 1 — network

| Control | Status |
|---|---|
| Default bind `127.0.0.1:3000` — unreachable from another host unless deliberately changed | ✅ |
| Compose uses `expose:`, never `ports:` — reachable by sibling containers only | ✅ [`deploy/docker-compose.yml`](../deploy/docker-compose.yml) |
| Kubernetes `ClusterIP` + `NetworkPolicy` restricting ingress to labelled pods | ⚠️ [`deploy/k8s`](../deploy/k8s) — enforcement depends on the CNI |
| No `Ingress` object is shipped, ever | ✅ by omission |

### `expose:` rather than `ports:` — the reason, not the preference

`ports: ["3000:3000"]` does not merely publish a port. Docker writes its own iptables
rules into the `DOCKER` chain, which is reached from `PREROUTING`/`FORWARD` **ahead of
the host's `INPUT` chain** — so a published port binds `0.0.0.0` on the host and is
*not* filtered by ufw or firewalld. An operator who checks `ufw status` sees the
firewall they configured and does not see the hole. On a machine with a public
interface that puts a QR renderer on the open internet behind nothing but an API key.

That is the argument that has to stop someone adding `ports:` "temporarily". If host
access is genuinely needed for debugging, bind the loopback explicitly —
`ports: ["127.0.0.1:3000:3000"]` — and remove it again.

### A NetworkPolicy is only as real as the CNI

`deploy/k8s/networkpolicy.yaml` restricts ingress to pods labelled
`barqr-client=true` and denies egress entirely. **A NetworkPolicy is enforced by the
CNI plugin, not by the API server**, and an unenforced one is accepted, stored, and
shown by `kubectl get netpol` exactly like an enforced one. Calico, Cilium, Antrea,
Weave and the managed offerings built on them enforce it; **stock Flannel and kindnet
do not**, and there the manifest is inert — every pod in the cluster can still reach
barqr and barqr can still reach out. Confirm your cluster's plugin enforces policy
before counting this as a control; if it does not, the ClusterIP and the API key are
the only two things left.

---

## Layer 2 — authentication

| Control | Status |
|---|---|
| `BARQR_AUTH_MODE` = `required` (default) or `open` | ✅ |
| `X-API-Key: <key>` or `Authorization: Bearer <key>` | ✅ |
| Keys SHA-256 hashed at boot; plaintext never stored on the config struct | ✅ |
| Comparison via `subtle.ConstantTimeCompare`, evaluated against *every* key so neither the outcome nor which key matched leaks through timing | ✅ |
| Exactly three endpoints are unauthenticated — `/v1/healthz`, `/v1/readyz`, `/v1/version`. `/metrics` is **not** among them | ✅ |
| Failed authentication is itself rate-limited per source address, by a limiter that runs *before* the auth check — otherwise a `401` returns without ever reaching it, and every key-checking endpoint is an unthrottled guessing oracle. A valid key costs nothing, so a busy legitimate caller is never throttled by it | ✅ |
| `X-Forwarded-For` is deliberately ignored when identifying a caller: barqr does not know whether it sits behind a trusted proxy, and trusting a client-settable header would let anyone reset their own limit | ✅ |
| `Cache-Control: private` on responses when auth is on | ✅ |

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
| Request body limit | `BARQR_MAX_BODY=2MB` | ✅ |
| Request timeout | `BARQR_REQUEST_TIMEOUT=10s` | ✅ |
| Per-key rate limit | `BARQR_RATE_LIMIT=120/min` | ✅ |
| Global concurrency semaphore | `BARQR_CONCURRENCY=8` | ✅ |
| Render canvas pixel cap | `BARQR_MAX_CANVAS_PX=25000000` | ✅ |
| Batch item cap | `BARQR_MAX_BATCH_ITEMS=1000` | ✅ |
| Decode pixel-count cap and decompression-bomb guard | — | ✅ |

The canvas cap's default is sized for a machine with memory to spare, not for a small
container: 25 MP is a ~100 MB buffer per render, and `BARQR_CONCURRENCY` of those is an
OOMKill under a modest limit. Both shipped deployments lower it to 4,000,000 and pair
it with an explicit memory ceiling — see
[`DEPLOY.md`](DEPLOY.md#the-canvas-cap-and-the-memory-limit-are-one-setting).

Decode guards run on the image *header*, before a single pixel is decoded: a
hundred-byte PNG can declare fifty thousand pixels square, and any cap on the decoded
size fires ten gigabytes too late. The decode caps cannot be switched off — a zero or
negative value normalises to the default rather than meaning "unlimited". See
[ADR-012](DECISIONS.md).

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

No proxy is honoured: `HTTPS_PROXY` is deliberately ignored, because routing
through a proxy would void the resolve-then-dial address pin. The **sniffed**
content type — not the one the server claimed — is what reaches the renderer.

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
| `cap_drop: ALL`, `no-new-privileges` | ✅ [`deploy/`](../deploy) |
| SBOM, provenance, cosign keyless signing, Trivy gate on HIGH/CRITICAL | ✅ `.github/workflows/release.yml` |
| Pod Security Admission `restricted`, `automountServiceAccountToken: false`, container-level `seccompProfile` | ✅ [`deploy/k8s`](../deploy/k8s) |

---

## Logging and secrets

Payloads routinely contain **Wi-Fi passwords and TOTP secrets**. Therefore:

- payload contents are never logged at `info` level; ✅
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
