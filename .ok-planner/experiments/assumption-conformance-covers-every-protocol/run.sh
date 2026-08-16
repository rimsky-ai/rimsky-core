#!/usr/bin/env bash
# Experiment: assumption-conformance-covers-every-protocol
#
# A third-party service author asks which protocols the shipped conformance
# kit can prove their implementation against. This run enumerates
# `rimsky conformance`'s subcommands, asks for the ones the protocol set
# suggests but the CLI may not carry, and checks how the two observability
# protocols are reached. The shipped CLI is the whole instrument, plus one
# bundled executor image to show the observability probe running.
set -uo pipefail

HERE="$(cd "$(dirname "$0")" && pwd)"
ROOT="$(cd "$HERE/../../.." && pwd)"
CLI="$ROOT/bin/rimsky"
: "${RIMSKY_IMAGE_TAG:?export RIMSKY_IMAGE_TAG=src-<tree hash> first}"
TAG="$RIMSKY_IMAGE_TAG"

EXECUTOR=exp-assumption-confcover-executor

fails=0
ok()  { printf 'PASS  %s\n' "$*"; }
bad() { printf 'FAIL  %s\n' "$*"; fails=$((fails+1)); }
has()   { case "$2" in *"$1"*) ok "$3";; *) bad "$3 (missing '$1')";; esac; }
hasnt() { case "$2" in *"$1"*) bad "$3 (found '$1')";; *) ok "$3";; esac; }

free_port() { python3 -c 'import socket;s=socket.socket();s.bind(("127.0.0.1",0));print(s.getsockname()[1]);s.close()'; }

cleanup() { docker rm -f "$EXECUTOR" >/dev/null 2>&1; }
trap cleanup EXIT

[ -x "$CLI" ] || { echo "build the CLI first: make cli"; exit 1; }

echo "--- the subcommands the kit offers"
USAGE="$("$CLI" conformance 2>&1)"; rc=$?
printf '    %s\n' "$USAGE"
[ "$rc" -ne 0 ] && ok "a bare 'rimsky conformance' exits non-zero with its usage line ($rc)" \
  || bad "a bare 'rimsky conformance' exited 0"
for sub in executor claim-producer publisher validation data-processing blob-backend lifecycle-subscriber probe; do
  has "$sub" "$USAGE" "the usage line offers '$sub'"
done

echo "--- the protocols with no subcommand of their own"
for sub in host-agent executor-observability claim-producer-observability; do
  out="$("$CLI" conformance "$sub" --endpoint grpc://127.0.0.1:1 2>&1)"; rc=$?
  printf '    %s\n' "$out"
  has "unknown subcommand \"$sub\"" "$out" "'rimsky conformance $sub' is not a subcommand"
  [ "$rc" -eq 2 ] && ok "'$sub' exits 2 as a usage error" || bad "'$sub' exited $rc, not 2"
done

echo "--- the two observability protocols are reached by a flag on their sibling's subcommand"
EXEC_HELP="$("$CLI" conformance executor --help 2>&1)"
has "check-observability" "$EXEC_HELP" "'rimsky conformance executor' carries --check-observability"
CP_HELP="$("$CLI" conformance claim-producer --help 2>&1)"
has "check-observability" "$CP_HELP" "'rimsky conformance claim-producer' carries --check-observability"

P_EXEC=$(free_port)
docker rm -f "$EXECUTOR" >/dev/null 2>&1
docker run -d --name "$EXECUTOR" -p "127.0.0.1:$P_EXEC:9091" \
  "rimsky-executor-http-node:$TAG" >/dev/null || exit 1
until nc -z 127.0.0.1 "$P_EXEC" >/dev/null 2>&1; do sleep 0.2; done
out="$("$CLI" conformance executor --endpoint "grpc://127.0.0.1:$P_EXEC" --allow-live --check-observability --timeout 20s 2>&1)"; rc=$?
printf '%s\n' "$out" | sed 's/^/    /'
has "observability: ok" "$out" "the flag really drives the ExecutorObservability probe"
[ "$rc" -eq 0 ] && ok "the run exits 0" || bad "the run exited $rc"

echo "--- no subcommand covers the host-agent protocol under any spelling"
for sub in hostagent host_agent agent; do
  out="$("$CLI" conformance "$sub" 2>&1)"
  hasnt "Usage of rimsky conformance" "$out" "'$sub' is not a subcommand either"
done

echo
echo "note: the shipped protocol set is ten .proto files declaring nine gRPC services"
echo "      (events.proto declares none): Executor, ExecutorObservability, ClaimProducer,"
echo "      ClaimProducerObservability, DataProcessing, Publisher, Validation,"
echo "      LifecycleSubscriber, HostAgent."
echo
if [ "$fails" -eq 0 ]; then echo "EXPERIMENT PASS"; else echo "EXPERIMENT FAIL ($fails)"; fi
exit "$fails"
