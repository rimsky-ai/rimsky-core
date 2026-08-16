#!/bin/bash
# Experiment: assumption cli-help-on-every-subcommand.
#
# Walks every node of the shipped CLI's verb tree with --help and records the
# exit code and the first line of what was printed. The population is the 85
# nodes reachable from `rimsky --help`: the root, 14 family nodes, and 70
# leaves across the dev-loop, literal-API, auth, ctx, compose, agent, and
# conformance families. Two claims are checked per node: it prints that node's
# own usage, and it exits zero. No server is involved — help never dials.
#
# Requires: a rimsky CLI binary (RIMSKY_BIN, default ./bin/rimsky).
set -u

RIMSKY_BIN=${RIMSKY_BIN:?set RIMSKY_BIN to the rimsky CLI binary}
WORK=$(mktemp -d); export HOME="$WORK/home"; mkdir -p "$HOME"
unset RIMSKY_API_KEY RIMSKY_CONTEXT
export RIMSKY_CONTROL_API_URL=http://127.0.0.1:1

fail=0

FAMILIES=( "template" "tag" "instance" "node" "admin" "parked" "messages" "asset"
           "lineage" "ctx" "auth" "agent" "compose" "conformance" )
LEAVES=(
  "version" "health" "run" "register" "deploy" "undeploy" "instantiate" "rm-instance"
  "ls" "ls templates" "ls instances" "ls tags" "logs" "watch"
  "template register" "template lint" "template list" "template get"
  "template deploy" "template undeploy" "template rm"
  "tag create" "tag list" "tag get" "tag mv" "tag rm"
  "instance create" "instance list" "instance get" "instance status"
  "instance delete" "instance kill" "instance nodes" "instance events"
  "node get" "admin reset" "parked list" "messages tail" "messages show"
  "asset list" "asset show" "asset versions" "asset delete" "asset lineage"
  "lineage prune" "ctx list" "ctx use" "ctx add" "ctx rm" "ctx current"
  "auth init" "auth login" "auth create-key" "auth list" "auth show"
  "auth revoke" "auth rotate" "auth status"
  "agent start" "agent status" "agent stop"
  "compose up" "compose down" "compose plan" "compose status" "compose run"
  "conformance executor" "conformance claim-producer" "conformance publisher"
  "conformance validation" "conformance data-processing" "conformance blob-backend"
  "conformance lifecycle-subscriber" "conformance probe"
)

nonzero=(); wrongname=()

probe() { # probe <node...>
  local v="$1" out rc
  read -r -a words <<<"$v"
  out=$("$RIMSKY_BIN" "${words[@]}" --help 2>&1); rc=$?
  local first; first=$(printf '%s\n' "$out" | head -1)
  printf '     exit %d  rimsky %-32s %s\n' "$rc" "$v" "$(printf '%s' "$first" | cut -c1-58)"
  [ $rc -ne 0 ] && nonzero+=("$v")
  # does the printed usage name this node, or some other one?
  local leaf=${v##* }
  case "$first" in
    *"$leaf"*|*"rimsky $v"*) ;;
    *) wrongname+=("$v → $first") ;;
  esac
}

echo "== root =="
out=$("$RIMSKY_BIN" --help 2>&1); rc=$?
echo "     exit $rc  rimsky --help"
[ $rc -eq 0 ] || { echo "FAIL  root --help exits $rc"; fail=1; }

echo
echo "== family nodes (${#FAMILIES[@]}) =="
for v in "${FAMILIES[@]}"; do probe "$v"; done

echo
echo "== leaves (${#LEAVES[@]}) =="
for v in "${LEAVES[@]}"; do probe "$v"; done

total=$(( ${#FAMILIES[@]} + ${#LEAVES[@]} + 1 ))
echo
echo "== verdict over $total nodes =="
if [ ${#nonzero[@]} -eq 0 ]; then
  echo "PASS  every node exits zero on --help"
else
  echo "FAIL  ${#nonzero[@]} of $total nodes exit non-zero on --help"
  fail=1
fi
if [ ${#wrongname[@]} -eq 0 ]; then
  echo "PASS  every node's help names that node"
else
  echo "FAIL  ${#wrongname[@]} of $total nodes print another node's usage:"
  printf '        %s\n' "${wrongname[@]}"
  fail=1
fi

echo
if [ $fail -eq 0 ]; then echo "RESULT: PASS"; else echo "RESULT: FAIL"; fi
exit $fail
