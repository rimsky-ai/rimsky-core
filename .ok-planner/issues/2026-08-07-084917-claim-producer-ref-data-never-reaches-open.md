---
issue: claim-producer-ref-data-never-reaches-open
kind: human
category: muddy-boundary
artifacts:
  - concept:claim-producer
  - concept:validation
status: verified
opened: 2026-08-07T08:49:17Z
github: https://github.com/rimsky-ai/rimsky-core/issues/69
---

# A template can attach data to a claim that the claim producer is asked to approve and never receives

Before a node runs, rimsky acquires whatever resources it declared from a
**claim producer** — an external service that owns a resource and hands out
time-limited claims on it. A node's declaration of a claim can carry a `data:`
blob: opaque JSON the template author writes to parameterize the claim.

At template registration rimsky forwards that blob to the producer for
validation, byte-verbatim (`lib/runtime/validation_pipeline.go#182`, with a
test asserting the verbatim forwarding). The producer inspects it and can
reject the template.

It never arrives at acquisition. The structure rimsky builds when it actually
opens the claim carries a producer name, a selector, an intent, an alias, a
template and instance id, a run scope, and a lifetime — and no data field
(`lib/protocols/claimproducer/types.go::ClaimSpec`). The wire message for
opening a claim has no field for it either. Nothing else in the tree reads the
blob. So its entire lifecycle is: an author writes it, a producer is asked to
approve it, and it is discarded.

Both halves of that are wrong for whoever is on the other side. A producer
author is handed a value to validate and reasonably concludes it will be handed
back when the claim opens — that is what validation is *for*. A template author
writes a field the schema accepts and the producer approves, and gets no
behavior from it whatsoever, with nothing anywhere reporting that.

The corpus is silent. Neither the claim-producer concept nor the validation
concept mentions a data field on a claim declaration at all, so nothing in the
design record says whether this is a feature that was never finished wiring or
a surface that should not exist.

## Options

- **Carry the blob through to acquisition** — add it to the open request and
  thread it to the producer. Makes the validation honest and gives template
  authors a real parameterization channel; costs a wire field and a change to
  every producer implementation's expectations.
- **Delete `data:` from the claim declaration entirely.** Removes a field that
  currently does nothing; costs the surface, and any template already carrying
  one stops registering.
- **Stop forwarding it for validation but keep the field.** Not really an
  option: it leaves an accepted field with no effect and no validation, which is
  strictly worse than either of the above.

The ruling decides whether a claim's data blob becomes a real input to
acquisition or leaves the template surface.

## Ruling

> Recommended ruling (/verify-issues): carry the blob through to acquisition, so
> the producer receives what it was asked to approve. Validation exists to let a
> producer reject a claim it could not serve, and asking it to judge a value it
> will never be given is not validation of anything — the fact that rimsky
> already forwards the blob byte-verbatim, with a test pinning that behavior,
> says the intent was always for it to matter.
>
> Rationale: deleting the field is the cheaper close and defensible under the
> project's pre-v1 stance on unfinished surfaces, but it removes the only
> per-node channel a template author has for parameterizing a claim, and the
> plumbing already exists on the registration side — this is half-built rather
> than abandoned. What would change this call: if there is a deliberate reason
> a producer must not see per-node data at acquisition — a boundary the claim
> vocabulary is meant to keep clean — then the field should go, and the
> registration-side forwarding with it.

<!-- Owner: this is a recommendation, not your decision. Leave it as-is to
accept — the next /plan-sprint carries it, naming the generated/recommended
batches at sign-off. Edit the text to redirect, empty the section to discuss
live, or delete this note to adopt the ruling as your own. -->
