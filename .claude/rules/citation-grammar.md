# Citation Hints for Agent Prose

When an agent is writing prose to a human in an interactive session — status updates, "I did X", "the bug is in Y", review findings, summaries surfaced back to the user, items written into a notes file for the user to read — wrap artifact references in a `<kind>:<value>` prefix so the user doesn't have to infer what kind of thing each noun is.

## Scope

**Where this applies:**
- Agent output addressed to a user in an interactive session.
- Items written into implementation notes or review notes that the user will read.
- Multi-turn agent ↔ user dialogue (clarifications, recommendations, "want me to X?").

**Where this does NOT apply:**
- Source code, docstrings, code comments.
- Concept docs, specs, the issue queue, and archived records under `.ok-planner/`.
- `CLAUDE.md`, `README.md`, anything else in the repo.
- Commit messages, PR descriptions.
- Test output, build output, stack traces, tool diagnostics — those keep their existing shape.

The grammar is a cognitive-load reducer for live conversation, not a documentation convention. Combined with cleaner naming inside the codebase itself, it should make agent-to-human discussion of the platform substantially easier to follow.

## Why

Bare nouns like `Open`, `claimed_by`, or `rimsky_node_events` could be Go methods, proto verbs, table columns, config keys, or concept slugs — the reader has to infer the kind from surrounding prose. The prefix replaces that inference with one extra word.

## Format

`` `<kind>:<value>` `` inside Markdown backticks.

- First `:` is the kind delimiter; the value can contain `:` `/` `.` `#` freely (no escaping needed).
- Backticks make the citation parse as a single token across Markdown, code blocks, and most chat surfaces.

## Kinds

| Kind | Example |
| --- | --- |
| `code:` | `` `code:foundation/locks/interface.go::ClaimProducer` `` |
| `code:` with line | `` `code:foundation/integration/runner_acquire.go::handleOrphanedClaim#793` `` |
| `file:` | `` `file:deploy/docker-compose.yml` `` |
| `file:` with line range | `` `file:README.md#1-50` `` |
| `pkg:` | `` `pkg:github.com/rimsky-ai/rimsky-core/foundation` `` |
| `table:` | `` `table:rimsky_claim_handle` `` |
| `col:` | `` `col:rimsky_claim_handle.scope_data` `` |
| `proto:` message | `` `proto:claim_producer.proto::OpenRequest` `` |
| `proto:` field | `` `proto:executor.proto::ExecuteRequest.userdata` `` |
| `proto:` RPC | `` `proto:claim_producer.proto::ClaimProducer.Open` `` |
| `route:` | `` `route:POST /instances` `` |
| `cfg:` | `` `cfg:persistence.blob.backend` `` |
| `env:` | `` `env:RIMSKY_CONFIG` `` |
| `concept:` | `` `concept:claim-handle` `` |
| `issue:` (issue-queue id) | `` `issue:endpoint-env-var-collision` `` |
| `invariant:` | `` `invariant:4` `` or `` `invariant:4-claimant-guarded-release` `` |
| `spec:` / `sketch:` | `` `spec:2026-05-11-design-log-convergence` `` |
| `cmd:` | `` `cmd:make build-all` `` |

Conventions:
- Use `::` (not `.`) to separate path from symbol, in both Go and TS. `.` is for runtime member access; `::` is name disambiguation.
- `cfg:` is the unified `rimsky.yml`. For ad-hoc YAML (Helm values, `deploy/docker-compose.yml`), use `file:` and quote the key inline.
- `proto:` uses basename (`claim_producer.proto`, not `protocols/proto/v1/claim_producer.proto`). Pre-v1 there is no path ambiguity.

## Practical notes

- A long citation is cheap. `` `code:foundation/integration/runner_acquire.go::handleOrphanedClaim#793` `` reads faster than chasing down what `handleOrphanedClaim` is.
- When the same noun appears multiple times in close proximity, prefix the first occurrence and use bare backticks after ("`` `code:foo.go::Bar` `` is responsible for `X`; `Bar` returns…").
- For nouns that span kinds, list them: "`` `concept:claim-handle` `` (the design-log concept) ↔ `` `table:rimsky_claim_handle` `` (the persisted row)."
- When the user mentions a bare noun, infer the kind from context, but cite back with the grammar in your reply — the user benefits even if they didn't use it themselves.

## Relation to in-code annotations

The `@concept:`, `@story:`, `@decision:` citation tags in source code are a different surface and keep their existing shape. They mark *where in the code* a design artifact is realized. This grammar is for prose citations *referring to* those things from outside the code. Do not change in-code citations.
