.PHONY: proto-gen test build lint tidy lint-docker tidy-docker test-docker build-docker proto-gen-docker cli cli-release core-images service-images push-images publish-protocols check-clean smoke-all test-all test-race test-root test-foundation test-protocols test-services test-examples test-report build-all license-lint license-stamp scan release buildx-builder publish-protocols-dev dev-release

# ── Host targets (assume `go`, `golangci-lint`, `protoc-gen-go*` on PATH) ──

proto-gen:
	cd lib/protocols/proto/v1 && protoc --go_out=gen --go_opt=paths=source_relative \
	  --go-grpc_out=gen --go-grpc_opt=paths=source_relative \
	  executor.proto events.proto claim_producer.proto lifecycle.proto \
	  executor_observability.proto claim_producer_observability.proto \
	  data_processing.proto validation.proto publisher.proto host_agent.proto

test:
	go test -timeout 120s ./...

build:
	go build ./...

lint:
	golangci-lint run
	cd lib/foundation && golangci-lint run
	cd lib/protocols && golangci-lint run
	cd lib/services && golangci-lint run
	cd examples && golangci-lint run

# license-lint enforces the multi-license boundary documented in
# docs/future-work/2026-05-02-licensing-design.md. Apache-classified packages
# cannot import AGPL-classified ones, and every source file's header must
# match its directory's classification per licensing.yml.
license-lint:
	go run ./tools/license-check

# license-stamp adds the appropriate header to any source file that lacks
# one. Idempotent. Run after moving files across the licensing boundary.
license-stamp:
	go run ./tools/license-check --stamp

tidy:
	go mod tidy

# ── Documentation tooling ──
#
# Docs sources and the docs-lint binaries are not part of this repo. This
# repo carries no docs targets and no docs gate.

# Multi-module helpers — exercise every Go module in the repo (root + lib/foundation + lib/protocols + lib/services).
# Each `cd` runs against that module's go.mod; the go.work file at the repo root makes
# inter-module references resolve via local replace.
#
# lib/services filters `go list ./...` to drop any path containing
# `node_modules/` — the claude-agent npm tree ships a stray Go file
# (`node_modules/flatted/golang/...`) that `go build ./...` would
# otherwise pick up. Filtering at the list step keeps the toolchain
# off third-party Go code that has no business compiling here. The
# build filter additionally drops packages with no non-test Go files,
# because passing explicit paths to `go build` (unlike `./...`) turns
# the "no non-test Go files" notice into an error.
# Per-module test targets — exposed so CI can shard the test run as a matrix
# over the four workspace modules (plus examples), with each job running its
# own module's slice in parallel. `test-all` composes them for local runs.
#
# The thin -race -count=1 slice rides on the owning module's target:
# root owns lib/runtime + lib/graph/scheduler; foundation owns its persistence
# packages. The full -count=3 treatment lives in `test-race` (release-gate
# only).
#
# Subscription mounting is asynchronous (instance-create returns 201 with
# rows in `mounting`; a reconciler drives Subscribe to `active`), and the
# docker-stack tests wait on that observable state instead of a wall-clock
# budget — so no -parallel cap is needed to keep the old synchronous-Subscribe
# flake from biting under load.
test-root:
	go test -timeout 120s ./...
	go test -timeout 180s -race -count=1 ./lib/runtime/... ./lib/graph/scheduler/...

test-foundation:
	cd lib/foundation && go test -timeout 120s ./...
	cd lib/foundation && go test -timeout 180s -race -count=1 ./persistence/postgres/... ./persistence/sqlite/...

test-protocols:
	cd lib/protocols && go test -timeout 60s ./...

test-services:
	cd lib/services && go test -timeout 120s $$(go list ./... | grep -v /node_modules/)

test-examples:
	cd examples && go test -timeout 60s ./...

test-all: test-root test-foundation test-protocols test-services test-examples

# Local test-speed observability. Runs every Go test across all modules under
# gotestsum, then prints the slowest tests across the whole run. Continues
# through failures so the timing picture is complete even if a test fails.
#
# Requires gotestsum on PATH:
#   go install gotest.tools/gotestsum@latest
#
# Output:
#   - Per-package elapsed times during the run (gotestsum --format pkgname)
#   - A final "Slowest tests" table (threshold tunable via SLOW_THRESHOLD,
#     top-N tunable via SLOW_NUM)
SLOW_THRESHOLD ?= 500ms
SLOW_NUM ?= 50
test-report:
	@command -v gotestsum >/dev/null || { echo "gotestsum not on PATH; install: go install gotest.tools/gotestsum@latest"; exit 1; }
	@mkdir -p .test-report
	-gotestsum --format pkgname --jsonfile=.test-report/root.json -- -count=1 -timeout 120s ./...
	-cd lib/foundation && gotestsum --format pkgname --jsonfile=../../.test-report/foundation.json -- -count=1 -timeout 120s ./...
	-cd lib/protocols && gotestsum --format pkgname --jsonfile=../../.test-report/protocols.json -- -count=1 -timeout 60s ./...
	-cd lib/services && gotestsum --format pkgname --jsonfile=../../.test-report/services.json -- -count=1 -timeout 120s $$(go list ./... | grep -v /node_modules/)
	-cd examples && gotestsum --format pkgname --jsonfile=../.test-report/examples.json -- -count=1 -timeout 60s ./...
	@cat .test-report/root.json .test-report/foundation.json .test-report/protocols.json .test-report/services.json .test-report/examples.json > .test-report/all.json
	@echo
	@echo "==== Slowest tests (threshold $(SLOW_THRESHOLD), top $(SLOW_NUM)) ===="
	@gotestsum tool slowest --jsonfile .test-report/all.json --threshold=$(SLOW_THRESHOLD) --num=$(SLOW_NUM)

# Full race-detection gate over the race-sensitive packages: -count=3 to
# shake out scheduling-order-dependent races that a single run can miss.
# Required by the `release` chain.
#
# Scope is the load-bearing race surface — runtime + scheduler. The
# persistence packages are deliberately omitted from the -count=3 slice:
# their race surface is mostly contention against the underlying driver,
# not Go data races, and `test-all` already covers them with
# -race -count=1, which catches the common races on every run without
# tripling testcontainer boot cost in the release gate.
test-race:
	go test -timeout 300s -race -count=3 ./lib/runtime/... ./lib/graph/scheduler/...

build-all:
	go build ./...
	cd lib/foundation && go build ./...
	cd lib/protocols && go build ./...
	cd lib/services && go build $$(go list -f '{{if .GoFiles}}{{.ImportPath}}{{end}}' ./... | grep -v /node_modules/)
	cd examples && go build ./...

# ── rimsky CLI targets ──

# Match only repo-level release tags (v1.2.3), never the path-prefixed
# Go-module tag (protocols/v0.1.0) — a slash is invalid in a Docker image tag.
# Falls back to a short commit SHA when no release tag is reachable.
VERSION ?= $(shell git describe --tags --match='v[0-9]*' --always --dirty 2>/dev/null || echo dev)

cli:
	go build -ldflags "-X main.version=$(VERSION)" -o bin/rimsky ./cmd/rimsky/

cli-release:
	@mkdir -p bin/release
	@for os in linux darwin; do \
	  for arch in amd64 arm64; do \
	    GOOS=$$os GOARCH=$$arch go build -ldflags "-X main.version=$(VERSION)" -o bin/release/rimsky_$${os}_$${arch} ./cmd/rimsky/; \
	  done; \
	done; \
	GOOS=windows GOARCH=amd64 go build -ldflags "-X main.version=$(VERSION)" -o bin/release/rimsky_windows_amd64.exe ./cmd/rimsky/

# Distributed images, built from this tree (Dockerfiles live in dockerfiles/).
# Four images:
#   rimsky                  — all role binaries + rimsky-entrypoint under one
#                             image; role chosen by container command, backend
#                             (postgres|sqlite) by config (Dockerfile.rimsky).
#   rimsky-all-in-one       — the `rimsky` image plus baked zero-config SQLite
#                             defaults; runs out of the box for local dev, not
#                             for production (Dockerfile.all-in-one). Built FROM
#                             the rimsky:$(VERSION) image, so it follows it here.
#   rimsky-host-agent-proxy — the late-bound host-agent proxy service
#                             (Dockerfile.go-base, single binary).
#   rimsky-conformance      — bundled protocol conformance runners; pick one
#                             by container command (Dockerfile.conformance).
# Each image is tagged $(VERSION) + latest. The CLI ships as a binary
# (`make cli` / `make cli-release`), not an image.
core-images:
	docker build -f dockerfiles/Dockerfile.rimsky -t rimsky:$(VERSION) -t rimsky:latest .
	docker build -f dockerfiles/Dockerfile.all-in-one --build-arg RIMSKY_BASE=rimsky:$(VERSION) \
	  -t rimsky-all-in-one:$(VERSION) -t rimsky-all-in-one:latest .
	docker build -f dockerfiles/Dockerfile.go-base --build-arg BINARY=rimsky-host-agent-proxy \
	  -t rimsky-host-agent-proxy:$(VERSION) -t rimsky-host-agent-proxy:latest .
	docker build -f dockerfiles/Dockerfile.conformance -t rimsky-conformance:$(VERSION) -t rimsky-conformance:latest .

# Bundled-service images: the consumption-side services (stores, sensors,
# subscribers, executors) shipped by rimsky as images. Each Dockerfile lives
# co-located with its service under lib/services/; the build context is the
# repo root so the build can reach lib/protocols + lib/services + go.work
# (the bundled services build against the in-tree protocols module via the
# workspace, with no published-tag pin). Each image is tagged $(VERSION) +
# latest, with a `rimsky-` prefix matching the core-image naming.
service-images:
	docker build -f lib/services/claim_producers/filesystem/Dockerfile.filesystem -t rimsky-claim-producer-filesystem:$(VERSION) -t rimsky-claim-producer-filesystem:latest .
	docker build -f lib/services/claim_producers/postgres/Dockerfile.postgres -t rimsky-claim-producer-postgres:$(VERSION) -t rimsky-claim-producer-postgres:latest .
	docker build -f lib/services/sensors/sensor-cron/Dockerfile.sensor-cron -t rimsky-sensor-cron:$(VERSION) -t rimsky-sensor-cron:latest .
	docker build -f lib/services/sensors/sensor-http/Dockerfile.sensor-http -t rimsky-sensor-http:$(VERSION) -t rimsky-sensor-http:latest .
	docker build -f lib/services/sensors/sensor-object-store/Dockerfile.sensor-object-store -t rimsky-sensor-object-store:$(VERSION) -t rimsky-sensor-object-store:latest .
	docker build -f lib/services/sensors/sensor-webhook/Dockerfile.sensor-webhook -t rimsky-sensor-webhook:$(VERSION) -t rimsky-sensor-webhook:latest .
	docker build -f lib/services/subscribers/openlineage/Dockerfile.openlineage -t rimsky-subscriber-openlineage:$(VERSION) -t rimsky-subscriber-openlineage:latest .
	docker build -f lib/services/executors/http-node/Dockerfile.http-node -t rimsky-executor-http-node:$(VERSION) -t rimsky-executor-http-node:latest .
	docker build -f lib/services/executors/verifier-http/Dockerfile.verifier-http -t rimsky-executor-verifier-http:$(VERSION) -t rimsky-executor-verifier-http:latest .
	docker build -f lib/services/executors/verifier-shape-checks/Dockerfile.verifier-shape-checks -t rimsky-executor-verifier-shape-checks:$(VERSION) -t rimsky-executor-verifier-shape-checks:latest .
	docker build -f lib/services/executors/claude-agent/Dockerfile -t rimsky-executor-claude-agent:$(VERSION) -t rimsky-executor-claude-agent:latest .

# The 15-image set published by this repo. Single source of truth for `scan`
# and (in symbolic form) push-images. Order matters for push-images:
# `rimsky` is the base for `rimsky-all-in-one`, so it must be pushed first.
IMAGES := \
    rimsky rimsky-all-in-one rimsky-host-agent-proxy rimsky-conformance \
    rimsky-claim-producer-filesystem rimsky-claim-producer-postgres \
    rimsky-sensor-cron rimsky-sensor-http rimsky-sensor-object-store rimsky-sensor-webhook \
    rimsky-subscriber-openlineage \
    rimsky-executor-http-node rimsky-executor-verifier-http rimsky-executor-verifier-shape-checks rimsky-executor-claude-agent

# Scan every locally-built image for critical or high CVEs via Docker Scout.
# Used as a pre-push gate by the `release` target. Exits non-zero on the first
# image that fails so a bad release can't proceed. Requires the `docker scout`
# CLI plugin (bundled with Docker Desktop; installed manually elsewhere).
# Works against the local docker daemon, no Hub enrollment required.
#
# Per-image accepted-CVE allowlist in $(SCAN_ACCEPTED_CVES_FILE) — see that
# file for the format and the rules governing what may go in. Accepted IDs
# are subtracted from each image's findings before the exit-code check; the
# human-readable scout output still surfaces them in the build log.
SCAN_ACCEPTED_CVES_FILE := .scout-accepted-cves.txt

scan:
	@command -v docker >/dev/null || { echo "docker not on PATH"; exit 1; }
	@docker scout --help >/dev/null 2>&1 || { echo "docker scout plugin not installed — install Docker Desktop or 'docker scout' plugin"; exit 1; }
	@for img in $(IMAGES); do \
	  echo "=== docker scout cves $$img:$(VERSION) ==="; \
	  scan_out=$$(mktemp); found_f=$$(mktemp); accepted_f=$$(mktemp); \
	  docker scout cves --only-severity critical,high $$img:$(VERSION) 2>&1 | tee $$scan_out; \
	  grep -oE 'CVE-[0-9]+-[0-9]+' $$scan_out | sort -u > $$found_f; \
	  rm -f $$scan_out; \
	  found_count=$$(wc -l < $$found_f | tr -d ' '); \
	  if [ "$$found_count" = "0" ]; then rm -f $$found_f $$accepted_f; continue; fi; \
	  if [ -f $(SCAN_ACCEPTED_CVES_FILE) ]; then \
	    grep -E "^$$img:" $(SCAN_ACCEPTED_CVES_FILE) | sed 's/#.*//' | awk -F: '{print $$2}' | tr -d ' ' | grep -E 'CVE-[0-9]+-[0-9]+' | sort -u > $$accepted_f; \
	  else \
	    : > $$accepted_f; \
	  fi; \
	  remaining=$$(comm -23 $$found_f $$accepted_f); \
	  rm -f $$found_f $$accepted_f; \
	  if [ -z "$$remaining" ]; then \
	    echo ""; \
	    echo "[scan] $$img: $$found_count finding(s) all accepted per $(SCAN_ACCEPTED_CVES_FILE)"; \
	    continue; \
	  fi; \
	  echo ""; \
	  echo "[scan] $$img: unaccepted CVE(s) remain:"; \
	  echo "$$remaining" | sed 's/^/  /'; \
	  exit 1; \
	done

# Push every rimsky image to $(REGISTRY) with SBOM + provenance attestations
# attached. Covers the four core images and the eleven bundled-service images
# in one pass — a release ships them together. Uses `docker buildx build --push`
# rather than `docker tag && docker push` because plain push does not carry
# build attestations; Docker Scout reports "Missing supply chain attestation(s)"
# in the Hub UI without them.
#
# Requires:
#   - a prior `docker login` to the registry
#   - buildx (bundled with Docker Desktop)
# This target builds (with cache) and pushes; it does NOT rely on
# `make core-images` / `make service-images` having run first — the buildx
# layer cache is shared with the local docker daemon so most layers hit cache
# from prior builds, but a clean push works standalone.
#
# A future release-management skill may split this into per-set targets; for
# now, keep it as one script.
#
# NOTE: the Docker Hub org is `rimskyai`, NOT `rimsky-ai`. Docker Hub namespaces
# disallow hyphens (unlike GitHub `rimsky-ai` and the npm `@rimsky-ai` scope);
# the hyphens survive only in the repo names (rimsky-host-agent-proxy). Do not
# "correct" this to rimsky-ai to match the other namespaces — it does not exist.
#
# The floating second tag is `$(LATEST_TAG)`, defaulting to `latest`;
# `make dev-release` overrides it to `dev`.
REGISTRY ?= docker.io/rimskyai

# Floating tag pushed alongside :$(VERSION) on every image. Defaults to
# `latest` for formal releases; `make dev-release` overrides to `dev` so
# the dev channel never moves :latest.
LATEST_TAG ?= latest

# Buildx instance used by push-images. Created on first use; idempotent.
# A dedicated docker-container builder gives consistent attestation support
# across Docker Desktop, OrbStack, and headless CI runners.
buildx-builder:
	@docker buildx inspect rimsky-builder >/dev/null 2>&1 \
	  || docker buildx create --name rimsky-builder --driver docker-container

# Shared flags for every buildx --push invocation below.
BUILDX_PUSH = docker buildx build --builder rimsky-builder --push \
              --provenance=mode=max --sbom=true

push-images: check-clean buildx-builder
	# Core images. `rimsky` first; `rimsky-all-in-one` FROMs it via the registry.
	$(BUILDX_PUSH) -f dockerfiles/Dockerfile.rimsky \
	  -t $(REGISTRY)/rimsky:$(VERSION) -t $(REGISTRY)/rimsky:$(LATEST_TAG) .
	$(BUILDX_PUSH) -f dockerfiles/Dockerfile.all-in-one \
	  --build-arg RIMSKY_BASE=$(REGISTRY)/rimsky:$(VERSION) \
	  -t $(REGISTRY)/rimsky-all-in-one:$(VERSION) -t $(REGISTRY)/rimsky-all-in-one:$(LATEST_TAG) .
	$(BUILDX_PUSH) -f dockerfiles/Dockerfile.go-base --build-arg BINARY=rimsky-host-agent-proxy \
	  -t $(REGISTRY)/rimsky-host-agent-proxy:$(VERSION) -t $(REGISTRY)/rimsky-host-agent-proxy:$(LATEST_TAG) .
	$(BUILDX_PUSH) -f dockerfiles/Dockerfile.conformance \
	  -t $(REGISTRY)/rimsky-conformance:$(VERSION) -t $(REGISTRY)/rimsky-conformance:$(LATEST_TAG) .
	# Bundled-service images.
	$(BUILDX_PUSH) -f lib/services/claim_producers/filesystem/Dockerfile.filesystem \
	  -t $(REGISTRY)/rimsky-claim-producer-filesystem:$(VERSION) -t $(REGISTRY)/rimsky-claim-producer-filesystem:$(LATEST_TAG) .
	$(BUILDX_PUSH) -f lib/services/claim_producers/postgres/Dockerfile.postgres \
	  -t $(REGISTRY)/rimsky-claim-producer-postgres:$(VERSION) -t $(REGISTRY)/rimsky-claim-producer-postgres:$(LATEST_TAG) .
	$(BUILDX_PUSH) -f lib/services/sensors/sensor-cron/Dockerfile.sensor-cron \
	  -t $(REGISTRY)/rimsky-sensor-cron:$(VERSION) -t $(REGISTRY)/rimsky-sensor-cron:$(LATEST_TAG) .
	$(BUILDX_PUSH) -f lib/services/sensors/sensor-http/Dockerfile.sensor-http \
	  -t $(REGISTRY)/rimsky-sensor-http:$(VERSION) -t $(REGISTRY)/rimsky-sensor-http:$(LATEST_TAG) .
	$(BUILDX_PUSH) -f lib/services/sensors/sensor-object-store/Dockerfile.sensor-object-store \
	  -t $(REGISTRY)/rimsky-sensor-object-store:$(VERSION) -t $(REGISTRY)/rimsky-sensor-object-store:$(LATEST_TAG) .
	$(BUILDX_PUSH) -f lib/services/sensors/sensor-webhook/Dockerfile.sensor-webhook \
	  -t $(REGISTRY)/rimsky-sensor-webhook:$(VERSION) -t $(REGISTRY)/rimsky-sensor-webhook:$(LATEST_TAG) .
	$(BUILDX_PUSH) -f lib/services/subscribers/openlineage/Dockerfile.openlineage \
	  -t $(REGISTRY)/rimsky-subscriber-openlineage:$(VERSION) -t $(REGISTRY)/rimsky-subscriber-openlineage:$(LATEST_TAG) .
	$(BUILDX_PUSH) -f lib/services/executors/http-node/Dockerfile.http-node \
	  -t $(REGISTRY)/rimsky-executor-http-node:$(VERSION) -t $(REGISTRY)/rimsky-executor-http-node:$(LATEST_TAG) .
	$(BUILDX_PUSH) -f lib/services/executors/verifier-http/Dockerfile.verifier-http \
	  -t $(REGISTRY)/rimsky-executor-verifier-http:$(VERSION) -t $(REGISTRY)/rimsky-executor-verifier-http:$(LATEST_TAG) .
	$(BUILDX_PUSH) -f lib/services/executors/verifier-shape-checks/Dockerfile.verifier-shape-checks \
	  -t $(REGISTRY)/rimsky-executor-verifier-shape-checks:$(VERSION) -t $(REGISTRY)/rimsky-executor-verifier-shape-checks:$(LATEST_TAG) .
	$(BUILDX_PUSH) -f lib/services/executors/claude-agent/Dockerfile \
	  -t $(REGISTRY)/rimsky-executor-claude-agent:$(VERSION) -t $(REGISTRY)/rimsky-executor-claude-agent:$(LATEST_TAG) .

# Full release chain — pre-push gates, build, scan, push.
#
# Order:
#   lint           — golangci-lint across all four modules (cheap, host-side)
#   license-lint   — go run ./tools/license-check (cheap, host-side)
#   core-images    — build the 4 core images locally
#   service-images — build the 11 bundled-service images locally
#   test-all       — full Go test suite, including testcontainer scenarios
#                    (requires Docker daemon for the testcontainer tests)
#   test-race      — full -race -count=3 treatment over the race-sensitive
#                    packages (runtime, scheduler, persistence)
#   scan           — docker scout cves against every locally-built image
#   push-images    — buildx build + push with SBOM + provenance attestations
#
# Image builds come BEFORE test-all on purpose: the services scenarios under
# lib/services/test/ pull rimsky-all-in-one:latest (and the bundled-service
# :latest tags) from the LOCAL docker daemon — nothing is fetched from a
# registry. If test-all ran first, the services scenarios would exercise
# whatever image happened to be on disk from a prior build, not the source
# tree we're about to release. That silently lets a regression in the live
# code slip through the gate (or, if no prior image exists, fails the test
# for a wholly unrelated reason). Building first means test-all always
# exercises the same binaries that scan + push-images later ship.
#
# Both `/release` (the skill, formal releases) and `make dev-release`
# (mechanical dev channel) invoke this chain; dev-release overrides
# LATEST_TAG=dev so the floating tag pushed alongside :$(VERSION) is :dev
# instead of :latest. If scan finds vulnerabilities, the chain stops before
# push.
release: lint license-lint core-images service-images test-all test-race scan push-images

# Mechanical pre-release / dev channel. Derives a SemVer-2.0 pre-release
# version (v<next-minor>.0-dev.<YYYYMMDD>.g<sha>) from the latest stable
# tag, then drives the same `make release` chain with LATEST_TAG=dev so the
# floating tag pushed alongside :$(VERSION) is :dev, not :latest. Bumps
# lib/protocols/package.json transiently for the npm publish.
#
# Implementation lives in tools/dev-release.sh (shell-heavy work that doesn't
# belong inline in the Makefile). The target is the entry point operators
# invoke (manually, via CI, via cron, etc.).
#
# The /release skill (formal releases) does NOT invoke this target — formal
# releases run the same chain but with their own SemVer/notes/review logic.
dev-release: check-clean
	@./tools/dev-release.sh

# Publish the @rimsky-ai/protocols npm package (the Apache wire-contract
# bundle). Needs a prior `npm login` to the @rimsky-ai scope. The package
# version comes from protocols/package.json (kept in lockstep with the
# protocols/vX.Y.Z Go-module tag), not $(VERSION) — but the clean-tree guard
# still applies so we never publish from an uncommitted tree.
publish-protocols: check-clean
	cd lib/protocols && npm publish

# Dev-channel sibling of publish-protocols. Lands the version under the
# `dev` npm dist-tag instead of `latest`. Same clean-tree guard.
# Invoked by `make dev-release` (which passes VERSION=$(DEV_VERSION) so
# the *-dirty arm of check-clean does not trip on the uncommitted
# package.json bump in the dev flow).
publish-protocols-dev: check-clean
	cd lib/protocols && npm publish --tag dev

# Publish guard shared by push-images / publish-protocols. Refuses to publish
# from a dirty tree: $(VERSION) would carry the -dirty suffix and the artifact
# would not be reproducible from any committed state. Commit (or stash) first.
check-clean:
	@case "$(VERSION)" in \
	  *-dirty) echo "refusing to publish: VERSION=$(VERSION) — commit or stash first"; exit 1 ;; \
	  dev)     echo "refusing to publish: no release version derivable (VERSION=dev)"; exit 1 ;; \
	esac
	@echo "publish guard ok: clean tree, VERSION=$(VERSION)"

smoke-all:
	go test -tags smoke -count=1 -timeout 5m ./test/smoke/all/...

# ── Docker-wrapped variants for contributors without a host Go toolchain ──
#
# Bind-mounts the repo as /src; uses two named volumes for caches:
#   - rimsky-gomod  → /go/pkg/mod   (Go module download cache)
#   - rimsky-gobin  → /go/bin       (installed dev tools: golangci-lint,
#                                    protoc-gen-go, protoc-gen-go-grpc)
#
# The on-demand `go install` on first run takes ~30s; subsequent runs are
# instant because the Linux binaries are cached in rimsky-gobin. The host's
# own `~/go/bin/` is intentionally NOT mounted: a Darwin-built golangci-lint
# does not run inside a Linux container.

DOCKER_GO_IMAGE ?= golang:1.25
DOCKER_RUN = docker run --rm \
  -v "$(CURDIR)":/src -w /src \
  -v rimsky-gomod:/go/pkg/mod \
  -v rimsky-gobin:/go/bin

build-docker:
	$(DOCKER_RUN) $(DOCKER_GO_IMAGE) go build ./...

test-docker:
	$(DOCKER_RUN) -v /var/run/docker.sock:/var/run/docker.sock \
	  $(DOCKER_GO_IMAGE) go test ./... -count=1 -timeout 180s

lint-docker:
	$(DOCKER_RUN) $(DOCKER_GO_IMAGE) sh -c '\
	  command -v golangci-lint >/dev/null || \
	    go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest; \
	  PATH=/go/bin:$$PATH golangci-lint run --timeout 5m && \
	  cd /src/lib/foundation && PATH=/go/bin:$$PATH golangci-lint run --timeout 5m && \
	  cd /src/lib/protocols && PATH=/go/bin:$$PATH golangci-lint run --timeout 5m && \
	  cd /src/lib/services && PATH=/go/bin:$$PATH golangci-lint run --timeout 5m'

tidy-docker:
	$(DOCKER_RUN) $(DOCKER_GO_IMAGE) go mod tidy

proto-gen-docker:
	$(DOCKER_RUN) -v /var/run/docker.sock:/var/run/docker.sock \
	  $(DOCKER_GO_IMAGE) sh -c '\
	  command -v protoc >/dev/null || (apt-get update -qq && apt-get install -y -qq protobuf-compiler); \
	  command -v protoc-gen-go >/dev/null || \
	    go install google.golang.org/protobuf/cmd/protoc-gen-go@latest; \
	  command -v protoc-gen-go-grpc >/dev/null || \
	    go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@latest; \
	  PATH=/go/bin:$$PATH $(MAKE) proto-gen'
