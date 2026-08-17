# Deployment

barqr is a single stateless binary in a 9.3 MB distroless image. It needs a port and
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
| `BARQR_FETCH_TIMEOUT` | `3s` | |
| `BARQR_FETCH_MAX_BYTES` | `2MB` | |

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

`expose:` publishes the port to sibling containers but not to the host — this is the
intended shape.

```yaml
services:
  app:
    build: .
    environment:
      BARQR_URL: http://barqr:3000
      BARQR_KEY: ${BARQR_KEY:?set BARQR_KEY}
    depends_on: [barqr]

  barqr:
    image: barqr:dev
    expose: ["3000"]
    environment:
      BARQR_BIND: 0.0.0.0
      BARQR_API_KEYS: ${BARQR_KEY:?set BARQR_KEY}
      BARQR_RATE_LIMIT: 600/min
      BARQR_CONCURRENCY: 16
    read_only: true
    cap_drop: [ALL]
    security_opt: ["no-new-privileges:true"]
    restart: unless-stopped
```

Ready-made files land in `deploy/docker-compose.yml` at M9.

---

## Kubernetes

```yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: barqr
spec:
  replicas: 3                     # stateless: scale flat, no coordination
  template:
    spec:
      securityContext:
        runAsNonRoot: true
        runAsUser: 65532
        seccompProfile: { type: RuntimeDefault }
      containers:
        - name: barqr
          image: barqr:dev
          ports: [{ containerPort: 3000 }]
          env:
            - name: BARQR_BIND
              value: "0.0.0.0"
            - name: BARQR_API_KEYS
              valueFrom:
                secretKeyRef: { name: barqr, key: api-keys }
          livenessProbe:
            httpGet: { path: /v1/healthz, port: 3000 }
          readinessProbe:
            httpGet: { path: /v1/readyz, port: 3000 }
          resources:
            requests: { cpu: 50m, memory: 32Mi }
            limits:   { memory: 128Mi }
          securityContext:
            readOnlyRootFilesystem: true
            allowPrivilegeEscalation: false
            capabilities: { drop: [ALL] }
```

Expose it as a `ClusterIP` Service with a `NetworkPolicy` allowing ingress only from
labelled pods. **Do not ship an `Ingress` object.** Manifests land in `deploy/k8s` at
M9.

### Rollouts

`/v1/readyz` returns `503` as soon as `SIGTERM` arrives and *before* connections are
cut, then the server drains for up to `BARQR_SHUTDOWN_GRACE`. Set
`terminationGracePeriodSeconds` above that value so Kubernetes does not `SIGKILL`
mid-drain.

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

Published multi-arch images (`linux/amd64`, `linux/arm64`) with SBOM, provenance, and
cosign signatures arrive with the release workflow at M9.
