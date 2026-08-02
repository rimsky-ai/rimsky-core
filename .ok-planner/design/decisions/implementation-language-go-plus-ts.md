---
decision: implementation-language-go-plus-ts
---

# Implementation languages

## Choice

A single primary systems language (Go) for all core code, including every bundled service and executor reference; TypeScript exists only as an ambient type-declaration stub for the protocols module's wire contract, not as an implementation language for any shipped binary.

## Rationale

A single core ecosystem keeps the build, lint, and module-graph discipline uniform across the codebase. Confining TypeScript to a type-only stub on the permissive wire-contract surface gives external implementers optional type hints for the protocol without paying for a second toolchain anywhere a real service runs.

## Alternatives

- Full TypeScript implementations alongside Go — rejected: a second toolchain, build, and lint discipline maintained for the same behavior.
- Go only, with no TypeScript stub at all — rejected: external implementers on the wire contract lose type hints that cost no toolchain to ship.
