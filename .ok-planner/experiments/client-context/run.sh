#!/bin/bash
# Experiment: story client-context.
#
# Boots two independent rimsky control-api deployments, registers both in the
# CLI's context list, and drives commands at each by switching the current
# context only -- no --endpoint flag anywhere after the two `ctx add` calls.
#
# Requires: docker, and a rimsky CLI binary (RIMSKY_BIN, default ./bin/rimsky).
# RIMSKY_IMAGE_TAG selects the rimsky-all-in-one image tag.
set -u

RIMSKY_BIN=${RIMSKY_BIN:?set RIMSKY_BIN to the rimsky CLI binary}
TAG=${RIMSKY_IMAGE_TAG:?set RIMSKY_IMAGE_TAG}
PORT_A=${PORT_A:-18120}
PORT_B=${PORT_B:-18121}
NAME_A=rimsky-exp-ctx-a
NAME_B=rimsky-exp-ctx-b

WORK=$(mktemp -d)
export HOME="$WORK/home"
mkdir -p "$HOME"
unset RIMSKY_CONTROL_API_URL RIMSKY_API_KEY RIMSKY_CONTEXT

cleanup() { docker rm -f "$NAME_A" "$NAME_B" >/dev/null 2>&1; }
trap cleanup EXIT

fail=0
check() { # check <label> <expected-substring> <actual>
  if printf '%s' "$3" | grep -qF -- "$2"; then
    echo "PASS  $1"
  else
    echo "FAIL  $1: expected to find [$2] in:"
    printf '%s\n' "$3" | sed 's/^/        /'
    fail=1
  fi
}

boot() { # boot <name> <port>
  docker rm -f "$1" >/dev/null 2>&1
  docker run -d --name "$1" -p "$2:8080" "rimsky-all-in-one:$TAG" >/dev/null || exit 1
  for _ in $(seq 1 90); do
    if [ "$(curl -s -m 2 -o /dev/null -w '%{http_code}' "http://127.0.0.1:$2/v1/health")" = 200 ]; then return 0; fi
    sleep 1
  done
  echo "FAIL  boot $1"; exit 1
}

boot "$NAME_A" "$PORT_A"
boot "$NAME_B" "$PORT_B"

cat > "$WORK/tpl-a.yml" <<'EOF'
name: only-on-alpha
version: "1"
nodes:
  - type: verify
    executor: verifier-shape-checks
EOF
cat > "$WORK/tpl-b.yml" <<'EOF'
name: only-on-beta
version: "1"
nodes:
  - type: verify
    executor: verifier-shape-checks
EOF

# Seed each deployment with a distinct template, addressed explicitly. After
# this point no command names an endpoint.
HASH_A=$("$RIMSKY_BIN" register "$WORK/tpl-a.yml" --endpoint "http://127.0.0.1:$PORT_A" -o json | tr ',' '\n' | grep -o 'sha256-[0-9a-f]*' | head -1)
HASH_B=$("$RIMSKY_BIN" register "$WORK/tpl-b.yml" --endpoint "http://127.0.0.1:$PORT_B" -o json | tr ',' '\n' | grep -o 'sha256-[0-9a-f]*' | head -1)
echo "alpha template hash: $HASH_A"
echo "beta  template hash: $HASH_B"
[ -n "$HASH_A" ] && [ -n "$HASH_B" ] && [ "$HASH_A" != "$HASH_B" ] || { echo "FAIL  seeding produced no distinct hashes"; exit 1; }

echo "== ctx add =="
out=$("$RIMSKY_BIN" ctx add alpha --endpoint "http://127.0.0.1:$PORT_A" 2>&1)
check "ctx add alpha" "added context alpha" "$out"
out=$("$RIMSKY_BIN" ctx add beta --endpoint "http://127.0.0.1:$PORT_B" 2>&1)
check "ctx add beta" "added context beta" "$out"

echo "== ctx list (inspect) =="
out=$("$RIMSKY_BIN" ctx list 2>&1)
check "ctx list shows alpha" "alpha" "$out"
check "ctx list shows beta" "beta" "$out"
check "ctx list shows alpha endpoint" "127.0.0.1:$PORT_A" "$out"
check "ctx list shows beta endpoint" "127.0.0.1:$PORT_B" "$out"

echo "== ctx current =="
out=$("$RIMSKY_BIN" ctx current 2>&1)
check "current is alpha (first added)" "alpha" "$out"

echo "== command routed by current context, no flag =="
out=$("$RIMSKY_BIN" ls templates -o json 2>&1)
check "alpha template visible" "$HASH_A" "$out"
if printf '%s' "$out" | grep -qF "$HASH_B"; then
  echo "FAIL  alpha listing must not contain beta's template"; fail=1
else
  echo "PASS  alpha listing excludes beta's template"
fi

echo "== ctx use beta =="
out=$("$RIMSKY_BIN" ctx use beta 2>&1)
check "switch to beta" "beta" "$out"
out=$("$RIMSKY_BIN" ctx current 2>&1)
check "current is beta" "beta" "$out"
out=$("$RIMSKY_BIN" ls templates -o json 2>&1)
check "beta template visible" "$HASH_B" "$out"
if printf '%s' "$out" | grep -qF "$HASH_A"; then
  echo "FAIL  beta listing must not contain alpha's template"; fail=1
else
  echo "PASS  beta listing excludes alpha's template"
fi

echo "== ctx rm =="
out=$("$RIMSKY_BIN" ctx rm alpha 2>&1)
rc=$?
if [ $rc -eq 0 ]; then echo "PASS  ctx rm alpha exit 0"; else echo "FAIL  ctx rm alpha exit $rc: $out"; fail=1; fi
out=$("$RIMSKY_BIN" ctx list 2>&1)
if printf '%s' "$out" | grep -qE '(^|[[:space:]])alpha([[:space:]]|$)'; then
  echo "FAIL  alpha still listed after rm"; fail=1
else
  echo "PASS  alpha gone after rm"
fi
check "beta still listed" "beta" "$out"

echo
if [ $fail -eq 0 ]; then echo "RESULT: PASS"; else echo "RESULT: FAIL"; fi
exit $fail
