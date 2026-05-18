#!/bin/bash
# Copyright © 2026 Fall Guy Consulting.
# Licensed under the Apache License, Version 2.0. See LICENSE.apache at the
# repo root, or http://www.apache.org/licenses/LICENSE-2.0.

set -euo pipefail
VERSION="${RIMSKY_VERSION:-0.1}"
cd "$(dirname "$0")/.."

for BIN in rimsky-scheduler rimsky-supervisor rimsky-control-api rimsky-migrate; do
  docker build -f deploy/Dockerfile.go-base --build-arg BINARY=$BIN -t rimsky/${BIN#rimsky-}:$VERSION -t rimsky/${BIN#rimsky-}:latest .
done

docker build -f deploy/Dockerfile.http-node -t rimsky/executor-http-node:$VERSION -t rimsky/executor-http-node:latest .
docker build -f deploy/Dockerfile.claude-agent -t rimsky/executor-claude-agent:$VERSION -t rimsky/executor-claude-agent:latest .
docker build -f executors/stub/Dockerfile.stub -t rimsky/executor-stub:$VERSION -t rimsky/executor-stub:latest .

# Unified image (rimsky/all) — bundles the four runtime binaries plus
# rimsky-entrypoint under a single PID-1 process supervisor. Defaults to
# SQLite for local development.
docker build -f deploy/Dockerfile.all -t rimsky/all:$VERSION -t rimsky/all:latest .

docker build -f stores/filesystem/Dockerfile.filesystem -t rimsky/store-filesystem:$VERSION -t rimsky/store-filesystem:latest .
docker build -f stores/postgres/Dockerfile.postgres -t rimsky/store-postgres:$VERSION -t rimsky/store-postgres:latest .
docker build -f stores/stub/Dockerfile.stub -t rimsky/store-stub:$VERSION -t rimsky/store-stub:latest .

# Bundled sensors (publisher protocol). Each binary has main.go at the
# package root — the Dockerfile target is the package itself, not a
# cmd/-rooted binary.
docker build -f sensors/sensor-cron/Dockerfile.sensor-cron -t rimsky/sensor-cron:$VERSION -t rimsky/sensor-cron:latest .
docker build -f sensors/sensor-http/Dockerfile.sensor-http -t rimsky/sensor-http:$VERSION -t rimsky/sensor-http:latest .
docker build -f sensors/sensor-object-store/Dockerfile.sensor-object-store -t rimsky/sensor-object-store:$VERSION -t rimsky/sensor-object-store:latest .
docker build -f sensors/sensor-webhook/Dockerfile.sensor-webhook -t rimsky/sensor-webhook:$VERSION -t rimsky/sensor-webhook:latest .

echo "Built 15 images at version $VERSION"
