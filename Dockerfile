# syntax=docker/dockerfile:1

# --- build ------------------------------------------------------------------
FROM golang:1.26-alpine AS build

WORKDIR /src

# Dependencies resolve in their own layer so that source-only edits do not
# re-download the module graph.
COPY go.mod go.sum ./
RUN go mod download

COPY . .

ARG VERSION=dev
ARG COMMIT=none
ARG DATE=unknown
# SOURCE_DATE_EPOCH is honoured by callers that need byte-identical rebuilds.
ARG SOURCE_DATE_EPOCH

# CGO_ENABLED=0 keeps the binary static so it can run on a distroless base
# with no libc; -trimpath strips absolute build paths for reproducibility.
RUN CGO_ENABLED=0 GOOS=linux go build \
      -trimpath \
      -ldflags "-s -w \
        -X github.com/el-amin-dev/barqr/internal/version.Version=${VERSION} \
        -X github.com/el-amin-dev/barqr/internal/version.Commit=${COMMIT} \
        -X github.com/el-amin-dev/barqr/internal/version.Date=${DATE}" \
      -o /barqr ./cmd/barqr

# --- runtime ----------------------------------------------------------------
FROM gcr.io/distroless/static-debian12:nonroot

ARG VERSION=dev
ARG COMMIT=none
ARG DATE=unknown

LABEL org.opencontainers.image.title="barqr" \
      org.opencontainers.image.description="QR codes and barcodes over HTTP. One stateless container." \
      org.opencontainers.image.source="https://github.com/el-amin-dev/barqr" \
      org.opencontainers.image.url="https://github.com/el-amin-dev/barqr" \
      org.opencontainers.image.documentation="https://github.com/el-amin-dev/barqr/blob/main/docs/API.md" \
      org.opencontainers.image.licenses="Apache-2.0" \
      org.opencontainers.image.version="${VERSION}" \
      org.opencontainers.image.revision="${COMMIT}" \
      org.opencontainers.image.created="${DATE}"

COPY --from=build /barqr /barqr

# No shell, no package manager, no writable root filesystem needed: barqr is
# stateless and logs to stdout.
USER 65532:65532
EXPOSE 3000

# distroless has no shell, so HEALTHCHECK cannot run here. Use the HTTP probes
# instead: /v1/healthz for liveness, /v1/readyz for readiness. The Kubernetes
# manifests in deploy/k8s wire them up.
ENTRYPOINT ["/barqr", "serve"]
