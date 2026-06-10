---
story: lenient-marker
status: as-is
---

# Template author marks substitution lenient

## Role

As a template author, I can mark a substitution directive lenient with `?` so a missing source resolves to empty at runtime instead of failing dispatch, so that I can write templates that gracefully handle optional upstream inputs.

## Capability

`?` lenient marker on substitution directives: missing source resolves to empty at runtime; absence of the marker keeps the strict (fail-on-missing) behavior.

## Business value

Template authors gracefully handle optional upstream inputs without writing handler branches; the strict / lenient distinction is explicit at the directive site.

## Acceptance

A template node setting an attribute via a `?`-marked directive whose source is absent at dispatch dispatches successfully (the executor receives the resolved-empty attribute) and the node-run reaches terminal. A companion template using the same directive without `?` against the same absent source fails dispatch with a clear missing-source error.

## Falsifier

The `?` marker is silently treated like no-marker (lenient dispatch fails when source absent), OR no-marker is silently treated like `?`.

## Proof

Executable proof.

## Notes

2026-06-08 — Story landed via spec 2026-06-08-design-corpus-bootstrap.
