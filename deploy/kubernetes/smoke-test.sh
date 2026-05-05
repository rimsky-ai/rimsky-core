#!/bin/bash
# Copyright © 2026 Fall Guy Consulting.
# Licensed under the Apache License, Version 2.0. See LICENSE.apache at the
# repo root, or http://www.apache.org/licenses/LICENSE-2.0.

# Stub smoke test. Run manually after kind/minikube setup.
set -euo pipefail

echo "run: kind create cluster; helm install rimsky deploy/kubernetes/rimsky-chart --set postgres.dsn=..."
echo "Then verify pods:"
echo "  kubectl get pods -l app.kubernetes.io/name=rimsky"
echo "And control-api reachability:"
echo "  kubectl port-forward svc/rimsky-rimsky-control-api 8080:8080"
echo "  curl http://localhost:8080/healthz"
