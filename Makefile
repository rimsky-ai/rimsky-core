.PHONY: proto-gen test build lint tidy lint-docker tidy-docker test-docker build-docker proto-gen-docker cli cli-release cli-sync-embedded cli-image smoke-cli test-all build-all license-lint license-stamp docs-glossary docs-llms-full docs-lint docs-roots docs-build

# ── Host targets (assume `go`, `golangci-lint`, `protoc-gen-go*` on PATH) ──

proto-gen:
	cd protocols/proto/v1 && protoc --go_out=gen --go_opt=paths=source_relative \
	  --go-grpc_out=gen --go-grpc_opt=paths=source_relative \
	  executor.proto events.proto claim_producer.proto lifecycle.proto \
	  executor_observability.proto claim_producer_observability.proto \
	  data_processing.proto validation.proto publisher.proto

test:
	go test ./...

build:
	go build ./...

lint:
	golangci-lint run
	cd foundation && golangci-lint run
	cd protocols && golangci-lint run

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

docs-glossary:
	go run ./cmd/rimsky-docs-glossary

docs-llms-full:
	go run ./cmd/rimsky-docs-llms-full

docs-lint:
	go run ./cmd/rimsky-docs-lint all

docs-roots: docs-llms-full docs-glossary
	cp docs/agents/llms.txt llms.txt
	cp docs/agents/llms-full.txt llms-full.txt

docs-build: docs-roots

# Multi-module helpers — exercise every Go module in the repo (root + foundation + protocols).
# Each `cd` runs against that module's go.mod; the go.work file at the repo root makes
# inter-module references resolve via local replace.
test-all:
	go test ./...
	cd foundation && go test ./...
	cd protocols && go test ./...

build-all:
	go build ./...
	cd foundation && go build ./...
	cd protocols && go build ./...

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

# cli-sync-embedded copies the canonical deploy assets into the embedded
# CLI tree, applying the v1 init-scaffold transforms required by the
# CLI's `dev up` workflow (spec §2.5):
#
#  - Drop the `store-postgres` service block. The postgres store is
#    optional under v1 and its config file (`store-postgres.yml`) is
#    intentionally NOT shipped with `init`.
#  - Drop the `init-items` one-shot service that exists solely to
#    create `topics_items` for `store-postgres`.
#  - Drop the `claude-agent` executor service. The init scaffold's
#    inline rimsky_config: declares only `http-node`; shipping a
#    claude-agent container that the rimsky processes never dial leaks
#    a service into the stack.
#  - Remove `store-postgres` and `claude-agent` references from
#    `depends_on` blocks of scheduler / supervisor / control-api.
#  - Rewrite the `./rimsky.yml:/etc/rimsky/rimsky.yml:ro` mounts to
#    `../.rimsky/rimsky.yml:/etc/rimsky/rimsky.yml:ro`. `dev up`
#    materializes inline rimsky_config to `<manifest-dir>/.rimsky/`,
#    which sits one level up from the embedded compose file's location
#    in the scaffold (`./deploy/docker-compose.yml`).
#
# The transforms are implemented in awk so that fixing them in one place
# (here) keeps the embedded copy and the live deploy in lockstep.
cli-sync-embedded:
	awk '\
	BEGIN { skip = 0; bufN = 0 } \
	function flush_buf(  i) { for (i = 0; i < bufN; i++) print buf[i]; bufN = 0 } \
	function drop_buf() { bufN = 0 } \
	/^  init-items:/ { skip = 1; drop_buf(); next } \
	/^  store-postgres:/ { skip = 1; drop_buf(); next } \
	/^  claude-agent:/ { skip = 1; drop_buf(); next } \
	skip && /^  [a-zA-Z]/ { skip = 0 } \
	skip && /^[a-zA-Z]/ { skip = 0 } \
	skip { next } \
	/^      init-items:$$/ { skip_dep = 1; next } \
	/^      store-postgres:$$/ { skip_dep = 1; next } \
	/^      claude-agent:$$/ { skip_dep = 1; next } \
	skip_dep && /^        condition:/ { skip_dep = 0; next } \
	{ \
	  gsub(/\.\/rimsky\.yml:\/etc\/rimsky\/rimsky\.yml:ro/, "../.rimsky/rimsky.yml:/etc/rimsky/rimsky.yml:ro"); \
	} \
	/^  #/ { buf[bufN++] = $$0; next } \
	{ flush_buf(); print }' deploy/docker-compose.yml > control/cli/embedded/deploy/docker-compose.yml
	cp deploy/store-filesystem.yml control/cli/embedded/deploy/store-filesystem.yml
	cp deploy/supervisor-config.yml control/cli/embedded/deploy/supervisor-config.yml

cli-image:
	docker build -f Dockerfile.cli --build-arg VERSION=$(VERSION) -t rimsky/cli:latest .

smoke-cli: cli
	go test -tags smoke -count=1 -timeout 5m ./test/smoke/cli/...

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
	  cd ../protocols && PATH=/go/bin:$$PATH golangci-lint run --timeout 5m'

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
