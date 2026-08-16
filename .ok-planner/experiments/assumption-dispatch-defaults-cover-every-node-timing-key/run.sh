#!/bin/bash
# Experiment: assumption dispatch-defaults-cover-every-node-timing-key.
#
# The population is every per-node timing or retry key a template may carry:
# sync_rpc_deadline, max_quiet_period, max_runtime, max_retries, and the four
# retry_backoff subkeys. For each one the run writes a rimsky.yml naming it
# under dispatch_defaults, boots a container on that config, and asks whether
# the deployment comes up. The config loader is strict, so an unsupported key
# is not ignored -- it stops the deployment before it starts, which is the
# observation.
#
# Requires: docker, python3, RIMSKY_IMAGE_TAG.
set -u

TAG=${RIMSKY_IMAGE_TAG:?set RIMSKY_IMAGE_TAG}
free_port() { python3 -c 'import socket;s=socket.socket();s.bind(("127.0.0.1",0));print(s.getsockname()[1]);s.close()'; }
NAME=exp-assumption-dispatch-defaults-cover-every-node-timing-key
WORK=$(mktemp -d)
cleanup() { docker rm -f "$NAME" >/dev/null 2>&1; }
trap cleanup EXIT

fail=0

# name : yaml body under dispatch_defaults
KEYS=(
  "sync_rpc_deadline|  sync_rpc_deadline: 30s"
  "max_quiet_period|  max_quiet_period: 5m"
  "max_runtime|  max_runtime: 24h"
  "max_retries|  max_retries: 3"
  "retry_backoff.kind|  retry_backoff:\n    kind: exponential"
  "retry_backoff.jitter|  retry_backoff:\n    jitter: true"
  "retry_backoff.base_delay_ms|  retry_backoff:\n    base_delay_ms: 100"
  "retry_backoff.max_delay_ms|  retry_backoff:\n    max_delay_ms: 5000"
)

accepted=0
for entry in "${KEYS[@]}"; do
  key=${entry%%|*}; body=${entry#*|}
  {
    printf 'persistence:\n  driver: sqlite\n  sqlite:\n    path: /var/lib/rimsky/state.db\ndispatch_defaults:\n'
    printf "$body\n"
    printf 'claim_producers: {}\nnamed_locks: {}\nexecutors: {}\n'
  } > "$WORK/rimsky.yml"
  p=$(free_port)
  docker rm -f "$NAME" >/dev/null 2>&1
  docker run -d --name "$NAME" -p "$p:8080" -v "$WORK/rimsky.yml:/etc/rimsky/rimsky.yml:ro" \
    "rimsky-all-in-one:$TAG" >/dev/null || { echo "FAIL  could not start a container"; exit 1; }
  ok=no
  for _ in $(seq 1 25); do
    [ "$(curl -s -m 2 -o /dev/null -w '%{http_code}' "http://127.0.0.1:$p/v1/health")" = 200 ] && { ok=yes; break; }
    sleep 1
  done
  if [ $ok = yes ]; then
    accepted=$((accepted+1))
    printf '  %-28s deployment came up\n' "$key"
  else
    reason=$(docker logs "$NAME" 2>&1 | grep -o 'field [a-z_]* not found in type [A-Za-z.]*' | head -1)
    printf '  %-28s deployment did NOT come up — %s\n' "$key" "${reason:-see container logs}"
  fi
  docker rm -f "$NAME" >/dev/null 2>&1
done

echo
echo "$accepted of ${#KEYS[@]} per-node timing/retry keys have a dispatch_defaults form"
if [ $accepted -eq ${#KEYS[@]} ]; then
  echo "PASS  every per-node timing key can be set deployment-wide"
else
  echo "FAIL  $(( ${#KEYS[@]} - accepted )) of ${#KEYS[@]} can only be set per node"
  fail=1
fi

echo
if [ $fail -eq 0 ]; then echo "RESULT: PASS"; else echo "RESULT: FAIL"; fi
exit $fail
