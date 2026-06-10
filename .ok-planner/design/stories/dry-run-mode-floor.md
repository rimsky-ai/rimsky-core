---
story: dry-run-mode-floor
status: as-is
---

# Operator mints attempt-only key

## Role

As an operator delegating control-plane access to an autonomous agent, I can mint an api-key whose grant pins write actions to dry-run mode — the key can preview every write but never commit one — so that I have attempt-only credentials that are safe to hand out.

## Capability

Identity-bound dry-run floor: a key whose grant carries `mode: dry_run` pins every write action to dry-run regardless of request flag.

## Business value

Operators can hand out attempt-only credentials safely — to autonomous agents or untrusted tooling — without trusting the caller to set a per-request flag.

## Acceptance

An operator mints an api-key whose grant carries `mode: dry_run` on a write action; using that key, an operator or agent issues a write request without the per-request dry-run flag and receives the synthetic envelope back; no row is persisted; the audit log records the attempt with executed-false. A second ordinary write-capable key issued by the same operator performs the same request and creates a real row — proving the floor is carried by key identity, not by the request flag.

## Falsifier

A dry-run-pinned key's write actually persists state, OR the audit misses the attempt, OR no comparison shows the floor is identity-bound.

## Proof

Executable proof.

## Notes

2026-06-08 — Story landed via spec 2026-06-08-design-corpus-bootstrap.
