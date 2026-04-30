#!/bin/bash
set -euo pipefail
VERSION="${RIMSKY_VERSION:-0.1}"
cd "$(dirname "$0")/.."

for BIN in rimsky-scheduler rimsky-supervisor rimsky-control-api rimsky-migrate; do
  docker build -f deploy/Dockerfile.go-base --build-arg BINARY=$BIN -t rimsky/${BIN#rimsky-}:$VERSION -t rimsky/${BIN#rimsky-}:latest .
done

docker build -f deploy/Dockerfile.http-node -t rimsky/executor-http-node:$VERSION -t rimsky/executor-http-node:latest .
docker build -f deploy/Dockerfile.claude-agent -t rimsky/executor-claude-agent:$VERSION -t rimsky/executor-claude-agent:latest .

docker build -f stores/filesystem/Dockerfile.filesystem -t rimsky/store-filesystem:$VERSION -t rimsky/store-filesystem:latest .
docker build -f stores/postgres/Dockerfile.postgres -t rimsky/store-postgres:$VERSION -t rimsky/store-postgres:latest .
docker build -f stores/stub/Dockerfile.stub -t rimsky/store-stub:$VERSION -t rimsky/store-stub:latest .

echo "Built 9 images at version $VERSION"
