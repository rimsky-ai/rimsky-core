---
concept: dry-run
status: as-is
aliases: []
---

# Dry-run

## What it is

A request mode — preview-without-commit — that asks "what would happen if I did this?" without applying it. It resolves from EITHER a per-request dry-run flag OR an identity-bound grant mode (see `concept:permission`). Default (no flag, an execute-mode grant) is execute. When the request resolves to dry-run, a write handler runs validation (including side-effect-free external calls like the validation protocol's checks; see `concept:validation`) but skips the actual mutation, returning a synthetic preview envelope describing what the write would have produced.

The grant mode is a **floor** — a key whose matched grant entry is dry-run runs every covered write in dry-run regardless of the flag, and the caller cannot escalate past it (absence of the flag, or a flag explicitly set to false, does NOT lift an identity-bound dry-run floor; the flag can only ADD dry-run on top of an execute grant). When more than one grant entry matches the same action, the most permissive matched mode governs — a coexisting execute-mode entry lifts the floor even if a dry-run entry also matches. The auth middleware threads the resolved mode through the request context; handlers read it back and gate the side-effectful path through a shared preview-response helper that emits the synthetic envelope.

## Purpose

Dry-run is human-in-the-loop preview-before-commit and validate-without-commit: an operator or agent can preview the effect of any write before applying it, and can validate that a request would be accepted (well-formed, authorized, structurally valid) without committing its side effect. The same audit-log trail records the attempt — "this request was previewed; we did not apply it" — as forensic evidence.

## Boundaries

Owns: the per-request dry-run flag handling in the auth middleware, the per-request context plumbing, the preview-response helper, and the per-handler dry-run branches. Dry-run covers **all** write actions uniformly — there is no carve-out for the auth surface or anywhere else. Does NOT own: the read path (a read has no mutation to skip, so the flag is a no-op there; see Invariants). Adjacent: `concept:permission` (the resolved mode is the max-restriction of the request flag and the matched grant entry's mode — `concept:permission` owns the grant mode, `concept:dry-run` owns its resolution and the dry-run branches), `concept:event-log` (the audit row records the resolved mode).

## Invariants

- **Reads honor the flag as a no-op.** A read has no mutation to skip, so the dry-run flag on a read action runs the read normally and returns it. This lets a mixed read/write script set the flag uniformly without special-casing reads. The audit row records the dry-run mode but marks the read as having executed.
- **Every write is previewable.** Each write action has a dry-run branch returning a synthetic preview envelope and performing no mutation. This is guaranteed structurally by a coverage conformance test that enumerates every write action and asserts each, invoked under the flag, mutates nothing — not by a runtime gate. Any write handler missing its branch fails the test.
- **Validation runs faithfully.** Dry-run is "validate-without-mutate." For template registration this includes firing the validation protocol's checks against advertising services (see `concept:validation`) — those are side-effect-free reads from the platform's perspective.
- **A request resolved to dry-run never mutates.** With no carve-outs, this holds by construction; the coverage conformance test is its enforcement.
- **Audit row reflects intent.** The auth middleware emits an access-attempt event tagged with the resolved mode. For a write under dry-run the row marks execution as not having happened; for a read, execution did happen. The row is the canonical evidence of "the request was previewed; we didn't apply it."
