#!/usr/bin/env bash
# Experiment: assumption-every-protocol-has-capabilities
#
# A service author reads the ten shipped .proto files, sees `Capabilities` on
# several services, and asks whether the handshake is universal. This run
# answers it by calling `/rimsky.v1.<Service>/Capabilities` on the bundled
# implementation of each protocol and recording the gRPC status. A server that
# serves the service but not the method answers Unimplemented with "unknown
# method Capabilities for service <name>", which is the observation that
# settles the question one protocol at a time.
#
# Instrument: the published protocol surface only -- the shipped gRPC method
# names, dialed by a probe built against github.com/rimsky-ai/rimsky-core/lib/protocols,
# against the bundled service images.
set -uo pipefail

HERE="$(cd "$(dirname "$0")" && pwd)"
ROOT="$(cd "$HERE/../../.." && pwd)"
: "${RIMSKY_IMAGE_TAG:?export RIMSKY_IMAGE_TAG=src-<tree hash> first}"
TAG="$RIMSKY_IMAGE_TAG"
WORK="$(mktemp -d)"

HTTPNODE=exp-assumption-caps-httpnode
SHAPES=exp-assumption-caps-shapes
FSPROD=exp-assumption-caps-fsproducer
FSLC=exp-assumption-caps-fslifecycle
CRON=exp-assumption-caps-cron
PROXY=exp-assumption-caps-proxy

fails=0
ok()  { printf 'PASS  %s\n' "$*"; }
bad() { printf 'FAIL  %s\n' "$*"; fails=$((fails+1)); }

free_port() { python3 -c 'import socket;s=socket.socket();s.bind(("127.0.0.1",0));print(s.getsockname()[1]);s.close()'; }

cleanup() {
  docker rm -f "$HTTPNODE" "$SHAPES" "$FSPROD" "$FSLC" "$CRON" "$PROXY" >/dev/null 2>&1
  rm -rf "$WORK"
}
trap cleanup EXIT

cp -r "$HERE/probe" "$WORK/probe"
sed "s|RIMSKY_PROTOCOLS_PATH|$ROOT/lib/protocols|" "$WORK/probe/go.mod.tmpl" > "$WORK/probe/go.mod"
rm "$WORK/probe/go.mod.tmpl"
(cd "$WORK/probe" && GOFLAGS=-mod=mod go build -o "$WORK/probe-bin" .) || { echo "probe build failed"; exit 1; }

mkdir -p "$WORK/data"
cat > "$WORK/fs.yml" <<'YAML'
root: /workspace/data
host: 0.0.0.0
grpc_port: 9100
http_port: 9110
sweep_interval_seconds: 60
YAML
sed 's|^sweep_interval_seconds: 60|enable_lifecycle: true\nsweep_interval_seconds: 60|' "$WORK/fs.yml" > "$WORK/fs-lc.yml"

P_EXEC=$(free_port); P_SHAPES=$(free_port); P_PROD=$(free_port); P_LC=$(free_port); P_PUB=$(free_port); P_AGENT=$(free_port)

docker rm -f "$HTTPNODE" "$SHAPES" "$FSPROD" "$FSLC" "$CRON" "$PROXY" >/dev/null 2>&1
docker run -d --name "$HTTPNODE" -p "127.0.0.1:$P_EXEC:9091" \
  "rimsky-executor-http-node:$TAG" >/dev/null || exit 1
docker run -d --name "$SHAPES" -e RIMSKY_EXECUTOR_PORT_GRPC=9095 -p "127.0.0.1:$P_SHAPES:9095" \
  "rimsky-executor-verifier-shape-checks:$TAG" >/dev/null || exit 1
docker run -d --name "$FSPROD" -e RIMSKY_CLAIM_PRODUCER_FILESYSTEM_CONFIG=/etc/rimsky/fs.yml \
  -v "$WORK/fs.yml:/etc/rimsky/fs.yml:ro" -v "$WORK/data:/workspace/data" \
  -p "127.0.0.1:$P_PROD:9100" "rimsky-claim-producer-filesystem:$TAG" >/dev/null || exit 1
docker run -d --name "$FSLC" -e RIMSKY_CLAIM_PRODUCER_FILESYSTEM_CONFIG=/etc/rimsky/fs.yml \
  -v "$WORK/fs-lc.yml:/etc/rimsky/fs.yml:ro" -v "$WORK/data:/workspace/data" \
  -p "127.0.0.1:$P_LC:9100" "rimsky-claim-producer-filesystem:$TAG" >/dev/null || exit 1
docker run -d --name "$CRON" -e RIMSKY_SENSOR_CRON_PORT=9081 -e RIMSKY_CONTROL_API_URL=http://127.0.0.1:1 \
  -p "127.0.0.1:$P_PUB:9081" "rimsky-sensor-cron:$TAG" >/dev/null || exit 1
docker run -d --name "$PROXY" -e RIMSKY_CONTROL_API_URL=http://127.0.0.1:1 \
  -p "127.0.0.1:$P_AGENT:9090" "rimsky-host-agent-proxy:$TAG" >/dev/null || exit 1

for p in "$P_EXEC" "$P_SHAPES" "$P_PROD" "$P_LC" "$P_PUB" "$P_AGENT"; do
  until nc -z 127.0.0.1 "$p" >/dev/null 2>&1; do sleep 0.2; done
done

probe() { "$WORK/probe-bin" -addr "127.0.0.1:$1" -method "$2"; }

answers() {
  out="$(probe "$2" "/rimsky.v1.$1/Capabilities")"
  printf '    %s\n' "$out"
  case "$out" in
    OK*) ok "$1 answers Capabilities";;
    *)   bad "$1 was expected to answer Capabilities, got: $out";;
  esac
}

silent() {
  out="$(probe "$2" "/rimsky.v1.$1/Capabilities")"
  printf '    %s\n' "$out"
  case "$out" in
    "Unimplemented"*"unknown method Capabilities for service rimsky.v1.$1"*)
      ok "$1 is served here and has no Capabilities method";;
    *) bad "$1 probe did not read as a served-but-missing method: $out";;
  esac
}

echo "--- the protocols that answer the handshake"
answers ClaimProducer "$P_PROD"
answers ClaimProducerObservability "$P_PROD"
answers ExecutorObservability "$P_EXEC"
answers Publisher "$P_PUB"

echo "--- the protocols served by the same bundled processes that do not"
silent Executor "$P_EXEC"
silent Executor "$P_SHAPES"
silent Validation "$P_SHAPES"
silent LifecycleSubscriber "$P_LC"
silent HostAgent "$P_AGENT"

echo "--- the missing method is a method gap, not an unreachable service"
out="$(probe "$P_LC" /rimsky.v1.LifecycleSubscriber/OnInstanceCreated)"
printf '    %s\n' "$out"
case "$out" in OK*) ok "the same LifecycleSubscriber endpoint answers its lifecycle verbs";;
  *) bad "expected LifecycleSubscriber.OnInstanceCreated to answer, got: $out";; esac
out="$(probe "$P_PUB" /rimsky.v1.Publisher/ListSubscriptions)"
printf '    %s\n' "$out"
case "$out" in OK*) ok "the publisher answers both Capabilities and its own verbs";;
  *) bad "expected Publisher.ListSubscriptions to answer, got: $out";; esac

echo
echo "note: DataProcessing declares Capabilities in the shipped proto and no bundled"
echo "      service implements it, so no live probe covers that protocol."
echo
if [ "$fails" -eq 0 ]; then echo "EXPERIMENT PASS"; else echo "EXPERIMENT FAIL ($fails)"; fi
exit "$fails"
