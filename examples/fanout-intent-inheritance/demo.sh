#!/usr/bin/env bash
# Copyright © 2026 Fall Guy Consulting.
# Licensed under the Apache License, Version 2.0. See LICENSE.apache at the
# repo root, or http://www.apache.org/licenses/LICENSE-2.0.

# @story: fanout-intent-inheritance

set -euo pipefail

SCRIPT_DIR="$( cd "$( dirname "${BASH_SOURCE[0]}" )" && pwd )"
TEMPLATE_R="${SCRIPT_DIR}/template-readonly.yaml"
TEMPLATE_RW="${SCRIPT_DIR}/template-readwrite.yaml"

RIMSKY_ENDPOINT="${RIMSKY_ENDPOINT:-http://127.0.0.1:8080}"
POLL_BUDGET_SECONDS="${POLL_BUDGET_SECONDS:-180}"

RIMSKY_DSN="${RIMSKY_DSN:-postgres://rimsky:rimsky@localhost:5432/rimsky?sslmode=disable}"

which yq >/dev/null 2>&1 || { echo "fanout-intent-inheritance: yq not on PATH" >&2; exit 2; }
which curl >/dev/null 2>&1 || { echo "fanout-intent-inheritance: curl not on PATH" >&2; exit 2; }
which jq >/dev/null 2>&1 || { echo "fanout-intent-inheritance: jq not on PATH" >&2; exit 2; }
which psql >/dev/null 2>&1 || { echo "fanout-intent-inheritance: psql not on PATH (needed to inspect rimsky_claim_handles directly; no HTTP API exposes the intent column today)" >&2; exit 2; }

register_and_deploy() {
    local path="$1"
    local label="$2"
    local spec_json
    spec_json="$( yq -o=json '.' "${path}" )"
    local body
    body="$( jq -n --argjson spec "${spec_json}" '{spec: $spec}' )"
    local out
    out="$( curl -sS -X POST -H 'Content-Type: application/json' \
        --data "${body}" "${RIMSKY_ENDPOINT}/v1/templates" )"
    local tid
    tid="$( echo "${out}" | jq -r '.template_id' )"
    if [ -z "${tid}" ] || [ "${tid}" = "null" ]; then
        echo "fanout-intent-inheritance: register ${label} failed: ${out}" >&2
        return 1
    fi
    curl -sS -X POST -H 'Content-Type: application/json' --data '{}' \
        "${RIMSKY_ENDPOINT}/v1/templates/${tid}/deploy" >/dev/null
    echo "${tid}"
}

create_instance() {
    local tid="$1"
    local key="$2"
    local out
    out="$( curl -sS -X POST -H 'Content-Type: application/json' \
        --data "{\"template\": \"${tid}\", \"instance_key\": \"${key}\", \"target_agent\": \"demo-agent\"}" \
        "${RIMSKY_ENDPOINT}/v1/instances" )"
    local iid
    iid="$( echo "${out}" | jq -r '.instance_id' )"
    if [ -z "${iid}" ] || [ "${iid}" = "null" ]; then
        echo "fanout-intent-inheritance: instance create failed: ${out}" >&2
        return 1
    fi
    echo "${iid}"
}

post_seed() {
    local iid="$1"
    local type="$2"
    local items="$3"
    local k
    k="fii-$( uuidgen 2>/dev/null || echo "${iid}-${RANDOM}" )"
    curl -sS -X POST -H 'Content-Type: application/json' \
        -H "Idempotency-Key: ${k}" \
        --data "{\"type\":\"${type}\",\"payload\":{\"items\":${items}}}" \
        "${RIMSKY_ENDPOINT}/v1/instances/${iid}/messages" >/dev/null
}

count_sub_claims_with_intent_for_instance() {
    local iid="$1"
    local want_intent="$2"
    psql "${RIMSKY_DSN}" -tA -v ON_ERROR_STOP=1 -c "
        SELECT count(*)
          FROM rimsky_claim_handles ch
          JOIN rimsky_node_runs    nr ON nr.id = ch.node_run_id
          JOIN rimsky_frames       f  ON f.frame_id = nr.frame_id
         WHERE f.instance_id = '${iid}'
           AND ch.parent_claim_handle_id IS NOT NULL
           AND ch.intent = '${want_intent}'
    " | tr -d '[:space:]'
}

count_sub_claims_with_intent_mismatch_for_instance() {
    local iid="$1"
    local want_intent="$2"
    psql "${RIMSKY_DSN}" -tA -v ON_ERROR_STOP=1 -c "
        SELECT count(*)
          FROM rimsky_claim_handles ch
          JOIN rimsky_node_runs    nr ON nr.id = ch.node_run_id
          JOIN rimsky_frames       f  ON f.frame_id = nr.frame_id
         WHERE f.instance_id = '${iid}'
           AND ch.parent_claim_handle_id IS NOT NULL
           AND ch.intent IS DISTINCT FROM '${want_intent}'
    " | tr -d '[:space:]'
}

R_TID="$( register_and_deploy "${TEMPLATE_R}" "read-only" )"
RW_TID="$( register_and_deploy "${TEMPLATE_RW}" "read-write" )"

R_IID="$( create_instance "${R_TID}" "fii-r-$( date +%s )-$$" )"
RW_IID="$( create_instance "${RW_TID}" "fii-rw-$( date +%s )-$$" )"
echo "fanout-intent-inheritance: read-only instance ${R_IID}; read-write instance ${RW_IID}"

post_seed "${R_IID}" "fanout_seed_r" '[{"key":"r-1","payload":{}},{"key":"r-2","payload":{}},{"key":"r-3","payload":{}}]'
post_seed "${RW_IID}" "fanout_seed_rw" '[{"key":"rw-1","payload":{}},{"key":"rw-2","payload":{}},{"key":"rw-3","payload":{}}]'

END=$(( $(date +%s) + POLL_BUDGET_SECONDS ))
R_INHERITED=0
RW_INHERITED=0
while [ "$( date +%s )" -lt "${END}" ]; do
    R_INHERITED="$( count_sub_claims_with_intent_for_instance "${R_IID}" "r" )"
    RW_INHERITED="$( count_sub_claims_with_intent_for_instance "${RW_IID}" "rw" )"
    if [ -z "${R_INHERITED}" ]; then R_INHERITED=0; fi
    if [ -z "${RW_INHERITED}" ]; then RW_INHERITED=0; fi
    if [ "${R_INHERITED}" -ge 3 ] && [ "${RW_INHERITED}" -ge 3 ]; then
        break
    fi
    sleep 3
done

R_MISMATCHED="$( count_sub_claims_with_intent_mismatch_for_instance "${R_IID}" "r" )"
RW_MISMATCHED="$( count_sub_claims_with_intent_mismatch_for_instance "${RW_IID}" "rw" )"
if [ -z "${R_MISMATCHED}" ]; then R_MISMATCHED=0; fi
if [ -z "${RW_MISMATCHED}" ]; then RW_MISMATCHED=0; fi

echo "fanout-intent-inheritance: read-only instance: ${R_INHERITED} sub-claims with intent=r (${R_MISMATCHED} mismatched)"
echo "fanout-intent-inheritance: read-write instance: ${RW_INHERITED} sub-claims with intent=rw (${RW_MISMATCHED} mismatched)"

if [ "${R_INHERITED}" -lt 3 ]; then
    echo "fanout-intent-inheritance: FAIL — read-only fan-out persisted only ${R_INHERITED} sub-claims with intent=r (expected >=3 per the 3-element seed list)" >&2
    exit 1
fi
if [ "${RW_INHERITED}" -lt 3 ]; then
    echo "fanout-intent-inheritance: FAIL — read-write fan-out persisted only ${RW_INHERITED} sub-claims with intent=rw (expected >=3 per the 3-element seed list)" >&2
    exit 1
fi
if [ "${R_MISMATCHED}" -ne 0 ]; then
    echo "fanout-intent-inheritance: FAIL — read-only fan-out produced ${R_MISMATCHED} sub-claim(s) with intent != r (intent inheritance broken at the runtime AcquireSubClaims propagation layer)" >&2
    exit 1
fi
if [ "${RW_MISMATCHED}" -ne 0 ]; then
    echo "fanout-intent-inheritance: FAIL — read-write fan-out produced ${RW_MISMATCHED} sub-claim(s) with intent != rw (intent inheritance broken at the runtime AcquireSubClaims propagation layer)" >&2
    exit 1
fi

echo "fanout-intent-inheritance: PASS — sub-claim intent inherits the parent's declared intent verbatim (intent=r -> ${R_INHERITED} sub-claims all intent=r; intent=rw -> ${RW_INHERITED} sub-claims all intent=rw). Intent is architecturally a cascade-layer contract (ModeCoexists in lib/foundation/locks/conflict.go), and this proof exhibits the persisted-state precondition the cascade relies on."
