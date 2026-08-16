#!/usr/bin/env bash
# Experiment: spawned-local-services
#
# A consumer-project author ships a binary and one command. The run checks that:
#   - `rimsky run <template> --service <name>=<path>` drives the template to
#     terminal with that binary serving the node, with no service installed, no
#     daemon started, no configuration file written by hand and no docker
#   - the binary the command spawned is gone when the command returns
#   - a second invocation spawns a new process rather than reusing the first
#   - the same template without the --service flag does not run, so the binding
#     is what supplies the service rather than something already installed
#
# The whole run happens inside a scrubbed process environment (`env -i`, an
# empty HOME, no rimsky variables), so nothing pre-existing on the machine can
# be supplying the service.

set -u
cd "$(dirname "$0")"
ROOT=$(cd ../../.. && pwd)
CLI="$ROOT/bin/rimsky"
PEERSRC="$ROOT/.ok-planner/experiments/host-agent-late-bind-all-protocols/peer"
WORK=$(mktemp -d)

fail=0
note() { printf '%s\n' "$*"; }
check() {
  if [ "$2" = "$3" ]; then printf 'PASS  %-62s %s\n' "$1" "$3"
  else printf 'FAIL  %-62s expected [%s] got [%s]\n' "$1" "$2" "$3"; fail=1; fi
}
cleanup() { rm -rf "$WORK"; }
trap cleanup EXIT

note "== the binary the consumer project ships =="
cp -r "$PEERSRC" "$WORK/peer"
sed "s|RIMSKY_PROTOCOLS_PATH|$ROOT/lib/protocols|" "$WORK/peer/go.mod.tmpl" > "$WORK/peer/go.mod"
rm "$WORK/peer/go.mod.tmpl"
( cd "$WORK/peer" && GOFLAGS=-mod=mod GOPROXY=off GOSUMDB=off go build -o "$WORK/devsvc" . ) || exit 1
check "the consumer's binary built" yes yes

run_case() { # run_case <case name> <extra args...> -> exit code, transcript at $WORK/<case>.txt
  local name=$1; shift
  local dir="$WORK/$name"
  mkdir -p "$dir/home"
  cp template.yml "$dir/"
  ( cd "$dir" && env -i PATH=/usr/bin:/bin HOME="$dir/home" TMPDIR=/tmp \
      "$CLI" run template.yml "$@" >"$WORK/$name.out" 2>"$WORK/$name.txt" )
  echo $?
}
served_pid() { grep -oE 'execute node=worker run_scope=[0-9a-f-]+ pid=[0-9]+' "$1" | grep -oE 'pid=[0-9]+' | cut -d= -f2 | head -1; }

note
note "== one command, one binary, nothing installed =="
BEFORE=$(pgrep -f "$WORK/devsvc" | wc -l | tr -d ' ')
check "no process is running the binary before the command" 0 "$BEFORE"
RC1=$(run_case first --service "devsvc=$WORK/devsvc")
note "the command's own transcript:"
grep -E 'peer |execute node=|^instance |terminal/|rimsky run:' "$WORK/first.txt" | sed 's/^/    /'
check "the command exited successfully" 0 "$RC1"
check "the node reached terminal success" yes \
  "$(grep -q 'terminal/success' "$WORK/first.txt" && echo yes || echo no)"
check "the consumer's binary served the node" yes \
  "$(grep -q 'execute node=worker' "$WORK/first.txt" && echo yes || echo no)"
PID1=$(served_pid "$WORK/first.txt")
check "the transcript names the process that served it" yes \
  "$([ -n "$PID1" ] && echo yes || echo no)"

note
note "== the spawned binary disappears with the run =="
check "the process that served the run is gone" no \
  "$(kill -0 "$PID1" 2>/dev/null && echo yes || echo no)"
check "no process is left running the binary" 0 \
  "$(pgrep -f "$WORK/devsvc" | wc -l | tr -d ' ')"
check "no rimsky process from this command is left behind" 0 \
  "$(pgrep -f "$CLI run template.yml" | wc -l | tr -d ' ')"

note
note "== a second run spawns its own process =="
RC2=$(run_case second --service "devsvc=$WORK/devsvc")
check "the second command exited successfully" 0 "$RC2"
PID2=$(served_pid "$WORK/second.txt")
check "the second run was served by a different process" yes \
  "$([ -n "$PID2" ] && [ "$PID1" != "$PID2" ] && echo yes || echo no)"
note "    first run served by pid $PID1, second by pid $PID2"
check "that process is gone too" no \
  "$(kill -0 "$PID2" 2>/dev/null && echo yes || echo no)"

note
note "== without the binding, the service is not there =="
RC3=$(run_case third)
check "the same template without --service does not run" yes \
  "$([ "$RC3" != 0 ] && echo yes || echo no)"
note "    it said:"
grep -E 'rimsky run:|unknown executor|unresolved|validation' "$WORK/third.txt" | tail -3 | sed 's/^/        /'

note
if [ "$fail" = 0 ]; then note "EXPERIMENT PASS"; else note "EXPERIMENT FAIL"; fi
exit "$fail"
