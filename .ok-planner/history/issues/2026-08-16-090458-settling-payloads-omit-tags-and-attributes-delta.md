---
issue: settling-payloads-omit-tags-and-attributes-delta
kind: audit
category: conflicting
artifacts:
  - concept:signal
status: promoted
opened: 2026-08-16T09:04:58Z
sprint: 2026-08-17-accepted-intake-drain.md
---

# Two terminal-error signals ship without the tags and attributes delta the signal concept promises

A settled node-run emits a terminal signal, and the signal concept says every terminal payload — success and every error class — carries tags and an attributes delta, so a subscription predicate on them compiles at registration and means something at run time. Two settlement sites — fan-out sibling cancellation and instance kill — emit a terminal-error signal whose payload carries only an error class, bypassing both the typed builders and the emit-path validator; a predicate on tags for those classes compiles and then silently evaluates false. The fitness test meant to force every terminal emission through the typed builders misses them because they name their type path through a constant rather than a literal. The ruling routes both through the builders and widens the test.

## Options

- Build both settling signals through the typed terminal-error builder, emit through the validating path, and teach the fitness test to resolve a type path named by a constant; cost: none beyond the change.
- Document two classes as exceptions; cost: leaves compiled predicates that never match — no real design.

The ruling closes the gap the fitness test was written to prevent.

## Ruling

> Generated ruling (/verify-issues): Emit the sibling-cancel and instance-kill terminal signals through the typed terminal-error builder and the validating emit path so they carry tags and the attributes delta like every other terminal payload, and widen the builder-only fitness test to resolve type paths named by constants. Forced by the signal concept's unconditional invariant and the fitness test's stated intent. Verified against the tree as it stands; nothing was applied.

<!-- Owner: this is a generated ruling, not your decision. Leave it as-is to accept — the next /plan-sprint carries it, naming the generated/recommended batches at sign-off. Edit the text to redirect, empty the section to discuss live, or delete this note to adopt the ruling as your own. -->
