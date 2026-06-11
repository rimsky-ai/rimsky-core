---
story: dry-run-request-flag
status: as-is
---

# Operator previews any write per-request

## Role

As an operator about to make a potentially destructive change, I can submit any write request with a per-request dry-run flag and get back a synthetic envelope showing what would have happened — same validation as a live write, no persistence — so that I preview the effect before committing.

## Capability

Per-request `dry_run` flag on writes: real validation runs, synthetic envelope returned, no persistence. Reads are flag-no-ops.

## Business value

Operators preview the effect of a write before committing — same validation as a real write — so destructive changes can be inspected before they land.

## Acceptance

An operator sends a write request (instance create, tag bind, template register, etc.) with the dry-run flag set; the response carries the dry-run marker and the synthetic envelope describing the would-have-been outcome; no row is persisted (verified via a subsequent list/get); a read request with the same flag genuinely executes (reads are flag-no-ops).

## Falsifier

A dry-run write persists state, OR returns a canned envelope unrelated to the inputs (validation didn't actually run), OR a read returns the dry-run marker.

## Proof

Executable proof.
