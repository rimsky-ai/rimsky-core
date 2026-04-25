#!/bin/bash
# Stub smoke test. Run manually after kind/minikube setup.
set -euo pipefail

echo "run: kind create cluster; helm install rimsky deploy/kubernetes/rimsky-chart --set postgres.dsn=..."
echo "Then verify pods:"
echo "  kubectl get pods -l app.kubernetes.io/name=rimsky"
echo "And control-api reachability:"
echo "  kubectl port-forward svc/rimsky-rimsky-control-api 8080:8080"
echo "  curl http://localhost:8080/healthz"
