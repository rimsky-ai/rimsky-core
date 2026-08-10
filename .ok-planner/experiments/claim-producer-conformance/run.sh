#!/usr/bin/env bash
# Experiment: claim-producer-conformance
# A claim-producer author's own producer is stood up three ways -- honest,
# one that rejects a retried terminal verb, and one that serialises a reader
# behind a writer while advertising staged_async -- and `rimsky conformance
# claim-producer` is pointed at each. The run shows the suite driving Open /
# Commit / Abandon / Release including the retry, running the serialization-9b
# probe, printing one pass/fail row per check, and exiting non-zero when a
# check fails. Public CLI only; no docker.
set -uo pipefail

HERE="$(cd "$(dirname "$0")" && pwd)"
ROOT="$(cd "$HERE/../../.." && pwd)"
CLI="$ROOT/bin/rimsky"
WORK="$(mktemp -d)"
PORT_HONEST="${PORT_HONEST:-19401}"
PORT_RETRY="${PORT_RETRY:-19402}"
PORT_SERIAL="${PORT_SERIAL:-19403}"

fails=0
ok()  { echo "PASS  $*"; }
bad() { echo "FAIL  $*"; fails=$((fails+1)); }
has() { case "$2" in *"$1"*) ok "$3";; *) bad "$3 (missing '$1')";; esac; }
hasnt() { case "$2" in *"$1"*) bad "$3 (found '$1')";; *) ok "$3";; esac; }

PIDS=()
cleanup() { for p in "${PIDS[@]:-}"; do kill "$p" >/dev/null 2>&1; done; rm -rf "$WORK"; }
trap cleanup EXIT

go build -o "$WORK/producer" "$HERE" || { echo "build failed"; exit 1; }

start() {
  "$WORK/producer" -grpc "127.0.0.1:$1" "${@:2}" >"$WORK/prod-$1.log" 2>&1 &
  PIDS+=("$!")
  until nc -z 127.0.0.1 "$1" >/dev/null 2>&1; do sleep 0.1; done
}

start "$PORT_HONEST" -name honest -semantics staged_async
start "$PORT_RETRY"  -name retry-hostile -semantics staged_async -non-idempotent-terminals
start "$PORT_SERIAL" -name serialising -semantics staged_async -serialize-readers

echo "--- an honest producer passes every check"
out="$("$CLI" conformance claim-producer --endpoint "grpc://127.0.0.1:$PORT_HONEST" --timeout 60s 2>&1)"
rc=$?
printf '%s\n' "$out" | sed 's/^/    /'
[ "$rc" -eq 0 ] && ok "the suite exits 0 against the honest producer" || bad "the suite exited $rc against the honest producer"
for row in Capabilities OpenFirst Commit Abandon Release TerminalIdempotency AbandonTerminalIdempotency ReleaseTerminalIdempotency Serialization9b; do
  has "ok    $row" "$out" "check $row reported ok"
done
hasnt "FAIL" "$out" "no check failed"

echo "--- a producer that rejects a retried terminal verb fails exactly the idempotency checks"
out="$("$CLI" conformance claim-producer --endpoint "grpc://127.0.0.1:$PORT_RETRY" --timeout 60s 2>&1)"
rc=$?
printf '%s\n' "$out" | sed 's/^/    /'
[ "$rc" -ne 0 ] && ok "the suite exits non-zero when a check fails ($rc)" || bad "the suite exited 0 despite a dishonest producer"
has "FAIL  TerminalIdempotency" "$out" "the retried Commit is reported as a named failing check"
has "FAIL  AbandonTerminalIdempotency" "$out" "the retried Abandon is reported as a named failing check"
has "FAIL  ReleaseTerminalIdempotency" "$out" "the retried Release is reported as a named failing check"
has "ok    Commit" "$out" "the first Commit still reports ok, so the report is per check"
has "checks failed" "$out" "the run summarises how many of its checks failed"

echo "--- a producer that serialises a reader behind a writer fails the serialization-9b probe"
out="$("$CLI" conformance claim-producer --endpoint "grpc://127.0.0.1:$PORT_SERIAL" --timeout 60s 2>&1)"
rc=$?
printf '%s\n' "$out" | sed 's/^/    /'
[ "$rc" -ne 0 ] && ok "the suite exits non-zero for the dishonest staged_async producer ($rc)" || bad "the suite exited 0 despite internal serialization"
has "FAIL  Serialization9b" "$out" "the serialization-9b probe is reported as a named failing check"
has "reader-lease pattern is forbidden for staged_async" "$out" "the failure says what the producer did wrong"
has "ok    Commit" "$out" "the terminal verbs still report ok, so 9b is an independent check"

echo
if [ "$fails" -eq 0 ]; then echo "EXPERIMENT PASS"; else echo "EXPERIMENT FAIL ($fails)"; fi
exit "$fails"
