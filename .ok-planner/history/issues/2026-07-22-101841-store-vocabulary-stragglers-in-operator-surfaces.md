---
issue: store-vocabulary-stragglers-in-operator-surfaces
kind: human
category: vocabulary
artifacts:
  - concept:claim-producer
  - decision:claim-producer-vocabulary-boundary
  - story:claim-producer-filesystem
  - story:claim-producer-postgres
  - story:claude-agent
status: answered
opened: 2026-07-22T10:18:41Z
github: https://github.com/rimsky-ai/rimsky-core/issues/34
---

# The "store" → "claim-producer" rename left operator-facing names behind

## Question

Does the store→claim-producer vocabulary boundary
(`decision:claim-producer-vocabulary-boundary`) cover the operator-facing
binary names, env vars, and node-attribute key the sweep had missed
(`store-filesystem`/`store-postgres`, `STORE_FILESYSTEM_CONFIG`/
`STORE_POSTGRES_CONFIG`, `cwd_from_store`), and if so, has the rename since
landed?

## Answer

`decision:claim-producer-vocabulary-boundary` already committed to exactly
this: "The store→claim-producer vocabulary sweep covers every shipped,
user-facing surface: binaries, entrypoints, config grammar, example
templates, and every name a template author or operator observes,"
exempting only internal test machinery. Binaries, env vars, and node
attributes are squarely inside that commitment.

The rename has since landed in the codebase (commit `2ef58038`, "execute
sprint 2026-07-24-github-issue-drain", dated 2026-07-24 — two days after
this issue was filed). Verified against current `HEAD`:

- Binaries/service directories: `lib/services/claim_producers/{filesystem,postgres}`
  — no `store-filesystem`/`store-postgres` naming remains anywhere in
  source, dockerfiles, CI config, or the services test harness (only two
  historical `releases/*.md` entries retain the old name, which is correct
  — release notes are a dated record, not a live surface).
- Env vars: `RIMSKY_CLAIM_PRODUCER_FILESYSTEM_CONFIG` /
  `RIMSKY_CLAIM_PRODUCER_POSTGRES_CONFIG` throughout
  `lib/services/test/harness/*.go`; no `STORE_FILESYSTEM_CONFIG` /
  `STORE_POSTGRES_CONFIG` remains.
- Node attribute: `cwd_from_claim_producer` throughout
  `lib/services/executors/claude-agent/{server.go,agentrun.go,expected_attributes_schema.json}`,
  including its error text; no `cwd_from_store` remains.

The corpus already answered the question and the code now conforms —
nothing left to fix.
