# syntax=docker/dockerfile:1.7
#
# Build context: the rimsky-core repo root. The executor resolves its protos
# from the in-tree lib/protocols package (a file: dependency in
# package.json), so the build must preserve the repo layout for npm to
# resolve it.
FROM node:20-alpine AS builder
WORKDIR /build
COPY lib/protocols/ ./lib/protocols/
COPY lib/services/executors/claude-agent/package.json lib/services/executors/claude-agent/package-lock.json* ./lib/services/executors/claude-agent/
WORKDIR /build/lib/services/executors/claude-agent
RUN npm ci || npm install
WORKDIR /build
COPY lib/services/executors/claude-agent/ ./lib/services/executors/claude-agent/
WORKDIR /build/lib/services/executors/claude-agent
RUN npm run build
# npm materializes the `file:../../../../lib/protocols` dependency as a
# symlink in node_modules pointing OUTSIDE /app (at /build/lib/protocols),
# which would dangle once only /app is copied into the final stage. Stage a
# dereferenced copy (`cp -rL` follows symlinks into real directories) so the
# final image carries the real @rimsky-ai/protocols package — including its
# proto/v1/*.proto files, which proto-loader.ts resolves at runtime via
# import.meta.url relative to the installed package.
RUN cp -rL node_modules /build/node_modules_deref

FROM node:20-alpine
RUN apk add --no-cache tini git
# The executor spawns the Claude Code CLI as a subprocess. cli-runner.ts
# defaults to `"claude"` from PATH; install it globally so the binary
# lands at /usr/local/bin/claude. Without this the executor exits with
# error_class: subprocess_exit_before_complete on every dispatch.
RUN npm install -g @anthropic-ai/claude-code
WORKDIR /app
COPY --from=builder /build/lib/services/executors/claude-agent/dist ./dist
# Use the dereferenced node_modules staged in the builder so
# @rimsky-ai/protocols is a real directory (with its .proto files), not a
# dangling symlink to a /build path absent from this image.
COPY --from=builder /build/node_modules_deref ./node_modules
ENV NODE_ENV=production
USER node
EXPOSE 9090 9190
ENTRYPOINT ["/sbin/tini", "--", "node", "dist/main.js"]
