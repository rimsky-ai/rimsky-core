# rimsky-core bug list — surfaced by the v0.4.1 docs reconciliation

Surfaced by a `rimsky-docs` build-docs / refine-docs run reconciling the published
docs against **rimsky-core v0.4.1** (tag `v0.4.1`, local `65ec29b`). Each item is a
fix in **rimsky-core**, not in rimsky-docs. Verified against source.

> Lifecycle note: the prior `rimsky-core-bugs.md` (the v0.4.0 batch — stub
> advertises no schema, `holds:` aliases invisible to the validator, the
> nil-tx / SQLite read-path wedge) was fully fixed in v0.4.1 and archived to
> `history/sketches/`. This is a fresh, **low-severity** batch: source-comment
> drift plus one invalid shipped example. **None break runtime behavior** — but
> BUG-1 propagates into the *generated* docs, and all four mislead a reader.

---

## A. Source-comment drift (misleading comments)

### BUG-1 — Spec doc-comments call the claim-acquisition block `claims:`, but the YAML field is `stores:`
- **Severity:** Low behaviorally; **propagates into generated docs**.
- **Location:** `lib/foundation/spec/graphs.go:39,49,51` and `lib/foundation/spec/template.go:194`. The actual field is `Stores []NodeStoreRef \`yaml:"stores"\`` (`template.go:130`). There is no `claims:` YAML key.
- **Symptom:** The godoc/struct comments describe claim acquisition as the node's `claims:` block. The `rimsky-docs-template-ref` generator lifts these comments verbatim into `reference/template-schema.md` (e.g. "the upstream declares the referenced claim alias in its `claims:` block"; "a claim alias declared on the node (in `claims:` or `holds:`)"), and the design concept echoes the same word. An agent reading the schema reference looks for a `claims:` key that does not exist — the real acquisition key is `stores:`. Surfaced by a cold-read of `cookbook/claim-handoff.md`, where the recipe's correct `stores:` YAML conflicted with the schema reference's `claims:` prose.
- **Suggested fix:** replace `claims:` with `stores:` (the real key) in those doc-comments, so the generated schema reference names the key operators actually type.

### BUG-2 — Stale `viewer` role in control-API comments (no such bundled role)
- **Severity:** Low (comments only).
- **Location:** `lib/control/controlapi/actions.go:453`, `lib/control/controlapi/mcp_route.go:83`.
- **Symptom:** Both comments attribute the `*:read` wildcard coverage to "the bundled `viewer` role." No `viewer` role exists — `cmd/rimsky/cli/roles/` holds `admin`, `agent-supervisor`, `debug-operator`, `operator`, `publisher-service`, `read-only`. The pure `*:read` bundle is `read-only` (`debug-operator` and `agent-supervisor` also carry `*:read`).
- **Suggested fix:** replace `viewer` with `read-only` in both comments.

### BUG-3 — Stale "deletes the lock-holder row" comment in `auto_terminal.go` (contradicts Promote-not-delete)
- **Severity:** Low (comment only) — but it misstates a `@blessed-invariant`.
- **Location:** `lib/runtime/auto_terminal.go:11` (the `@blessed-invariant 13` header comment).
- **Symptom:** The comment says auto-terminal fires Commit/Abandon "then deletes the lock-holder row." Current behavior **promotes, never deletes**: `promoteHandleState` (`lib/runtime/terminal_decision.go`) flips `rimsky_claim_handles.state` to `committed`/`failed` and preserves the row past terminal (a later retention sweep reaps it); `markClaimHolderForRun` → `CompleteByClaimHandleAndRun` transitions the holder row to `completed`/`failed` with `completed_at` set — there is no delete (`ClaimHolderTable` has no delete method). The comment predates the Promote refactor. Surfaced while correcting `cookbook/claim-handoff.md`, which had inherited the same "deleted / holders → 0" misconception from this comment.
- **Suggested fix:** rewrite the comment to describe promotion (state flip + row preserved past terminal), matching `promoteHandleState`.

## B. Invalid shipped example

### BUG-4 — postgres store `config-example.yml` ships an invalid `write_semantics: direct`
- **Severity:** Low–Medium (a copyable example that won't load).
- **Location:** `lib/services/stores/postgres/config-example.yml:10`.
- **Symptom:** `write_semantics: direct`. `ParseWriteSemantics` (`lib/protocols/claimproducer/types.go`) accepts only `sync` | `staged_async` | `blocking_async` | `read_only`; `direct` is rejected. An operator copying the example to start the postgres store hits a config error. (The published docs' `store-postgres.yml` correctly uses `sync`, so this is a rimsky-core-side example bug only.)
- **Suggested fix:** change the value to a valid one (`sync` or `staged_async`).

---

## Not a bug — known deferred (tracked here so the doc stays in sync)

- `lib/graph/node/template_validator_graphs.go:227-235` notes that an absorbed
  sub-graph **entry** node still gets its own `rimsky_nodes` row at provisioning
  (it just never dispatches standalone); removing that row is a documented
  follow-up. The new `cookbook/sub-graph.md` recipe describes the *current*
  behavior accurately (the entry has a row, never dispatches). Flagged only so
  that if/when the follow-up lands and the entry row goes away, the recipe's
  node-listing example is updated to match.
