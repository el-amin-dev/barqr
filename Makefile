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

# smoke deliberately does not depend on docker-build: CI builds the image with
# buildx and then runs this target against it, so the two must stay separable.
.PHONY: smoke
smoke: ## Start the built image and exercise it over HTTP
	@$(DOCKER) rm -f $(SMOKE_NAME) >/dev/null 2>&1 || true
	@set -e; \
	fail() { echo "  FAIL: $$1"; $(DOCKER) logs $(SMOKE_NAME) 2>&1 | tail -20; exit 1; }; \
	trap '$(DOCKER) rm -f $(SMOKE_NAME) >/dev/null 2>&1 || true' EXIT; \
	echo "==> smoke: refusing an insecure configuration"; \
	if $(DOCKER) run --rm -e BARQR_BIND=0.0.0.0 $(IMAGE):$(IMAGE_TAG) >/dev/null 2>&1; then \
		echo "  FAIL: container started on a wildcard bind with no API keys"; exit 1; \
	fi; \
	echo "==> smoke: starting container"; \
	$(DOCKER) run -d --name $(SMOKE_NAME) \
		-p 127.0.0.1:$(SMOKE_PORT):3000 \
		-e BARQR_BIND=0.0.0.0 \
		-e BARQR_API_KEYS=smoke-key \
		$(IMAGE):$(IMAGE_TAG) >/dev/null; \
	base=http://127.0.0.1:$(SMOKE_PORT)/v1; \
	for _ in $$(seq 1 100); do \
		curl -fsS $$base/readyz >/dev/null 2>&1 && break || sleep 0.2; \
	done; \
	echo "==> smoke: GET /v1/healthz"; \
	curl -fsS $$base/healthz | grep -q '"status":"ok"' || fail "healthz"; \
	echo "==> smoke: GET /v1/readyz"; \
	curl -fsS $$base/readyz | grep -q '"status":"ready"' || fail "readyz"; \
	echo "==> smoke: GET /v1/version"; \
	curl -fsS $$base/version | grep -q '"name":"barqr"' || fail "version"; \
	echo "==> smoke: GET /v1/nope is 404"; \
	test "$$(curl -s -o /dev/null -w '%{http_code}' $$base/nope)" = 404 || fail "404"; \
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
