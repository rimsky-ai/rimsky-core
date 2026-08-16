#!/bin/bash
# Experiment: story script-friendly-outcome.
#
# A surrounding script branches on the exit status of a one-shot run, never
# on its output. Three runs exercise the three outcome classes:
#
#   all succeeded  -> exit 0
#   something failed -> exit 1
#   the run was bounded out (--timeout expired) -> exit 2
#
# The branching below is a real `case` on $? with the transcript discarded,
# so a pass demonstrates the classes are distinguishable without parsing logs.
#
# Requires: a rimsky CLI binary (RIMSKY_BIN), python3 (the slow upstream the
# bounded-out case waits on). No docker.
set -u

RIMSKY_BIN=${RIMSKY_BIN:?set RIMSKY_BIN to the rimsky CLI binary}
HERE=$(cd "$(dirname "$0")" && pwd)
free_port() { python3 -c 'import socket;s=socket.socket();s.bind(("127.0.0.1",0));print(s.getsockname()[1]);s.close()'; }
SLOW_PORT=${SLOW_PORT:-$(free_port)}
WORK=$(mktemp -d)
fail=0

python3 "$HERE/slow-server.py" 20 "$SLOW_PORT" &
SLOW_PID=$!
cleanup() { kill "$SLOW_PID" 2>/dev/null; }
trap cleanup EXIT
until python3 -c "import socket,sys;s=socket.socket();sys.exit(0 if s.connect_ex(('127.0.0.1',$SLOW_PORT))==0 else 1)"; do sleep 0.1; done

branch() { # branch <label> <expected-class> <manifest> [extra args...]
  local label=$1 want=$2 manifest=$3; shift 3
  local dir="$WORK/$label"
  mkdir -p "$dir/home"
  cp "$HERE"/*.yml "$dir/"
  sed -i.bak "s|127.0.0.1:18777|127.0.0.1:$SLOW_PORT|" "$dir/template-slow.yml"
  rm -f "$dir/template-slow.yml.bak"
  ( cd "$dir" && env -i PATH=/usr/bin:/bin HOME="$dir/home" TMPDIR=/tmp \
      RIMSKY_EXECUTOR_HTTP_NODE_EGRESS_ALLOWLIST=127.0.0.0/8 \
      "$RIMSKY_BIN" compose run "$manifest" "$@" >/dev/null 2>"$dir/stderr.txt" )
  local rc=$?
  local got
  case $rc in
    0) got=all-succeeded ;;
    1) got=something-failed ;;
    2) got=bounded-out ;;
    *) got="unclassified($rc)" ;;
  esac
  if [ "$got" = "$want" ]; then
    echo "PASS  $label: exit $rc -> $got"
  else
    echo "FAIL  $label: exit $rc -> $got, want $want"
    tail -5 "$dir/stderr.txt" | sed 's/^/        /'
    fail=1
  fi
}

branch all-success     all-succeeded    rimsky-compose-all-success.yml
branch one-failure     something-failed rimsky-compose-one-failure.yml
branch bounded-out     bounded-out      rimsky-compose-slow.yml --timeout 3s

echo
if [ $fail -eq 0 ]; then echo "RESULT: PASS"; else echo "RESULT: FAIL"; fi
exit $fail
