---
issue: stub-executor-declares-tags-it-never-echoes
kind: human
category: test-coverage
artifacts:
  - concept:tag
  - concept:conformance
  - concept:executor
  - decision:terminal-tags
status: promoted
sprint: 2026-08-08-ruled-intake-drain.md
opened: 2026-08-07T21:53:21Z
github: https://github.com/rimsky-ai/rimsky-core/issues/99
---

# The in-tree reference stub advertises five tags and echoes none

rimsky ships a stub executor in-tree — the reference someone reads to learn what
a protocol-conformant executor does. It advertises five tags in its capabilities
and never reads the attribute that asks it to echo one back.

That combination is exactly what the conformance suite's tag round-trip scenario
tests. The scenario picks the executor's first declared tag, sends it, and
requires it back on the settling success. Because this stub declares tags, the
scenario would dispatch — and because the stub never reads the attribute,
nothing comes back, and it fails. It passes today only because nothing points the
conformance runner at this stub.

So the defect is latent rather than active, and its cost is in the reading rather
than the running: the next person implementing an executor copies a reference
that would not pass the suite it exists to illustrate. The declaration is also
untrue on its own terms — declared tags are meant to advertise an executor's
actual emit vocabulary (`decision:terminal-tags`), and these five name nothing
the stub ever emits.

Verified in the current tree: the five tags are declared in the stub's
observability capabilities, and the stub reads five other stub-mode attributes
but never the tags one.

## Options

- **Echo the requested tag on success.** Makes the stub self-consistent and lets
  the conformance runner actually be pointed at it. Adds a small amount of
  behavior to a file whose value is partly in being minimal.
- **Drop the declared tags** so the scenario correctly self-skips. Smallest
  change, and honest about what the stub emits — but it removes the reference's
  only illustration of the tag mechanism, leaving an implementer with nothing
  in-tree to copy for the one scenario most likely to trip them up.

The ruling decides whether the in-tree stub demonstrates tag round-tripping or
opts out of it.

## Ruling

> Echo it. The stub echoes the requested tag on the success outcome; the five
> declared tags stay and become true.
>
> Rationale: this file's job is to be read. Of the two ways to remove the
> contradiction, the other leaves a reference that demonstrates every stub-mode
> probe except the tag round-trip, which is the scenario an implementer is most
> likely to get wrong — the sibling issue about the stub-mode package doc exists
> because the tag probe's gate went undocumented. Making the stub echo also means
> it can be pointed at the conformance runner without further work, which is the
> check that would have caught this. The declared-tags contract argues the same
> way: it is supposed to advertise real emit vocabulary, and satisfying it by
> emitting beats satisfying it by advertising less.
