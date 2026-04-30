.PHONY: proto-gen test build lint tidy lint-docker tidy-docker test-docker build-docker proto-gen-docker

# ── Host targets (assume `go`, `golangci-lint`, `protoc-gen-go*` on PATH) ──

proto-gen:
	cd proto/v1 && protoc --go_out=gen --go_opt=paths=source_relative \
	  --go-grpc_out=gen --go-grpc_opt=paths=source_relative \
	  node_executor.proto events.proto store_service.proto

test:
	go test ./...

build:
	go build ./...

lint:
	golangci-lint run

tidy:
	go mod tidy

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
	  PATH=/go/bin:$$PATH golangci-lint run --timeout 5m ./...'

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
