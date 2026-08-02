---
issue: stories-claim-producer-backend-collapse
kind: sprint
category: stories-collapses
artifacts:
  - story:claim-producer-filesystem
  - story:claim-producer-postgres
  - concept:claim-producer
status: promoted
sprint: 2026-08-01-ruled-intake-drain.md
opened: 2026-08-01T22:31:20Z
---

# Are the two bundled claim-producer stories duplicates — and where does the filesystem pick-policy shape live?

Two stories cover the bundled claim producers (shipped services that grant workflow nodes exclusive claims over external data): one against the filesystem, one against Postgres. The filed worry is that they tell one outcome — "a production-grade bundled producer" — once per backend, the pattern the story rules collapse. Re-verification shows otherwise: the two promise materially different guarantees. The filesystem producer promises synchronous in-place writes (no staging), directory-per-scope claims, and folder-derived partitioning; the Postgres producer promises row-locking claims, optional atomic staging (stage-then-swap at commit), and its own verifier checks. These are different contracts a user picks between, not one contract behind two doors. The producer-protocol concept (`concept:claim-producer`) stays backend-agnostic by design and never commits to one canonical guarantee set, consistent with the stories being genuinely distinct.

Separately, the filesystem story carries prose past its sentence: pick-policy actions at commit/abandon time, and the three-value request discriminator for scope-splitting (`list` / `batch_pick` / `expand_folder`). The claim-producer concept documents scope-splitting generically but never enumerates that discriminator — the story tail is the corpus's only copy, and reducing the story to its sentence (which the format rules force) orphans it.

## Options

- Collapse to one bundled-producer story with backend as a decision — erases the different write-semantics/staging/verifier commitments, which are not interchangeable.
- Keep both stories, reduce the filesystem tail, and absorb the pick-policy semantics into the producer's documented scope-splitting contract, leaving the discriminator's spelling to the protocol surface — one commitment moves, nothing collapses.

The ruling decides whether backend is surface or substance here, and what happens to the pick-policy content. Rule together with `issue:stories-fanout-partition-collapse`, `issue:stories-bundled-sensor-collapse`, and the stray-commitment siblings (`issue:story-claude-agent-restructure`, `issue:story-bundled-park-resume-recipe-mechanism-home`).

## Ruling

> Recommended ruling (/verify-issues): keep both per-backend stories and reduce the filesystem tail to its sentence; fold the pick-policy-at-commit/abandon semantics into the producer's documented scope-splitting contract, and leave the three-value discriminator's spelling to the protocol definition. Backend is substance here — the two producers promise different write-semantics and staging contracts, which is exactly what the collapse rule says stays separate.
>
> Rationale: this is the same line drawn for the sensors — collapse only where outcomes coincide, and the fan-out list pair is the only place they do; here a user choosing filesystem-vs-Postgres is choosing between guarantee sets, not deployment targets. The pick-policy semantics are behavior users rely on and so belong in the durable contract, while the discriminator enum is protocol spelling that would only rot in a second home. Flip case: if the two producers converge on identical write-semantics and staging behavior, the stories become the duplicate the filer saw and the collapse is forced.

<!-- Owner: this is a recommendation, not your decision. Leave it
as-is to accept — the next /plan-sprint carries it, naming the
generated/recommended batches at sign-off. Edit the text to
redirect, empty the section to discuss live, or delete this note
to adopt the ruling as your own. -->
