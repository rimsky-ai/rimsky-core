# syntax=docker/dockerfile:1.7
#
# Build context: the rimsky-core repo root. The bundled services build
# against the in-tree lib/protocols module via go.work — no module proxy,
# no published-tag pin.
FROM golang:1.25-alpine AS builder
WORKDIR /src
COPY go.work go.work.sum ./
COPY go.mod go.sum ./
COPY lib/foundation/go.mod lib/foundation/go.sum ./lib/foundation/
COPY lib/protocols/go.mod lib/protocols/go.sum ./lib/protocols/
COPY lib/services/go.mod lib/services/go.sum ./lib/services/
# go.work lists ./examples, so its go.mod/go.sum must be present or
# `go mod download` aborts loading the workspace.
COPY examples/go.mod examples/go.sum ./examples/
RUN cd lib/services && go mod download
COPY . .
RUN cd lib/services && CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/app ./executors/claude-agent/cmd

# The executor spawns the Claude Code CLI as a subprocess. The Go port drops
# the Node/npm runtime entirely by installing the CLI's native (self-contained)
# binary distribution instead of the @anthropic-ai/claude-code npm package.
# Pinned for reproducible builds: an unpinned install lets a later rebuild
# silently pull a different version. Bump deliberately, re-scanning.
#
# Base pinned by digest: wolfi-base is a rolling :latest, so an unpinned FROM
# lets a rebuild pull a different base non-deterministically.
FROM cgr.dev/chainguard/wolfi-base@sha256:34977aa13765da89f60fee8fe5230e2bb1c55192df08e383c58221ee0d1277fb AS clifetch
RUN apk add --no-cache curl bash
ENV HOME=/opt/claude-home
RUN mkdir -p /opt/claude-home && curl -fsSL https://claude.ai/install.sh | bash -s 2.1.186

# Runtime: wolfi (glibc) rather than distroless/static — the executor's whole
# job is spawning the `claude` CLI, which needs a real userland (shell for its
# Bash tool, git for repository work). The Node/npm surface is gone; the CVE
# surface is the base + git + tini + the native CLI binary.
FROM cgr.dev/chainguard/wolfi-base@sha256:34977aa13765da89f60fee8fe5230e2bb1c55192df08e383c58221ee0d1277fb
RUN apk add --no-cache git tini
# wolfi-base already ships an unprivileged `nonroot` user (UID/GID 65532,
# created by apko). Create it idempotently so the runtime never runs as root
# and the Dockerfile stays correct if a future base image drops the default
# user. -D = no password (BusyBox adduser), -u pins the UID.
RUN id -u nonroot >/dev/null 2>&1 || adduser -D -u 65532 nonroot
# The native installer materializes the self-contained versioned binary under
# ~/.local/share/claude/versions/<version> and symlinks ~/.local/bin/claude to
# it by absolute path; copy the real binary itself to a PATH location.
COPY --from=clifetch /opt/claude-home/.local/share/claude/versions/2.1.186 /usr/local/bin/claude
COPY --from=builder /out/app /app
USER nonroot
ENV HOME=/home/nonroot
EXPOSE 9090 9190
# tini installs to /usr/bin/tini on Wolfi (NOT /sbin/tini as on Alpine). The
# CLI children the executor spawns can orphan grandchildren; tini reaps them.
ENTRYPOINT ["/usr/bin/tini", "--", "/app"]
