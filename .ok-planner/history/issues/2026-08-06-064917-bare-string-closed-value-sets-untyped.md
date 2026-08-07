---
issue: bare-string-closed-value-sets-untyped
kind: audit
category: config-surface
artifacts:
  - concept:cascade-mode
status: promoted
opened: 2026-08-06T06:49:17Z
sprint: 2026-08-06-ruled-intake-drain.md
---

# Two closed template vocabularies are typed as bare strings, so generated references cannot enumerate them

The template spec types some closed value sets as named Go types with
const blocks (`AggregationKind`, `BackoffKind`, `JitterKind`,
`ClaimLifetime`) and others as bare `string`. Two of the bare ones are
genuinely closed vocabularies: `cascade_mode`
(`lib/foundation/spec/template.go:98`), a fixed four-value set the
cascade-mode concept commits to, and claim `intent:`
(`template.go:112`), whose two values already have a proper named type
one layer down (`lib/protocols/claimproducer/types.go::Intent`) that
the spec layer simply doesn't reuse. Nothing behavioral rides on this —
a named string type marshals identically — but the reference generator
that documents the template schema emits value tables only for named
types with const blocks, so these two vocabularies come out of every
generated reference as undocumented free-text fields.

One correction to the issue as filed: the other two fields it named,
node `kind:` and the publisher `kind:`, are not closed sets — both
resolve against registries populated by deployment config, so a named
type would produce an "enum" with no enumerable values. They don't
belong in this fix. The corpus never commits to Go type shapes, so
what forces the remaining two is the codebase's own uniformity rule
(one idiom per job): closed vocabularies in this file already have an
idiom, and these two fields break it.

## Options

- Name the two genuinely closed types — `cascade_mode` and claim
  `intent:` — sweeping the roughly thirty call sites the compiler
  enumerates. Cost: a mechanical multi-package sweep.
- Do all four as filed. Cost: miscategorizes two open vocabularies as
  closed ones.
- Do nothing. Cost: the two vocabularies stay unenumerable in every
  generated reference.

The ruling decides which fields get named types.

## Ruling

Follow the codebase's convention: name the two genuinely closed
vocabularies — `cascade_mode` and claim `intent:` — as named string
types with const blocks, sweeping every call site, and leave the two
registry-resolved `kind:` fields as bare strings. For `intent:`,
reuse the protocol layer's existing type unless cross-module reuse
proves unwanted, in which case mirror it locally.

(Transcribed from the owner's live acceptance of the recommended
ruling, 2026-08-06.)
