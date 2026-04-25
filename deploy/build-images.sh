#!/bin/bash
set -euo pipefail
VERSION="${RIMSKY_VERSION:-0.1}"
cd "$(dirname "$0")/.."

for BIN in rimsky-scheduler rimsky-supervisor rimsky-control-api rimsky-migrate; do
  docker build -f deploy/Dockerfile.go-base --build-arg BINARY=$BIN -t rimsky/${BIN#rimsky-}:$VERSION -t rimsky/${BIN#rimsky-}:latest .
done

docker build -f deploy/Dockerfile.http-node -t rimsky/executor-http-node:$VERSION -t rimsky/executor-http-node:latest .
docker build -f deploy/Dockerfile.claude-agent -t rimsky/executor-claude-agent:$VERSION -t rimsky/executor-claude-agent:latest .

echo "Built 6 images at version $VERSION"
