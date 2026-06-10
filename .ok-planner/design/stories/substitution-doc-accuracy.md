---
story: substitution-doc-accuracy
status: as-is
---

# Substitution module header matches resolver

## Role

As a template author reading the substitution module documentation, I can trust that the listed source kinds match exactly what the resolver actually recognizes, so that I don't silently miss a supported source.

## Capability

Automated accuracy gate: parses the substitution module's header enumeration of source kinds and asserts it matches the set of source kinds the resolver actually dispatches on (the live `case` arms).

## Business value

Template authors don't silently miss supported substitution sources because the doc undercounts; the gate catches drift at build time, not at template-author confusion time.

## Acceptance

An automated accuracy check parses the substitution module's header enumeration of source kinds and asserts it matches the set of source kinds the resolver actually dispatches on (the live `case` arms). The check fails when the header undercounts, omits a real kind (trigger, child), or lists a kind the resolver doesn't handle.

## Falsifier

The check is informational only (doesn't fail CI), OR text-matches the doc without ASTs over the resolver code.

## Proof

Executable proof.

## Notes

2026-06-08 — Story landed via spec 2026-06-08-design-corpus-bootstrap.
