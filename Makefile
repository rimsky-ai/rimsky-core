.PHONY: proto-gen test build lint tidy lint-docker tidy-docker test-docker build-docker proto-gen-docker cli cli-release core-images smoke-all test-all build-all license-lint license-stamp

# ── Host targets (assume `go`, `golangci-lint`, `protoc-gen-go*` on PATH) ──

proto-gen:
	cd protocols/proto/v1 && protoc --go_out=gen --go_opt=paths=source_relative \
	  --go-grpc_out=gen --go-grpc_opt=paths=source_relative \
	  executor.proto events.proto claim_producer.proto lifecycle.proto \
	  executor_observability.proto claim_producer_observability.proto \
	  data_processing.proto validation.proto publisher.proto host_agent.proto

test:
	go test ./...

build:
	go build ./...

lint:
	golangci-lint run
	cd foundation && golangci-lint run
	cd protocols && golangci-lint run
	cd testpg && golangci-lint run

# license-lint enforces the multi-license boundary documented in
# docs/future-work/2026-05-02-licensing-design.md. Apache-classified packages
# cannot import AGPL-classified ones, and every source file's header must
# match its directory's classification per licensing.yml.
license-lint:
	go run ./cmd/rimsky-license-check

# license-stamp adds the appropriate header to any source file that lacks
# one. Idempotent. Run after moving files across the licensing boundary.
license-stamp:
	go run ./cmd/rimsky-license-check --stamp

tidy:
	go mod tidy

# ── Documentation tooling ──
#
# Docs sources and the docs-lint binaries are not part of this repo. This
# repo carries no docs targets and no docs gate.

# Multi-module helpers — exercise every Go module in the repo (root + foundation + protocols + testpg).
# Each `cd` runs against that module's go.mod; the go.work file at the repo root makes
# inter-module references resolve via local replace.
test-all:
	go test ./...
	cd foundation && go test ./...
	cd protocols && go test ./...
	cd testpg && go test ./...

build-all:
	go build ./...
	cd foundation && go build ./...
	cd protocols && go build ./...
	cd testpg && go build ./...

# ── rimsky CLI targets ──

VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)

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

# Distributed images, built from this tree. Three images:
#   rimsky                  — all role binaries + rimsky-entrypoint under one
#                             image; role chosen by container command, backend
#                             (postgres|sqlite) by config (Dockerfile.rimsky).
#   rimsky-host-agent-proxy — the late-bound host-agent proxy service
#                             (Dockerfile.go-base, single binary).
#   rimsky-conformance      — bundled protocol conformance runners; pick one
#                             by container command (Dockerfile.conformance).
# Each image is tagged $(VERSION) + latest. The CLI ships as a binary
# (`make cli` / `make cli-release`), not an image.
core-images:
	docker build -f Dockerfile.rimsky -t rimsky:$(VERSION) -t rimsky:latest .
	docker build -f Dockerfile.go-base --build-arg BINARY=rimsky-host-agent-proxy \
	  -t rimsky-host-agent-proxy:$(VERSION) -t rimsky-host-agent-proxy:latest .
	docker build -f Dockerfile.conformance -t rimsky-conformance:$(VERSION) -t rimsky-conformance:latest .

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
	  $(DOCKER_GO_IMAGE) go test ./... -count=1 -timeout 600s

lint-docker:
	$(DOCKER_RUN) $(DOCKER_GO_IMAGE) sh -c '\
	  command -v golangci-lint >/dev/null || \
	    go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest; \
	  PATH=/go/bin:$$PATH golangci-lint run --timeout 5m && \
	  cd foundation && PATH=/go/bin:$$PATH golangci-lint run --timeout 5m && \
	  cd ../protocols && PATH=/go/bin:$$PATH golangci-lint run --timeout 5m && \
	  cd ../testpg && PATH=/go/bin:$$PATH golangci-lint run --timeout 5m'

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
