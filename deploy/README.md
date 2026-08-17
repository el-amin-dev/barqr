# Deploying barqr

Ready-made manifests for the two ways barqr is meant to run. Both implement the
posture in [`../docs/SECURITY.md`](../docs/SECURITY.md); the configuration
reference is in [`../docs/DEPLOY.md`](../docs/DEPLOY.md).

| File | What it is |
|---|---|
| `docker-compose.yml` | Two-container stack: barqr plus a stand-in consumer, on a private network. |
| `k8s/namespace.yaml` | Namespace `barqr`, with Pod Security Admission set to `restricted`. |
| `k8s/deployment.yaml` | Deployment (3 replicas) and the PodDisruptionBudget. |
| `k8s/service.yaml` | `ClusterIP` Service on port 3000. No Ingress — see the note in the file. |
| `k8s/networkpolicy.yaml` | Ingress limited to pods labelled `barqr-client=true`; egress denied entirely. |
| `k8s/secret.example.yaml` | Template for the API-key Secret. Not applied, never filled in here. |
| `k8s/kustomization.yaml` | Ties the four applied manifests together and sets the image and labels. |

## Neither stack exposes barqr, and that is the point

Compose uses `expose:` and not `ports:`; Kubernetes gets a `ClusterIP` and no
`Ingress`, `NodePort`, or `LoadBalancer`. barqr is reachable by the workloads
you name and by nothing else.

This is not caution for its own sake. barqr has no tenancy model, no quotas,
and no abuse protection beyond a per-key rate limiter — so the design goal is
not "survive the open internet", it is "never end up on the open internet by
accident". If you need public access, that decision belongs in your own
infrastructure, behind an authenticating gateway, not in a file copied from
here.

Both files carry the reasoning inline. Read them before changing them; the
comments explain what each line is holding up.

## Docker Compose

```bash
export BARQR_KEY="$(openssl rand -base64 32)"
docker compose -f deploy/docker-compose.yml up -d
```

The key is required: `${BARQR_KEY:?}` fails the command with a message rather
than starting the service unauthenticated. It has to be in the environment for
*every* compose subcommand, `down` and `logs` included, because Compose
re-interpolates the file each time. Put it in `deploy/.env` instead — Compose
loads that automatically, and `.env` is already gitignored:

```bash
printf 'BARQR_KEY=%s\n' "$(openssl rand -base64 32)" > deploy/.env
```

Check it from the consumer container, which is the only thing that can reach it:

```bash
docker compose -f deploy/docker-compose.yml exec app \
  sh -c 'curl -sS -H "X-API-Key: $BARQR_KEY" "$BARQR_URL/qr?data=hello" -o /tmp/qr.png && ls -l /tmp/qr.png'

docker compose -f deploy/docker-compose.yml down
```

## Kubernetes

Create the Secret first — it is deliberately not part of the kustomization, so
that no placeholder key can ever be applied as a real one:

```bash
kubectl create namespace barqr
kubectl -n barqr create secret generic barqr-api-keys \
  --from-literal=api-keys="$(openssl rand -base64 32)"
```

Then apply the stack:

```bash
kubectl apply -k deploy/k8s
kubectl -n barqr rollout status deployment/barqr
```

Callers need the label the NetworkPolicy selects on, in their own pod template
or by hand:

```bash
kubectl -n barqr label pod/<caller> barqr-client=true
```

To reach it from a laptop, port-forward rather than publishing anything:

```bash
kubectl -n barqr port-forward svc/barqr 3000:3000
```

Remove it with `kubectl delete -k deploy/k8s`; the Secret is not part of the
kustomization, so delete it separately if you want it gone.

## Before it takes traffic

```bash
docker run --rm --env-file prod.env barqr:dev check-config
```

Exits `0` printing the effective configuration with the keys redacted, or `1`
listing every problem — an init container or a CI job's worth of work that
catches a bad deploy while it is still cheap.

## Two settings that move together

Both files change the defaults in the same two places, and each pair has to
stay consistent:

- **`BARQR_SHUTDOWN_GRACE` (20s) < the platform's kill deadline** —
  `terminationGracePeriodSeconds: 30` in Kubernetes, `stop_grace_period: 30s`
  in Compose. barqr answers `/v1/readyz` with `503` on `SIGTERM` and then
  drains; if the platform kills it first, the drain window is a lie and every
  rollout drops requests. Raise the platform value before raising the grace.
- **`BARQR_MAX_CANVAS_PX` (4,000,000) vs the memory limit** — the 25 MP default
  lets one request ask for a 100 MB pixel buffer, and `BARQR_CONCURRENCY`
  copies of that is an OOMKill. 4 MP is 16 MB per render, 128 MB across the
  semaphore, inside the 256Mi limit. Raise them together or not at all.
