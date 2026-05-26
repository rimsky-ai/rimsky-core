#!/bin/bash
# Copyright © 2026 Fall Guy Consulting.
# Licensed under the Apache License, Version 2.0. See LICENSE.apache at the
# repo root, or http://www.apache.org/licenses/LICENSE-2.0.
#
# Builds rimsky-core Docker images only: the four runtime binaries
# (scheduler / supervisor / control-api / migrate), the unified rimsky/all
# convenience image, and the in-tree test-double executor (executor-stub)
# and test-double store (store-stub).
#
# Bundled-service images (production-side stores / sensors / subscribers /
# executors: claude-agent, http-node, verifier-http, verifier-shape-checks)
# live in ../rimsky-services and are built separately by that repo's
# deploy/build-images.sh. See spec
# .ok-planner/specs/2026-05-24-repo-reorganization-design.md phase P3.

set -euo pipefail
VERSION="${RIMSKY_VERSION:-0.1}"
cd "$(dirname "$0")/.."

for BIN in rimsky-scheduler rimsky-supervisor rimsky-control-api rimsky-migrate; do
  docker build -f deploy/Dockerfile.go-base --build-arg BINARY=$BIN -t rimsky/${BIN#rimsky-}:$VERSION -t rimsky/${BIN#rimsky-}:latest .
done

# Unified image (rimsky/all) — bundles the four runtime binaries plus
# rimsky-entrypoint under a single PID-1 process supervisor. Defaults to
# SQLite for local development.
docker build -f deploy/Dockerfile.all -t rimsky/all:$VERSION -t rimsky/all:latest .

# In-tree test doubles. Kept in rimsky as test infrastructure per spec
# 2026-05-24-repo-reorganization-design carve-outs.
docker build -f executors/stub/Dockerfile.stub -t rimsky/executor-stub:$VERSION -t rimsky/executor-stub:latest .
docker build -f stores/stub/Dockerfile.stub -t rimsky/store-stub:$VERSION -t rimsky/store-stub:latest .

echo "Built 7 rimsky-core images at version $VERSION"
