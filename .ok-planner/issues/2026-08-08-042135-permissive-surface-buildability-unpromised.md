---
issue: permissive-surface-buildability-unpromised
kind: human
category: coverage
artifacts:
  - decision:licensing-dual-apache-agpl
  - decision:licensing-enforced-by-license-lint
  - concept:module-layout
status: promoted
sprint: 2026-08-08-ruled-intake-drain.md
opened: 2026-08-08T04:21:35Z
---

# Nothing promises that the permissive surface is enough to build against

rimsky is dual-licensed. The protocols module is permissive; the orchestrator and
everything around it is copyleft with a commercial alternative. The whole point of
that split is that a service author who cannot take on copyleft obligations can
still write a peer — an executor, a claim producer, a publisher, a validator — and
ship it.

Two decisions record the arrangement. One makes the split and says why. The other
decides how it is held: a build-step check constraining permissive packages to
import only the standard library and permissively-licensed dependencies, with
"rely on the module split and code review" explicitly rejected because nothing
fails mechanically that way.

Both are sound, and between them they answer one question: can a copyleft import
contaminate the permissive surface? No — the check makes it impossible.

They do not answer the question a permissive consumer actually has: **is the
permissive surface sufficient to build a working peer?** A surface can be
perfectly uncontaminated and still be too thin to build against. Nothing in the
corpus states that need, so nothing is audited against it, and the check floats
free of any user-facing promise it serves.

## What the corpus does and doesn't say

Four protocol-author stories exist — executor, claim-producer, publisher,
validation. Each promises that a service implementing the protocol is discovered,
validated, dispatched to, and has its outcomes accepted; the executor one adds
"without rimsky-internal knowledge." Those are functional promises about
integration working. None of them says anything about what the author must depend
on to get there. A peer can integrate flawlessly while linking a copyleft module —
which is exactly what the examples module's own test files do today.

No story in the corpus mentions licensing, permissiveness, or module dependency.

## Why this surfaced now

The examples module's cross-stack proofs were incidentally demonstrating the
missing promise: peers built in a separate module, exercised against a real stack.
Nobody had said that was their job, which is part of why the module's value was
hard to assess when its removal came up. Whatever happens to that module, the
promise deserves to be stated and proven on purpose rather than as a side effect.

## Ruling

> Add the story. A service author who cannot take on copyleft obligations can
> build and ship a working rimsky peer depending only on the permissive module, so
> that integrating with rimsky does not put their own service under copyleft.
>
> Prove it the way a story is proven — by looking. A minimal peer that depends on
> the permissive module alone, builds, and runs against a real stack settles it.
> That is a deliberate proof of the promise, not a byproduct of demonstrating
> something else.
>
> The two licensing decisions stay as they are on their own terms; this is the
> statement of need they serve, which neither of them is the right shape to carry.
> A decision says what was chosen and why, and gets audited for whether the
> implementation honors the choice — which the import check already answers. A
> story says what a user needs, and gets audited for whether they can actually do
> it. Only the second question is currently unasked.
