# Deployment

barqr is a single stateless binary in an 18.5 MB distroless image. It needs a port and
some environment variables. It does not need a database, a volume, a sidecar, or a
config file.

Read [`SECURITY.md`](SECURITY.md) before exposing it to anything.

---

## Configuration reference

Configuration comes from the environment **only** — twelve-factor III. There are no
config files and no flags that change behaviour.

Two rules:

- **An invalid value is fatal.** barqr never silently falls back to a default, because
  a silently ignored `BARQR_AUTH_MODE` is a security incident waiting to happen. All
  problems are reported in one boot, not one per restart.
- **An unrecognised `BARQR_*` variable warns.** A typo like `BARQR_API_KEY` (singular)
  shows up in the boot log instead of quietly leaving the service unauthenticated.

### Network

| Variable | Default | Notes |
|---|---|---|
| `BARQR_BIND` | `127.0.0.1` | Listen address. Inside a container, `0.0.0.0` binds the container's namespace, not the host — combine with `expose:`/`ClusterIP`. |
| `BARQR_PORT` | `3000` | 1–65535. |

### Authentication

| Variable | Default | Notes |
|---|---|---|
| `BARQR_AUTH_MODE` | `required` | `required` \| `open`. |
| `BARQR_API_KEYS` | *(empty)* | Comma-separated. SHA-256 hashed at boot; plaintext is discarded and never logged or printed. |
| `BARQR_I_UNDERSTAND_OPEN_BIND` | `false` | Required acknowledgement to run `open` on a wildcard bind. |

### Limits

| Variable | Default | Notes |
|---|---|---|
| `BARQR_MAX_BODY` | `2MB` | Accepts `2MB`, `512KB`, `1GiB`, or a bare byte count. Binary multiples. |
| `BARQR_REQUEST_TIMEOUT` | `10s` | Go duration syntax. |
| `BARQR_RATE_LIMIT` | `120/min` | `<count>/<s\|min\|hour>`. A count of `0` disables limiting. |
| `BARQR_CONCURRENCY` | `8` | Worker semaphore per process. |
| `BARQR_SHUTDOWN_GRACE` | `15s` | Drain window after `SIGTERM`. |
| `BARQR_MAX_CANVAS_PX` | `25000000` | Render size cap, in pixels. |
| `BARQR_MAX_BATCH_ITEMS` | `1000` | Batch cap. |

### Rendering defaults

| Variable | Default | Notes |
|---|---|---|
| `BARQR_DEFAULT_FORMAT` | `png` | One of `png svg jpeg webp pdf ascii unicode ansi json datauri txt`. |
| `BARQR_DEFAULT_ECC` | `M` | `L` \| `M` \| `Q` \| `H`. |
| `BARQR_STRICT_SCANNABILITY` | `warn` | `off` \| `warn` \| `strict`. |

### Egress

| Variable | Default | Notes |
|---|---|---|
| `BARQR_ALLOW_REMOTE_FETCH` | `false` | Lets `style.logo` name an `https` URL. This is the SSRF surface — leave it off unless you need it. |
| `BARQR_FETCH_ALLOWLIST` | *(empty)* | Comma-separated hosts, matched exactly. Empty means **nothing** is fetchable, so enabling the feature without setting this warns at boot. |
| `BARQR_FETCH_TIMEOUT` | `3s` | Bounds the whole fetch. |
| `BARQR_FETCH_MAX_BYTES` | `2MB` | Checked against `Content-Length` and again while reading. |

> **Egress must be direct.** `HTTPS_PROXY` is deliberately ignored: barqr
> resolves the host, vets every address, and dials the vetted address, and a
> proxy would void that pin. If your egress goes through a proxy, allow direct
> egress to the allowlisted hosts instead, or remote logos will simply fail.

### Observability and behaviour

| Variable | Default | Notes |
|---|---|---|
| `BARQR_CORS_ORIGINS` | *(empty)* | Comma-separated. Empty means no CORS headers. |
| `BARQR_METRICS` | `true` | Prometheus endpoint. |
| `BARQR_LOG_LEVEL` | `info` | `debug` \| `info` \| `warn` \| `error`. |
| `BARQR_LOG_FORMAT` | `json` | `json` \| `text`. JSON to stdout; never to a file. |
| `BARQR_PRESETS_PATH` | *(empty)* | Preset bundle location. |
| `BARQR_DOCS` | `true` | The browser documentation at `/` and `/v1/docs`. |
| `BARQR_DYNAMIC` | `false` | Opt-in dynamic-code module. **Not implemented in this build** — setting it warns at boot. |

### Not honoured by this build

One documented variable parses and validates but has no feature behind it yet.
Rather than accept it silently, barqr warns at boot:

| Variable | What happens |
|---|---|
| `BARQR_DYNAMIC` | Warns at boot. `/v1/dynamic` is not routed. |

With `BARQR_ALLOW_REMOTE_FETCH=false` (the default), `style.logo` accepts only
`data:` URIs and a remote reference is refused with `501 UNSUPPORTED_OPTION`
rather than ignored.

---

## Validating a deployment before it takes traffic

```bash
docker run --rm --env-file prod.env barqr:dev check-config
```

Exits `0` and prints the effective configuration with secrets redacted, or exits `1`
listing every problem. This is the twelve-factor admin process: run it in an init
container, or as a CI job against your production env file, and catch a bad deploy
before it is live.

---

## Docker Compose

A ready-made file is in [`deploy/docker-compose.yml`](../deploy/docker-compose.yml),
with the reasoning for every line inline. The shape, abridged:

```yaml
services:
  app:
    image: your-app:latest      # whatever calls barqr — replace this
    environment:
      BARQR_URL: http://barqr:3000
      BARQR_KEY: ${BARQR_KEY:?set BARQR_KEY}
    depends_on: [barqr]

  barqr:
    image: barqr:dev
    expose: ["3000"]            # NOT ports: — see below
    environment:
      BARQR_BIND: 0.0.0.0
      BARQR_API_KEYS: ${BARQR_KEY:?set BARQR_KEY}
      BARQR_RATE_LIMIT: 600/min
      BARQR_CONCURRENCY: 8
      BARQR_MAX_CANVAS_PX: 4000000
      BARQR_SHUTDOWN_GRACE: 20s
    user: "65532:65532"
    read_only: true
    cap_drop: [ALL]
    security_opt: ["no-new-privileges:true"]
    restart: unless-stopped
    stop_grace_period: 30s
```

`expose:` publishes the port to sibling containers but not to the host — this is the
intended shape, and [`SECURITY.md`](SECURITY.md#layer-1--network) explains why `ports:`
is not a smaller version of it.

Three of those lines are easy to leave out and each one costs something:

- **`user: "65532:65532"`.** The image already declares it, but repeating it here means
  a `docker compose run --user root` or a future base-image change cannot quietly hand
  the process root.
- **`stop_grace_period: 30s` paired with `BARQR_SHUTDOWN_GRACE: 20s`.** On `SIGTERM`
  barqr answers `/v1/readyz` with `503` and then drains in-flight requests for up to
  the grace window. Compose SIGKILLs at `stop_grace_period`, which **defaults to 10
  seconds** — shorter than even the default 15s drain, so leaving it out cuts live
  requests on every `compose up` of a new image. The platform's kill deadline must be
  strictly greater than barqr's drain window, always, on both platforms.
- **`BARQR_MAX_CANVAS_PX: 4000000`.** See the note under the Kubernetes manifest below;
  the default and a small memory ceiling do not go together.

---

## Kubernetes

Applyable manifests — Namespace, Deployment, PodDisruptionBudget, Service and
NetworkPolicy — are in [`deploy/k8s`](../deploy/k8s), tied together by a
`kustomization.yaml` and commented line by line. `kubectl apply -k deploy/k8s`.
The Deployment, abridged to the parts an operator has to get right:

```yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: barqr
spec:
  replicas: 3                     # stateless: scale flat, no coordination
  strategy:
    rollingUpdate: { maxUnavailable: 0, maxSurge: 1 }
  template:
    spec:
      automountServiceAccountToken: false   # barqr never calls the API server
      terminationGracePeriodSeconds: 30     # > BARQR_SHUTDOWN_GRACE, always
      securityContext:
        runAsNonRoot: true
        runAsUser: 65532
        runAsGroup: 65532
        seccompProfile: { type: RuntimeDefault }
      containers:
        - name: barqr
          image: barqr:dev
          ports: [{ name: http, containerPort: 3000 }]
          env:
            - name: BARQR_BIND
              value: "0.0.0.0"
            - name: BARQR_API_KEYS
              valueFrom:
                secretKeyRef: { name: barqr-api-keys, key: api-keys, optional: false }
            - name: BARQR_CONCURRENCY
              value: "8"
            - name: BARQR_MAX_CANVAS_PX     # NOT the 25000000 default — see below
              value: "4000000"
            - name: BARQR_SHUTDOWN_GRACE
              value: "20s"
            - name: GOMEMLIMIT              # ~80% of limits.memory
              value: "200MiB"
          startupProbe:                     # gates the other two on a cold pull
            httpGet: { path: /v1/healthz, port: http }
            periodSeconds: 1
            failureThreshold: 30
          livenessProbe:
            httpGet: { path: /v1/healthz, port: http }
          readinessProbe:
            httpGet: { path: /v1/readyz, port: http }
          resources:
            requests: { cpu: 100m, memory: 96Mi }
            limits:   { memory: 256Mi }     # no cpu limit — see Scaling
          securityContext:
            runAsNonRoot: true
            runAsUser: 65532
            runAsGroup: 65532
            readOnlyRootFilesystem: true
            allowPrivilegeEscalation: false
            capabilities: { drop: [ALL] }
            seccompProfile: { type: RuntimeDefault }
```

The pod-level `securityContext` is inherited, but the container repeats it: a container
entry can override the pod's, so the guarantee is written where the container is. That
also makes the manifest admissible under Pod Security Admission `restricted`, which
[`deploy/k8s/namespace.yaml`](../deploy/k8s/namespace.yaml) enforces.

Expose it as a `ClusterIP` Service with a `NetworkPolicy` allowing ingress only from
labelled pods. **Do not ship an `Ingress` object.**

### The canvas cap and the memory limit are one setting

`BARQR_MAX_CANVAS_PX` defaults to `25000000`. At four bytes per pixel that is a
**100 MB buffer for a single request**, and `BARQR_CONCURRENCY` of them in flight is
800 MB — an OOMKill under any limit a small deployment would write. The two numbers
have to be chosen together:

| | Per render | Across the semaphore (8) |
|---|---|---|
| `BARQR_MAX_CANVAS_PX=25000000` (default) | ~100 MB | ~800 MB |
| `BARQR_MAX_CANVAS_PX=4000000` (the manifests) | ~16 MB | ~128 MB |

128 MB of pixel buffers plus the Go runtime and the encode buffer is what the `256Mi`
limit above is sized for. `GOMEMLIMIT` is the second half of it: it is a Go runtime
variable, not a barqr one, and it tells the collector to hold the heap under a soft
ceiling below the cgroup limit, so pressure becomes more GC rather than an OOMKill —
the kernel gives Go no warning and no chance to shed load. Set it to roughly 80% of
`limits.memory`. Raise the pixel cap and you must raise the limit and `GOMEMLIMIT`
with it, or not raise it at all.

### Rollouts

`/v1/readyz` returns `503` as soon as `SIGTERM` arrives and *before* connections are
cut, then the server drains for up to `BARQR_SHUTDOWN_GRACE`. Set
`terminationGracePeriodSeconds` strictly above that value — it must cover endpoint
propagation across every kube-proxy plus the whole drain window — or the kubelet
`SIGKILL`s mid-drain and the drain window is a lie. Raise the platform deadline
*before* raising the grace, never after.

`maxUnavailable: 0` means capacity never dips during a rollout: a new pod must pass
readiness before an old one is asked to stop.

### Scraping metrics

**`/metrics` sits behind the API key.** Only `/v1/healthz`, `/v1/readyz` and
`/v1/version` are unauthenticated, because a kubelet cannot hold a key. The reflexive
`prometheus.io/scrape: "true"` pod annotation and nothing else therefore collects a
steady stream of `401`s and no metrics at all. Give the scrape job the same Secret.
barqr accepts `Authorization: Bearer <key>` as well as `X-API-Key`, which is the form
Prometheus already speaks:

```yaml
scrape_configs:
  - job_name: barqr
    authorization:
      type: Bearer
      credentials_file: /etc/prometheus/secrets/barqr/api-keys
```

Under the Prometheus Operator the same thing is `spec.authorization` on a
`ServiceMonitor`, with the Secret mounted into the Prometheus pod.
`BARQR_METRICS=false` turns the endpoint off entirely.

### Scaling

Every replica is identical and holds no state — no sticky sessions, no shared volume,
no leader election. Scale on CPU or on request rate. Inside each process,
`BARQR_CONCURRENCY` bounds the worker semaphore so a burst queues rather than thrashes,
and `BARQR_REQUEST_TIMEOUT` bounds how long a queued request waits before it is shed.
Boot is under 100 ms, so a scale-up is useful immediately.

---

## Building the image

```bash
make docker-build                       # tags barqr:dev
make smoke                              # start it and exercise it over HTTP
```

The build is reproducible: `-trimpath`, no embedded timestamps, and `SOURCE_DATE_EPOCH`
honoured. Version metadata is injected at link time:

```bash
make docker-build VERSION=v1.2.3 IMAGE=ghcr.io/el-amin-dev/barqr IMAGE_TAG=v1.2.3
```

Multi-arch images (`linux/amd64`, `linux/arm64`) with SBOM, provenance, and cosign
keyless signatures are published by the release workflow; every green push to `main`
publishes `edge` to GHCR.
