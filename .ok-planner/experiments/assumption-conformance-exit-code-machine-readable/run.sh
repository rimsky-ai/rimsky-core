#!/usr/bin/env bash
# Experiment: assumption-conformance-exit-code-machine-readable
#
# A service author wiring conformance into CI wants two things from the kit:
# machine-readable per-scenario output, and an exit code that tells "your
# implementation failed a check" apart from "I never reached your endpoint".
# This run asks the CLI for JSON output on every subcommand, then compares the
# exit codes of four outcomes: a clean run, a failed check, an unreachable
# endpoint, and a backend the run cannot open.
set -uo pipefail

HERE="$(cd "$(dirname "$0")" && pwd)"
ROOT="$(cd "$HERE/../../.." && pwd)"
CLI="$ROOT/bin/rimsky"
: "${RIMSKY_IMAGE_TAG:?export RIMSKY_IMAGE_TAG=src-<tree hash> first}"
TAG="$RIMSKY_IMAGE_TAG"
WORK="$(mktemp -d)"

PRODUCER=exp-assumption-confexit-producer
SHAPES=exp-assumption-confexit-shapes

fails=0
ok()  { printf 'PASS  %s\n' "$*"; }
bad() { printf 'FAIL  %s\n' "$*"; fails=$((fails+1)); }
has() { case "$2" in *"$1"*) ok "$3";; *) bad "$3 (missing '$1')";; esac; }
check() { if [ "$2" = "$3" ]; then ok "$1 [$3]"; else bad "$1 expected [$2] got [$3]"; fi; }

free_port() { python3 -c 'import socket;s=socket.socket();s.bind(("127.0.0.1",0));print(s.getsockname()[1]);s.close()'; }

cleanup() { docker rm -f "$PRODUCER" "$SHAPES" >/dev/null 2>&1; rm -rf "$WORK"; }
trap cleanup EXIT

[ -x "$CLI" ] || { echo "build the CLI first: make cli"; exit 1; }

echo "--- machine-readable output"
for sub in executor claim-producer publisher validation data-processing blob-backend lifecycle-subscriber probe; do
  out="$("$CLI" conformance "$sub" --json 2>&1)"; rc=$?
  has "flag provided but not defined: -json" "$out" "'rimsky conformance $sub' has no --json flag"
  check "'$sub --json' exits as a usage error" 2 "$rc"
done
EXEC_HELP="$("$CLI" conformance executor --help 2>&1)"
case "$EXEC_HELP" in *warnings-as-errors*) bad "executor carries --warnings-as-errors";;
  *) ok "no conformance subcommand carries --warnings-as-errors either";; esac

mkdir -p "$WORK/data"
cat > "$WORK/fs.yml" <<'YAML'
root: /workspace/data
host: 0.0.0.0
grpc_port: 9100
http_port: 9110
sweep_interval_seconds: 60
YAML

P_PROD=$(free_port); P_SHAPES=$(free_port); P_CLOSED=$(free_port)
docker rm -f "$PRODUCER" "$SHAPES" >/dev/null 2>&1
docker run -d --name "$PRODUCER" -e RIMSKY_CLAIM_PRODUCER_FILESYSTEM_CONFIG=/etc/rimsky/fs.yml \
  -v "$WORK/fs.yml:/etc/rimsky/fs.yml:ro" -v "$WORK/data:/workspace/data" \
  -p "127.0.0.1:$P_PROD:9100" "rimsky-claim-producer-filesystem:$TAG" >/dev/null || exit 1
docker run -d --name "$SHAPES" -e RIMSKY_EXECUTOR_PORT_GRPC=9095 -p "127.0.0.1:$P_SHAPES:9095" \
  "rimsky-executor-verifier-shape-checks:$TAG" >/dev/null || exit 1
for p in "$P_PROD" "$P_SHAPES"; do until nc -z 127.0.0.1 "$p" >/dev/null 2>&1; do sleep 0.2; done; done

echo "--- a clean run"
out="$("$CLI" conformance claim-producer --endpoint "grpc://127.0.0.1:$P_PROD" --timeout 30s 2>&1)"; RC_PASS=$?
printf '    %s\n' "$(printf '%s' "$out" | tail -1)"
check "a conforming producer exits 0" 0 "$RC_PASS"

echo "--- a reachable implementation that fails a check"
out="$("$CLI" conformance validation --endpoint "grpc://127.0.0.1:$P_SHAPES" --role publisher --timeout 30s 2>&1)"; RC_FAILED=$?
printf '%s\n' "$out" | sed 's/^/    /'
has "checks failed" "$out" "the run names how many checks failed"
[ "$RC_FAILED" -ne 0 ] && ok "a failed check exits non-zero ($RC_FAILED)" || bad "a failed check exited 0"

echo "--- an endpoint the run never reached"
out="$("$CLI" conformance claim-producer --endpoint "grpc://127.0.0.1:$P_CLOSED" --timeout 5s 2>&1)"; RC_UNREACHED=$?
printf '%s\n' "$out" | sed 's/^/    /'
has "connection refused" "$out" "the message says the dial failed"
[ "$RC_UNREACHED" -ne 0 ] && ok "an unreachable endpoint exits non-zero ($RC_UNREACHED)" || bad "an unreachable endpoint exited 0"

echo "--- a backend the run cannot open"
out="$("$CLI" conformance blob-backend --backend filesystem --root /nonexistent/deep/path --timeout 10s 2>&1)"; RC_BADCFG=$?
printf '%s\n' "$out" | sed 's/^/    /'

echo "--- the codes side by side"
echo "    clean=$RC_PASS failed-check=$RC_FAILED unreachable=$RC_UNREACHED unopenable-backend=$RC_BADCFG usage-error=2"
if [ "$RC_FAILED" = "$RC_UNREACHED" ]; then
  ok "one code covers both outcomes, so CI cannot tell them apart from the status alone ($RC_FAILED)"
else
  bad "the codes differ: failed-check=$RC_FAILED unreachable=$RC_UNREACHED"
fi
check "the unopenable backend reuses the same code" "$RC_FAILED" "$RC_BADCFG"

echo
if [ "$fails" -eq 0 ]; then echo "EXPERIMENT PASS"; else echo "EXPERIMENT FAIL ($fails)"; fi
exit "$fails"
