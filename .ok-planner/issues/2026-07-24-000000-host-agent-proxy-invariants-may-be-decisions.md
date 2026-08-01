---
issue: host-agent-proxy-invariants-may-be-decisions
kind: human
category: muddy-boundary
artifacts:
  - concept:host-agent-proxy
status: verified
opened: 2026-07-24T00:00:00Z
---

# Three "invariants" look more like choices someone made — do they belong in a different kind of document?

Rimsky's design corpus separates a concept's *invariants* (durable properties of what a thing fundamentally is) from *decisions* (choices made among real alternatives). The concept describing the host-agent proxy — the component that relays dispatched work between a rimsky deployment and service processes on a developer's machine — lists eleven invariants, and three read like decisions wearing the wrong hat: the proxy is the *only* place that rewrites callback URLs handed to spawned processes; concurrent dispatches to one spawned process share a single connection, with only one spawn ever issued even when dispatches race; and a spawned binary's capabilities are checked at startup rather than at registration. Each has a plausible alternative someone could have built — which is the decision-definition's own test for being a decision. Re-verified today, all three bullets stand unchanged.

The complication that used to dominate this question has dissolved: under the previous guidance a decision had to carry a written proof, and two of the three candidates (the closed-world "only rewriting site" claim, the deferred handshake) could never carry one that genuinely fails. Decisions no longer carry proof sections at all — verification is the periodic implementation audit's job — so extraction is now just authoring a Choice/Rationale/Alternatives file and dropping the bullet from the concept. What survives of the old asymmetry is about the *audit*, not the form: the single-spawn-under-race claim is something an audit can settle by pointing at a concurrency test; the closed-world claim can only ever be audited as "nothing found to the contrary." No rule requires extracting decision-shaped content already living as a concept invariant, so this stays a judgment call — partly about these three bullets, partly about the precedent for a corpus that blurs this line elsewhere.

## Options

- **Extract only the single-spawn multiplexing claim** into a decision (a real choice with a crisp auditable shape); leave the two closed-world claims as honest boundary invariants of what the proxy *is*.
- **Extract all three** — maximal rule fidelity; the two closed-world decisions audit permanently as "nothing found to the contrary."
- **Leave all three as invariants** — free today; reviews will keep flagging them.

The ruling decides each claim's home and what precedent the corpus keeps.

## Ruling

> Recommended ruling (/verify-issues): extract only the
> multiplexed-dispatch/single-spawn claim into a decision; the
> URL-rewriting-exclusivity and deferred-capabilities-handshake
> bullets stay as concept invariants.
>
> Rationale: the multiplexing behavior is a genuine choice among
> alternatives with an audit-settleable shape, while the other two
> are boundary statements about what the proxy is — the concept's
> native territory — and moving them buys no audit leverage now that
> decisions carry no proofs. The flip case: if either closed-world
> claim ever gains a mechanical enforcement (a lint or fitness test
> proving exclusivity), it becomes extractable on the same terms as
> the multiplexing claim.

<!-- Owner: this is a recommendation, not your decision. Leave it
as-is to accept — the next /plan-sprint carries it, naming the
generated/recommended batches at sign-off. Edit the text to
redirect, empty the section to discuss live, or delete this note
to adopt the ruling as your own. -->
