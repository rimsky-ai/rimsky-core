# rimsky-core bug list

Surfaced by a `rimsky-docs` build-docs / refine-docs run reconciling the published
docs + bundles against **rimsky-core v0.4.0** (tag `v0.4.0`; local `dev@5801b54`).
Each item is a fix in **rimsky-core**, not in rimsky-docs. Verified against source
(several also against a live `quickstart/` + `deploy/` stack).

> Not in this list, by decision: the `mcp_catalog`/`CLAUDE_AGENT_CONFIG` config
> surface and the `rimsky compose` CLI workflow are *documented-but-unimplemented*.
> The call was "documentation should reflect implemented reality," so those are
> rimsky-**docs** fixes (scrub the descriptions to match the code), not rimsky-core
> work — tracked on the docs side.

---

## A. Behavior / code bugs

### BUG-1 — Quickstart stub executor advertises no attribute schema → attribute nodes fail dispatch
- **Severity:** High (breaks the published quickstart's own flagship demo).
- **Location:** `lib/services/test/stubexecutor/main.go` (Execute-only gRPC server; registers no `ExecutorObservability` / `Capabilities` RPC). Dispatch gate: `lib/runtime/runner_dispatch.go::resolveAttributes` (~line 440).
- **Symptom:** Any node that declares an `attributes:` block and names `executor: stub` fails dispatch with `executor_schema_unavailable` and settles `failed` (default policy → `give_up`). The standalone stub has no expected-attributes schema to validate against, so the attribute-surface gate rejects the node before substitution runs.
- **Live evidence:** dispatch log `expected_attributes_schema_resolver: skip executor=stub reason=executor_capabilities_nil`.
- **Blast radius:** `quickstart/example-template.yml` (two `stub` nodes, each with an `attributes:` block) does not reach its documented "both nodes `fresh`" end state; it settles `failed`. Same for any quickstart cookbook recipe that uses attributes on the stub.
- **Suggested fix:** give the standalone `stubexecutor` an `ExecutorObservability`/`Capabilities` RPC that advertises a permissive expected-attributes schema (e.g. `{type: object, additionalProperties: true}`), so attribute-bearing nodes dispatch and settle. (Alternatively/additionally a product call on the docs side — see the quickstart-framing discussion — but the stub being unable to carry an `attributes:` node is the core defect.)

### BUG-2 — `holds:` claim aliases are invisible to the registration validator (modern co-holdership unregistrable)
- **Severity:** High (the directive the concept docs call modern cannot be used with `{{claim.<alias>}}` reads).
- **Location:** `lib/graph/node/template_validator.go::validateAttributesSchema` (~lines 889–899 build the recognized claim-alias set; checked at ~line 1373 in `checkAttributeDirectiveBody`). The set is derived from `stores:` (direct aliases) and `inherits:` (inherited aliases) only — never from `n.Holds`.
- **Symptom:** A node that co-holds a claim via `holds: { <alias>: { from: <upstream> } }` and reads `{{claim.<alias>.address}}` is **rejected at `rimsky template register`** with: `claim directive references alias "<alias>" which is neither acquired here nor declared in inherits:`. The otherwise-identical `inherits: [{ claim: <alias> }]` form passes.
- **Contradiction:** `concept:claim-co-holdership` (and `concept:claim`) document `holds:` as the modern claim-pass directive and call `inherits:` legacy/superseded — so the documented-modern form is the one that fails, and the "legacy" form is the only one that works.
- **Verified:** ran `ValidateTemplate` on both variants against v0.4.0 — `holds:` rejects, `inherits:` validates.
- **Suggested fix:** teach `validateAttributesSchema` to include `holds:` (`n.Holds`) aliases in the recognized claim-alias set, so the modern co-holdership directive supports `{{claim.<alias>...}}` reads. (If `holds:` is *not* meant to support claim reads, the concept docs' legacy/modern framing should be reversed instead — but that seems contrary to intent.)

### BUG-3 — Quickstart `SQLITE_BUSY` storm wedges the control-API read path
- **Severity:** High-ish (makes the quickstart feel broken even when writes succeed).
- **Location:** the `rimsky/all` image runs scheduler + supervisor + control-api as three processes against one single-connection SQLite pool (`quickstart/` default persistence).
- **Symptom:** under the three-process contention, `GET /instances/{id}/nodes` returned 500/timeout in a sustained `SQLITE_BUSY` storm during a live run; `template register` / `deploy` / `instance create` (writes) succeeded, but the read path did not reliably respond.
- **Note:** `quickstart/rimsky.yml`'s own comment credits an "option-C nil-tx refactor" with having removed SQLite self-deadlock on the single-conn pool — so this looks like a regression or an incomplete fix.
- **Suggested fix:** enable SQLite WAL + a `busy_timeout`, and/or serialize-with-retry on `SQLITE_BUSY`, and/or give the read path its own connection. Reproduce by bringing up `quickstart/` and hammering `GET /instances/{id}/nodes` while a frame is in flight.

---

## B. rimsky's own source docs / comments out of sync with its code

### BUG-4 — `named-lock` concept documents a phantom `mode:` field
- **Severity:** Low (doc-vs-code).
- **Location:** `.ok-planner/design/concepts/named-lock.md:11` says named locks are declared with `mode: mutex | counting` and a capacity. The code (`lib/foundation/locks/registry.go::NamedLockConfig`) has a single field `Limit int` (limit=1 ⇒ mutex, N>1 ⇒ counting). There is no `mode:`.
- **Suggested fix:** scrub `mode: mutex | counting` from the concept doc; describe the `limit`-only schema. (This flows into the published `docs/concepts/named-lock.md`, which is a verbatim copy and will pick the fix up automatically.)

### BUG-5 — Stale attribute-writeback path in the executor proto comment
- **Severity:** Low (doc-vs-code; shows through the generated wire reference).
- **Location:** the `ExecuteRequest.attributes` comment in `lib/protocols/proto/v1/executor.proto` says the incremental writeback path is `/v1/attributes/{node_id}` (pre-2026-05-20). The implementation uses `${base}/v1/runs/{runId}/attributes` (`lib/services/executors/claude-agent/src/attributes-tools.ts::buildAttributesWritebackUrl`).
- **Suggested fix:** update the proto comment to the current `/v1/runs/{runId}/attributes` path. (Regenerating the docs' wire reference then corrects it downstream.)

### BUG-6 — `force-fire` is documented + still in the CLI, but the route is not mounted
- **Severity:** Medium (a documented/CLI-exposed admin action that 404s).
- **Location:** `.ok-planner/design/concepts/control-api.md:13` lists "scheduled-node force-fire endpoints"; `cmd/rimsky/cli/admin.go` still carries an `admin force-fire` command. But no `force-fire` / `scheduled-nodes` route is mounted anywhere in `lib/control` — the mounted admin routes are invalidate (`POST /admin/instances/{i}/nodes/{n}/invalidate`), `POST /admin/lineage/prune`, and the `GET /admin/diagnostics/*` reads. The template-schedule fire path was retired in the 2026-05-15 schedule cascade.
- **Suggested fix:** remove the `force-fire` endpoint from the control-api concept doc and drop (or repoint) the vestigial CLI command. (The published operator-guide / comparison / patterns prose was already corrected on the docs side.)

### BUG-7 — Stub executor README cites a nonexistent `ParkReason` enum value
- **Severity:** Low (doc-vs-proto).
- **Location:** `test/support/executors/stub/README.md:33` references `ParkReason_PARK_REASON_RETRY_BACKOFF`. The proto enum (`lib/protocols/proto/v1/executor.proto`, ParkReason) is the closed set `{PARK_REASON_AWAIT_CALLBACK=0, PARK_REASON_SNOOZE=2}`.
- **Suggested fix:** correct the README to an existing ParkReason value (or drop the reference).

### BUG-8 — protocols `conformance` godoc references the retired standalone conformance binaries
- **Severity:** Low (doc-vs-code; shows through the generated Go-package reference).
- **Location:** the godoc comments in `lib/protocols/conformance/...` reference `cmd/rimsky-claim-producer-conformance` / `rimsky-executor-conformance` etc. Those standalone binaries were folded into the `rimsky conformance <protocol>` CLI subcommand (`cmd/rimsky/conformance.go`). The stale names surface in the generated `docs/protocols/go-packages.md` (≈ lines 480, 508, 562).
- **Suggested fix:** update the conformance package godoc to reference `rimsky conformance <protocol>` instead of the removed `cmd/rimsky-*-conformance` binaries. (Regenerating go-packages.md then corrects it downstream.)

---

## Notes on verification
- BUG-1, BUG-2, BUG-3 were observed against a live stack (quickstart + a validator probe); the rest are source reads.
- This file lives in rimsky-docs run-scratch (`.build-docs/`, gitignored) because this skill treats the rimsky-core checkout as read-only and never writes into it. Move it into rimsky-core (e.g. an issue tracker or a `BUGS.md`) when you start that session.
