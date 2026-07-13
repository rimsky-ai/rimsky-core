# Intent Dossier: dry-run

Distilled 2026-07-13 from session transcripts (2026-06-12..2026-07-13) and ok-planner
history artifacts (2026-05-04..2026-06-11). Transcript tier outranks artifact tier;
later intent supersedes earlier. Part of the drift-remediation intent ledger.

## Net position

- Dry-run has **two layers** (final position, 2026-06-06/08): a per-request `?dry_run=true` flag on any write returns a synthetic envelope with the same validation as a live write and no persistence; and an api-key grant entry may carry `mode: dry_run`, pinning that key to attempt-only. **Effective mode is the floor of grant mode and request flag — the caller cannot escalate past the grant.**
- Every write action on the control-api has a dry-run branch, **no carve-outs** — including auth:create/revoke/rotate. A request resolved to dry_run must never produce a live mutation. The guarantee is structural: a coverage conformance test enumerates every write action in the registry, invokes each with `?dry_run=true`, and asserts no mutation plus a `would_have_*` envelope (2026-05-29).
- Reads honor the flag as a no-op preview: the read runs normally; the audit row records mode dry_run with executed:true — so mixed scripts can set the flag uniformly.
- Dry-run's stated purpose is human-in-the-loop preview-before-commit and validate-without-commit; the graduated-trust / agent-promotion narrative was removed (2026-05-29) and never explicitly reinstated even after grant-mode returned.
- `template:validate` is a deliberately **orthogonal** read action, not a dry-run extension: narrower grant than register, 200-with-findings lint semantics, ignores the dry-run modifier.

## Required behaviors (open promises)

- Synthetic dry-run responses carry `dry_run: true` and clearly marked placeholder IDs (`would_have_created` / `would_have_invalidated` / `would_have_terminated` …) (2026-05-15, control-plane-mcp-and-auth, artifact; the envelope contract survived every subsequent redesign).
- Dry-run of template:register runs the Validation mix-in RPCs faithfully (side-effect-free) and skips only the DB insert — a real precursor to live invocation (2026-05-15, artifact).
- Every-write-action coverage with the registry-enumerating conformance test failing CI when a future write handler omits its branch (2026-05-29, console-upstream-auth-audit, artifact): "With no carve-outs, this holds by construction."
- Grant-mode floor: a `mode: dry_run` grant previews every write and commits none; effective mode = floor(grant, flag); attempts audited executed:false — "proving the floor is carried by key identity, not by the request flag" (2026-06-06 gap-closure reversal + 2026-06-08 corpus-bootstrap, artifact).
- auth:create dry-run mints no plaintext credential, returns a placeholder id, and in anonymous mode notes that committing the first key exits anonymous mode (2026-05-29, artifact).
- lineage:prune dry-run returns a real exact would-prune count via a COUNT sharing one WHERE-clause constant with the DELETE so the two physically cannot drift (2026-05-29, artifact).
- backfill:create rejects (400) a target_node that is not a fan-out node wired for the partition override — refused at submit, not warned; the dry-run branch applies the same validation (2026-05-29, artifact; tightened the earlier warn-only invariant).
- instance:kill (named to avoid colliding with instance:terminate in the action registry) carries a dry-run branch returning a would_have_terminated envelope listing the node-runs that would be force-failed (2026-05-28, quality-of-life-features, artifact).
- MCP-vs-HTTP parity: the same logical operation gets the same permission check and dry-run behavior; audit rows differ only in protocol_skin (2026-05-15, artifact-only).

## Intentional absences

- **Per-handler validate/execute two-function factoring** — the plan's implementation hint was judged unnecessary; dry-run is a shared early-exit helper (WriteDryRunResponse) inside otherwise-unchanged handlers. The synthetic-response contract, not the factoring, is the requirement (2026-05-15, divergences).
- **The graduated-trust / agent-promotion narrative** — removed from the concept 2026-05-29; grant-mode's 2026-06-06 return was justified as an identity-bound floor, not a promotion ladder.
- **Dry-run modifier semantics on read actions** — reads never have would-be behavior; the flag is a defined no-op preview (2026-05-29).

## Corrections and restorations (drift-fight record)

- **Auth carve-out eliminated** (2026-05-29): the 2026-05-15 position that auth mutations are not dry-runnable in V1 was replaced by the no-carve-outs constraint; auth:create/revoke/rotate now have real dry-run branches. A finding citing the V1 auth carve-out asserts superseded expectations.
- **Dropped test protections recorded** (2026-05-15, divergences, artifact-only): no TestAuthDryRunIgnored, no per-subcommand CLI auth tests, no anonymous-predicate cache-invalidation test landed with the original pass — later mooted for the auth-ignore case by the no-carve-outs redesign and its structural conformance test.

## Superseded / historical

- Per-grant-entry `mode: dry_run` v1 design with first-match-wins grant ordering (2026-05-15) → grant mode dropped entirely; flag-only, set-membership permission evaluation (2026-05-29) → grant mode restored as an identity-bound floor composing with the flag (2026-06-06, explicit reversal of the 2026-05-29 decision). The 2026-06-06/08 two-layer floor model is the standing position.
- Auth mutations not dry-runnable (2026-05-15) → every write action, no carve-outs (2026-05-29).
