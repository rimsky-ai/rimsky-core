---
issue: compose-demo-scripts-unrunnable-when-vendored
kind: audit
category: doc-drift
artifacts: []
status: promoted
opened: 2026-08-06T06:49:20Z
sprint: 2026-08-06-ruled-intake-drain.md
---

# The compose demo scripts reach outside the examples module, so a vendored copy cannot run them

The examples module ships as copy-and-modify reference material — the
module-layout concept frames it as self-contained, depending only on
the protocols module. Its three compose demo scripts
(`examples/compose/*-demo.sh`) break that promise: each one builds a
stub executor from `cmd/rimsky/cli/compose/testdata/` and copies a
sample manifest from the same place — paths two directories above the
examples module that exist only in a full checkout. A consumer who
vendors `examples/` (as the docs repo does) gets scripts that fail on
their first cross-module reference. The scripts already honor
`RIMSKY_BIN` to skip building the CLI itself, so the binary is not the
blocker; the two fixture references are, and they have no override.

The fixtures' current home is a placement accident, not a shared
dependency: nothing under `cmd/rimsky/cli/compose/` — no test, no
other consumer — references them; only the demo scripts do. The
module-layout invariant is enforced as a Go-import lint rule, which is
why shell-script cross-references never tripped it.

## Options

- Move the stub executor and sample manifest into `examples/compose/`
  and keep the `RIMSKY_BIN` override — the scripts become genuinely
  self-contained. Cost: fixture paths move.
- Document the full-checkout requirement in each script's header.
  Cost: vendored copies stay broken, by admission.

The ruling decides whether the demo scripts become self-contained or
document their dependence.

## Ruling

> Recommended ruling (/verify-issues): relocate the two fixtures into
> the examples module and keep the existing binary override — the
> scripts then run from a vendored copy, which is the module's whole
> stated purpose.
>
> Rationale: the documentation option concedes the copy-and-modify
> promise rather than keeping it, and the fixtures have no other
> consumer holding them where they are. The flip case: a second
> consumer of those fixtures appearing inside the CLI tree would
> argue for duplication instead of relocation.

<!-- Owner: this is a recommendation, not your decision. Leave it
as-is to accept — the next /plan-sprint carries it, naming the
generated/recommended batches at sign-off. Edit the text to
redirect, empty the section to discuss live, or delete this note
to adopt the ruling as your own. -->
