# syntax=docker/dockerfile:1.7
#
# Build context: the rimsky-core repo root. The executor resolves its protos
# from the in-tree lib/protocols package (a file: dependency in
# package.json), so the build must preserve the repo layout for npm to
# resolve it.
#
# Base: Chainguard/Wolfi (glibc). Switched off node:24-alpine (musl) because
# Alpine 3.23 ships curl/git apk packages with unfixed High CVEs. Wolfi +
# {nodejs-24,npm,git,tini} scans 0 Critical / 0 High. Both stages share the
# same glibc base so the production node_modules (incl. any native modules)
# carry no musl→glibc ABI mismatch.
#
# Pinned by digest for reproducible builds: wolfi-base is a rolling :latest, so
# an unpinned FROM lets a rebuild pull a different base non-deterministically.
# This digest is the multi-arch index validated to scan 0C/0H; bump it
# deliberately (re-resolving the tag and re-scanning).
FROM cgr.dev/chainguard/wolfi-base@sha256:34977aa13765da89f60fee8fe5230e2bb1c55192df08e383c58221ee0d1277fb AS builder
RUN apk add --no-cache nodejs-24 npm
WORKDIR /build
COPY lib/protocols/ ./lib/protocols/
COPY lib/services/executors/claude-agent/package.json lib/services/executors/claude-agent/package-lock.json* ./lib/services/executors/claude-agent/
WORKDIR /build/lib/services/executors/claude-agent
RUN npm ci || npm install
WORKDIR /build
COPY lib/services/executors/claude-agent/ ./lib/services/executors/claude-agent/
WORKDIR /build/lib/services/executors/claude-agent
RUN npm run build
# Prune devDependencies before the dereference so the runtime image carries
# production deps only. This keeps the dev toolchain (esbuild's Go binary,
# vitest/vite, eslint, typescript) out of the shipped node_modules and off the
# CVE surface. @rimsky-ai/protocols is a production `dependency`, so prune
# keeps it and the cp -rL below still dereferences its symlink into a real dir.
RUN npm prune --omit=dev
# `npm prune` removes pruned packages' contents but leaves their now-empty
# scope directories behind (e.g. an empty @esbuild/, @vitest/, @eslint/). The
# CVE-bearing files are gone, but the empty skeletons keep the dev-tree shape
# in the runtime image. Delete them so the shipped node_modules carries no
# trace of the dev toolchain (LB1: no devDependencies in the runtime image).
RUN find node_modules -mindepth 1 -type d -empty -delete
# npm materializes the `file:../../../../lib/protocols` dependency as a
# symlink in node_modules pointing OUTSIDE /app (at /build/lib/protocols),
# which would dangle once only /app is copied into the final stage. Stage a
# dereferenced copy (`cp -rL` follows symlinks into real directories) so the
# final image carries the real @rimsky-ai/protocols package — including its
# proto/v1/*.proto files, which proto-loader.ts resolves at runtime via
# import.meta.url relative to the installed package.
RUN cp -rL node_modules /build/node_modules_deref

FROM cgr.dev/chainguard/wolfi-base@sha256:34977aa13765da89f60fee8fe5230e2bb1c55192df08e383c58221ee0d1277fb
RUN apk add --no-cache nodejs-24 npm git tini
# The executor spawns the Claude Code CLI as a subprocess. cli-runner.ts
# defaults to `"claude"` from PATH; install it globally so the binary
# lands on PATH. Without this the executor exits with
# error_class: subprocess_exit_before_complete on every dispatch.
# Pinned for reproducible builds: an unpinned (floating-latest) install lets a
# later rebuild silently pull a different version. 2.1.158 is the version this
# image was validated against (scans 0C/0H). Bump deliberately, re-scanning.
RUN npm install -g @anthropic-ai/claude-code@2.1.158
# wolfi-base already ships an unprivileged `nonroot` user (UID/GID 65532,
# created by apko). Create it idempotently so the runtime never runs as root
# and the Dockerfile stays correct if a future base image drops the default
# user. -D = no password (BusyBox adduser), -u pins the UID.
RUN id -u nonroot >/dev/null 2>&1 || adduser -D -u 65532 nonroot
WORKDIR /app
COPY --from=builder /build/lib/services/executors/claude-agent/dist ./dist
# Use the dereferenced node_modules staged in the builder so
# @rimsky-ai/protocols is a real directory (with its .proto files), not a
# dangling symlink to a /build path absent from this image.
COPY --from=builder /build/node_modules_deref ./node_modules
ENV NODE_ENV=production
USER nonroot
EXPOSE 9090 9190
# tini installs to /usr/bin/tini on Wolfi (NOT /sbin/tini as on Alpine).
ENTRYPOINT ["/usr/bin/tini", "--", "node", "dist/main.js"]
