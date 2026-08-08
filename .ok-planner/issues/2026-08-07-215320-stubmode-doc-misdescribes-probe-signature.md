---
issue: stubmode-doc-misdescribes-probe-signature
kind: human
category: doc-drift
artifacts:
  - concept:conformance
status: promoted
sprint: 2026-08-08-ruled-intake-drain.md
opened: 2026-08-07T21:53:20Z
github: https://github.com/rimsky-ai/rimsky-core/issues/98
---

# The doc that defines stub-mode conformance describes a contract that fails it

rimsky ships a conformance suite that third-party executors run to prove they
speak the protocol. Part of that suite drives executors through "stub mode" — a
set of magic attributes that ask an executor to behave in a specific, checkable
way. One package holds the canonical definition of those attributes, and its
package doc promises that an executor reproducing exactly this contract is
stub-conformant.

An executor that does exactly what the doc says fails the suite.

**The cancel probe is described as half of what it is.** The doc says the probe
asks the executor to hold the dispatch open until cancelled. The scenario
actually registers two fixed acknowledgement ids and fails on timeout if either
never arrives. The real contract is four steps: post to a cancel-observed
callback, block, post to a cancel-acknowledged callback, and return a cancelled
status — returning success fails. An implementer who follows the doc gets a
timeout error naming a callback the doc never mentions.

**The async probe is written as mandatory and is a capability check.** It carries
the same imperative voice as the required probes, but the runner treats any
answer other than an async handoff as "not supported" and skips the two async
scenarios. One of rimsky's own bundled executors deliberately opts out this way,
with a test pinning that it does.

**The claim that the signature is defined nowhere else is false**, and this is the
part that matters beyond wording. Five string literals that are part of the
signature live outside the package: the two cancel acknowledgement ids appear at
three sites each, and the reserved malformed-shape marker at three more. That is
precisely the fragility the package exists to eliminate, and the conformance
concept states it as an invariant — the stub-mode signature is defined once, in a
shared definition every issuing and asserting site imports. Six duplicated call
sites are a live violation of a stated invariant, not a style preference.

**The tag probe omits its gate.** It's stated unconditionally, but the scenario
only ever requests a tag the executor has already declared, and passes without
dispatching when none are declared.

All four points were re-verified against the current tree.

## Ruling

> Generated ruling (/verify-issues): fix both halves. Rewrite the package doc so
> the cancel probe states its full four-step contract, the async probe reads as
> the capability check it is, and the tag probe names its declared-tags gate. Then
> pull the five stray literals into the package as named constants — the two
> cancel acknowledgement ids and the malformed-shape marker — and repoint all six
> duplicate sites at them, which also lets the doc's "defined once" claim become
> true rather than deleted. The constant extraction is forced by the conformance
> concept's own invariant that the stub-mode signature is defined once and
> imported everywhere; the doc corrections are forced by direct contradiction with
> the scenarios the doc claims to describe.
> Verified against the tree as it stands; nothing was applied.
