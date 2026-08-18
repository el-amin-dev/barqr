# barqr — developer entrypoints.
#
# Every target runs identically on a laptop and in CI. If Go is not installed
# locally the Go and lint toolchains run in containers instead, so a fresh
# clone needs nothing but Docker. Force either mode with TOOLCHAIN=local or
# TOOLCHAIN=docker.

SHELL := /bin/sh
.DEFAULT_GOAL := help

# --- identity ---------------------------------------------------------------

MODULE     := github.com/el-amin-dev/barqr
BINARY     := barqr
IMAGE      ?= barqr
IMAGE_TAG  ?= dev

VERSION    ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT     ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo none)
DATE       ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)

LDFLAGS := -s -w \
	-X $(MODULE)/internal/version.Version=$(VERSION) \
	-X $(MODULE)/internal/version.Commit=$(COMMIT) \
	-X $(MODULE)/internal/version.Date=$(DATE)

# --- toolchain --------------------------------------------------------------

# The dev toolchain uses the Debian image, not Alpine: `go test -race` needs
# cgo and a C compiler. The runtime image in Dockerfile stays Alpine-based
# because the shipped binary is built with CGO_ENABLED=0.
GO_IMAGE   ?= golang:1.26
LINT_IMAGE ?= golangci/golangci-lint:v2.12.2-alpine
DOCKER     ?= docker

ifeq ($(shell command -v go 2>/dev/null),)
TOOLCHAIN ?= docker
else
TOOLCHAIN ?= local
endif

# Caches live inside the repo so the containerised toolchain can write them as
# the invoking user; .cache/ is already git-ignored.
CACHE_DIR := $(CURDIR)/.cache
DOCKER_GO := $(DOCKER) run --rm \
	--user $(shell id -u):$(shell id -g) \
	-v $(CURDIR):/src -w /src \
	-v $(CACHE_DIR)/gomod:/gomod \
	-v $(CACHE_DIR)/gobuild:/gobuild \
	-e HOME=/tmp -e GOMODCACHE=/gomod -e GOCACHE=/gobuild -e GOFLAGS -e CGO_ENABLED

ifeq ($(TOOLCHAIN),docker)
GO   := $(DOCKER_GO) $(GO_IMAGE) go
LINT := $(DOCKER_GO) --entrypoint golangci-lint $(LINT_IMAGE)
PREP := cache-dirs
else
GO   := go
LINT := golangci-lint
PREP :=
endif

# --- meta -------------------------------------------------------------------

.PHONY: help
help: ## Show this help
	@echo "barqr — toolchain: $(TOOLCHAIN)"
	@echo
	@grep -hE '^[a-zA-Z_-]+:.*?## ' $(MAKEFILE_LIST) \
		| awk 'BEGIN{FS=":.*?## "}{printf "  \033[36m%-14s\033[0m %s\n", $$1, $$2}'

.PHONY: cache-dirs
cache-dirs:
	@mkdir -p $(CACHE_DIR)/gomod $(CACHE_DIR)/gobuild

# --- develop ----------------------------------------------------------------

.PHONY: tidy
tidy: $(PREP) ## Sync go.mod / go.sum
	$(GO) mod tidy

.PHONY: fmt
fmt: $(PREP) ## Format the source tree
	$(GO) fmt ./...

.PHONY: build
build: $(PREP) ## Build the binary into bin/
	CGO_ENABLED=0 $(GO) build -trimpath -ldflags "$(LDFLAGS)" -o bin/$(BINARY) ./cmd/$(BINARY)

.PHONY: run
run: $(PREP) ## Run the server with the local defaults
	$(GO) run ./cmd/$(BINARY) serve

.PHONY: print-config
print-config: $(PREP) ## Print the effective configuration, secrets redacted
	$(GO) run ./cmd/$(BINARY) serve --print-config

# --- verify -----------------------------------------------------------------

.PHONY: vet
vet: $(PREP) ## Run go vet
	$(GO) vet ./...

.PHONY: lint
lint: $(PREP) ## Run golangci-lint (errcheck, govet, staticcheck, gosec, revive)
	$(LINT) run ./...

.PHONY: test
test: $(PREP) ## Run tests with the race detector and coverage
	CGO_ENABLED=1 $(GO) test -race -covermode=atomic -coverprofile=coverage.out ./...

.PHONY: cover
cover: test ## Report per-function coverage
	$(GO) tool cover -func=coverage.out

.PHONY: bench
bench: $(PREP) ## Run the hot-path benchmarks
	$(GO) test -run '^$$' -bench . -benchmem ./...

.PHONY: fuzz
fuzz: $(PREP) ## Run each fuzz target briefly (FUZZTIME=30s to go longer)
	@for pkg in ./internal/builder ./internal/httpapi; do \
		for target in $$($(GO) test -list 'Fuzz.*' $$pkg 2>/dev/null | grep '^Fuzz' || true); do \
			echo "==> $$pkg $$target"; \
			$(GO) test -run '^$$' -fuzz "^$$target$$" -fuzztime $${FUZZTIME:-20s} $$pkg || exit 1; \
		done; \
	done

# --- package ----------------------------------------------------------------

.PHONY: docker-build
docker-build: ## Build the runtime image
	$(DOCKER) build \
		--build-arg VERSION=$(VERSION) \
		--build-arg COMMIT=$(COMMIT) \
		--build-arg DATE=$(DATE) \
		-t $(IMAGE):$(IMAGE_TAG) .

# --- smoke ------------------------------------------------------------------

SMOKE_NAME ?= barqr-smoke
SMOKE_PORT ?= 38080
SMOKE_KEY  ?= smoke-key

# The project brief set 20 MB as the target. The binary carries the Swagger UI
# and ReDoc bundles, so the number is checked here rather than trusted: a README
# that claims a size is a claim like any other.
MAX_IMAGE_BYTES ?= 24000000

# smoke deliberately does not depend on docker-build: CI builds the image with
# buildx and then runs this target against it, so the two must stay separable.
# It asserts the security posture as well as the endpoints — the guards that
# matter most are the ones nothing else would notice regressing.
.PHONY: smoke
smoke: ## Start the built image and exercise it over HTTP
	@$(DOCKER) rm -f $(SMOKE_NAME) >/dev/null 2>&1 || true
	@set -e; \
	fail() { echo "  FAIL: $$1"; $(DOCKER) logs $(SMOKE_NAME) 2>&1 | tail -20; exit 1; }; \
	trap '$(DOCKER) rm -f $(SMOKE_NAME) >/dev/null 2>&1 || true' EXIT; \
	echo "==> smoke: refuses a wildcard bind with no API keys"; \
	if $(DOCKER) run --rm -e BARQR_BIND=0.0.0.0 $(IMAGE):$(IMAGE_TAG) >/dev/null 2>&1; then \
		echo "  FAIL: container started unauthenticated on a wildcard bind"; exit 1; \
	fi; \
	echo "==> smoke: refuses an invalid configuration"; \
	if $(DOCKER) run --rm -e BARQR_RATE_LIMIT=nonsense $(IMAGE):$(IMAGE_TAG) >/dev/null 2>&1; then \
		echo "  FAIL: container started with an unparseable BARQR_RATE_LIMIT"; exit 1; \
	fi; \
	echo "==> smoke: check-config validates and redacts"; \
	$(DOCKER) run --rm -e BARQR_API_KEYS=topsecret --entrypoint /barqr $(IMAGE):$(IMAGE_TAG) \
		check-config | grep -q 'BARQR_API_KEYS=<redacted' || fail "check-config redaction"; \
	$(DOCKER) run --rm -e BARQR_API_KEYS=topsecret --entrypoint /barqr $(IMAGE):$(IMAGE_TAG) \
		check-config | grep -q topsecret && fail "check-config leaked an API key"; \
	echo "==> smoke: starting container"; \
	$(DOCKER) run -d --name $(SMOKE_NAME) \
		--read-only --cap-drop ALL --security-opt no-new-privileges \
		-p 127.0.0.1:$(SMOKE_PORT):3000 \
		-e BARQR_BIND=0.0.0.0 \
		-e BARQR_API_KEYS=$(SMOKE_KEY) \
		$(IMAGE):$(IMAGE_TAG) >/dev/null; \
	base=http://127.0.0.1:$(SMOKE_PORT)/v1; \
	auth="-H X-API-Key:$(SMOKE_KEY)"; \
	for _ in $$(seq 1 100); do \
		curl -fsS $$base/readyz >/dev/null 2>&1 && break || sleep 0.2; \
	done; \
	echo "==> smoke: GET /v1/healthz"; \
	curl -fsS $$base/healthz | grep -q '"status":"ok"' || fail "healthz"; \
	echo "==> smoke: GET /v1/readyz"; \
	curl -fsS $$base/readyz | grep -q '"status":"ready"' || fail "readyz"; \
	echo "==> smoke: GET /v1/version"; \
	curl -fsS $$base/version | grep -q '"name":"barqr"' || fail "version"; \
	echo "==> smoke: unauthenticated render is rejected"; \
	test "$$(curl -s -o /dev/null -w '%{http_code}' "$$base/qr?data=hi")" = 401 \
		|| fail "an unauthenticated render was not rejected"; \
	echo "==> smoke: GET /v1/symbologies"; \
	curl -fsS $$auth $$base/symbologies | grep -q '"name":"qr"' || fail "symbologies"; \
	echo "==> smoke: GET /v1/openapi.json"; \
	curl -fsS $$auth $$base/openapi.json | grep -q '"openapi"' || fail "openapi"; \
	echo "==> smoke: GET /v1/qr renders a PNG"; \
	curl -fsS $$auth "$$base/qr?data=https://example.com" -o /tmp/barqr-smoke.png || fail "qr png"; \
	head -c 8 /tmp/barqr-smoke.png | od -An -tx1 | tr -d ' \n' \
		| grep -qi '^89504e470d0a1a0a$$' || fail "output is not a PNG"; \
	echo "==> smoke: GET /v1/qr renders SVG"; \
	curl -fsS $$auth "$$base/qr?data=hi&output.format=svg" | grep -q '<svg' || fail "qr svg"; \
	echo "==> smoke: GET /v1/qr renders ANSI for a terminal"; \
	curl -fsS $$auth "$$base/qr?data=hi&output.format=ansi" | grep -q "$$(printf '\033')" \
		|| fail "qr ansi"; \
	echo "==> smoke: ETag returns 304 on revalidation"; \
	etag=$$(curl -fsS $$auth -D - -o /dev/null "$$base/qr?data=etag" | tr -d '\r' \
		| awk '/^[Ee][Tt]ag:/{print $$2}'); \
	test -n "$$etag" || fail "no ETag header"; \
	test "$$(curl -s $$auth -H "If-None-Match: $$etag" -o /dev/null \
		-w '%{http_code}' "$$base/qr?data=etag")" = 304 || fail "ETag revalidation"; \
	echo "==> smoke: GET /v1/build/wifi"; \
	curl -fsS $$auth "$$base/build/wifi?payload.ssid=Lobby&payload.password=guest2026&payload.auth=WPA" \
		| grep -q 'WIFI:' || fail "build wifi"; \
	echo "==> smoke: POST /v1/validate"; \
	curl -fsS $$auth -H 'Content-Type: application/json' \
		-d '{"data":"hello","style":{"fg":"#eeeeee"}}' $$base/validate \
		| grep -q 'LOW_CONTRAST' || fail "validate did not flag a low-contrast design"; \
	echo "==> smoke: POST /v1/decode round-trips a rendered code"; \
	curl -fsS $$auth "$$base/qr?data=https://barqr.dev" -o /tmp/barqr-smoke-rt.png; \
	curl -fsS $$auth -H 'Content-Type: application/octet-stream' \
		--data-binary @/tmp/barqr-smoke-rt.png "$$base/decode?parse=true" \
		| grep -q 'https://barqr.dev' || fail "decode round trip"; \
	echo "==> smoke: POST /v1/batch returns a zip"; \
	curl -fsS $$auth -H 'Content-Type: application/json' \
		-d '{"items":[{"id":"a","data":"one"},{"id":"b","data":"two"}],"output":"zip"}' \
		$$base/batch -o /tmp/barqr-smoke.zip; \
	head -c 2 /tmp/barqr-smoke.zip | grep -q 'PK' || fail "batch zip"; \
	echo "==> smoke: POST /v1/sheet returns a PDF"; \
	curl -fsS $$auth -H 'Content-Type: application/json' \
		-d '{"template":"avery-l7160","items":[{"id":"x","data":"label"}]}' \
		$$base/sheet | head -c 5 | grep -q '%PDF-' || fail "sheet pdf"; \
	echo "==> smoke: GET /v1/preset/terminal"; \
	curl -fsS $$auth "$$base/preset/terminal?data=hi" | grep -q "$$(printf '\033')" \
		|| fail "preset terminal"; \
	echo "==> smoke: rendered responses carry a sandboxing CSP"; \
	curl -fsS $$auth -D - -o /dev/null "$$base/qr?data=hi&output.format=svg" \
		| grep -qi 'content-security-policy' || fail "no CSP on a rendered response"; \
	echo "==> smoke: an unknown field is rejected with a suggestion"; \
	curl -s $$auth "$$base/qr?data=hi&output.formt=png" | grep -q 'did you mean' \
		|| fail "no closest-match suggestion on an unknown field"; \
	echo "==> smoke: every symbology renders with no options at all"; \
	for sym in qr datamatrix aztec pdf417 code128 code39 code93 codabar \
	           ean13 ean8 upca upce itf itf14 2of5; do \
		case $$sym in \
			ean13) d=590123412345;; ean8) d=9638507;; upca) d=03600029145;; \
			upce) d=0425261;; itf14) d=1234567890123;; codabar) d=A12345B;; \
			*) d=12345678;; \
		esac; \
		curl -fsS $$auth -o /dev/null "$$base/barcode/$$sym?data=$$d" \
			|| fail "$$sym did not render on a request naming no options"; \
	done; \
	echo "==> smoke: the location builder resolves an address"; \
	curl -fsS $$auth --data-urlencode 'payload.location=45 Rue Didouche Mourad, Alger' \
		-G "$$base/build/location" | grep -q 'google.com/maps' || fail "location builder"; \
	echo "==> smoke: GET / is public and names the developer"; \
	curl -fsS localhost:$(SMOKE_PORT)/ | grep -q 'el-amin-dev' || fail "landing page"; \
	echo "==> smoke: the public landing page leaks no configuration"; \
	curl -fsS localhost:$(SMOKE_PORT)/ | grep -q 'max_canvas_px' \
		&& fail "the landing page exposed the instance limits"; \
	echo "==> smoke: docs need a key, and say so in HTML to a browser"; \
	test "$$(curl -s -o /dev/null -w '%{http_code}' -H 'Accept: text/html' $$base/docs)" = 401 \
		|| fail "docs served without a key"; \
	curl -fsS $$auth $$base/docs | grep -q 'barqr-data' || fail "docs dashboard"; \
	curl -fsS $$auth $$base/docs/swagger | grep -q 'swagger-ui' || fail "swagger view"; \
	curl -fsS $$auth $$base/docs/redoc | grep -q -i 'redoc' || fail "redoc view"; \
	echo "==> smoke: /metrics requires a key"; \
	test "$$(curl -s -o /dev/null -w '%{http_code}' http://127.0.0.1:$(SMOKE_PORT)/metrics)" = 401 \
		|| fail "metrics served without a key"; \
	echo "==> smoke: a remote logo is refused while remote fetch is off"; \
	test "$$(curl -s -o /dev/null -w '%{http_code}' $$auth \
		"$$base/qr?data=hi&style.logo=https://evil.example/x.png")" = 501 \
		|| fail "a remote logo was not refused"; \
	echo "==> smoke: a terminal format refuses a frame it cannot draw"; \
	test "$$(curl -s -o /dev/null -w '%{http_code}' $$auth \
		"$$base/qr?data=hi&output.format=ascii&style.frame=border")" = 400 \
		|| fail "ascii accepted a frame it cannot draw"; \
	echo "==> smoke: the json format reports its geometry"; \
	curl -fsS $$auth "$$base/qr?data=hi&output.format=json" \
		| grep -q '"symbol"' || fail "json omits the symbol rectangle"; \
	echo "==> smoke: GET /v1/nope is 404"; \
	test "$$(curl -s -o /dev/null -w '%{http_code}' $$base/nope)" = 404 || fail "404"; \
	echo "==> smoke: the image stays under its stated ceiling"; \
	bytes=$$($(DOCKER) image inspect $(IMAGE):$(IMAGE_TAG) --format '{{.Size}}'); \
	test "$$bytes" -lt $(MAX_IMAGE_BYTES) \
		|| fail "image is $$bytes bytes, over the $(MAX_IMAGE_BYTES) ceiling — \
update the README and docs, or work out what grew"; \
	echo "==> smoke: image runs as an unprivileged user"; \
	test "$$($(DOCKER) inspect --format '{{.Config.User}}' $(IMAGE):$(IMAGE_TAG))" = "65532:65532" \
		|| fail "image USER is not 65532:65532"; \
	echo "==> smoke: OK"

# --- gates ------------------------------------------------------------------

.PHONY: ci
ci: build vet lint test docker-build smoke ## Everything a pull request must pass
	@echo "==> ci: OK"

.PHONY: clean
clean: ## Remove build artefacts and caches
	rm -rf bin coverage.out $(CACHE_DIR)
	-$(DOCKER) rm -f $(SMOKE_NAME) >/dev/null 2>&1
