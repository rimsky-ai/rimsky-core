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

Rimsky's design corpus separates two kinds of claims: a concept's *invariants* (durable properties of what a thing fundamentally is) and *decisions* (choices made among real alternatives, each required to carry a proof — a mechanical check that fails if the choice is silently violated). The concept describing the host-agent proxy — the component that relays dispatched work between a rimsky deployment and service processes spawned on a developer's machine — lists eleven invariants, and three of them read like decisions wearing the wrong hat: the proxy is the *only* place that rewrites callback URLs handed to spawned processes; concurrent dispatches to one spawned process share a single connection (and only one spawn is ever issued, even when dispatches race); and a spawned binary's capabilities are checked when it starts rather than at registration. Each has a plausible alternative someone could have built — which is precisely the test for being a decision.

The complication is provability. A decision must carry a falsifiable proof, and the three candidates aren't equal: the no-double-spawn-under-a-race claim has a crisp test shape (race the dispatches, count the spawns); the only-rewriting-site claim is closed-world — proving nothing *else* rewrites URLs is hard to check mechanically; the deferred-capabilities claim sits in between. The corpus already blurs this line elsewhere (one existing decision is more implementation-grained than these invariants), so this is partly a one-file fix and partly a precedent question.

## Options

- **Leave all three as invariants** — free today; they can never carry a decision-grade proof, and reviews will keep flagging them.
- **Extract all three into decisions** — matches the rule; two of the three would carry proofs that can't genuinely fail, which the rule itself says makes them not-decisions.
- **Extract only the provable one** (single-spawn multiplexing, with a concurrency test); leave the two closed-world claims as honest invariants.

The ruling decides each claim's home, whether real proofs are achievable for the extracted ones, and whether to state a sharper general line for the corpus.

## Ruling

> Recommended ruling (/recommend-rulings): Middle path: extract only
> the multiplexed-dispatch/single-Spawn claim into a decision with a
> concurrency-test proof; the URL-rewriting-boundary and deferred-
> capabilities-handshake bullets stay as concept invariants.
>
> Rationale: Extract where a crisp, exhibitable falsifier exists —
> races on Spawn are provable; the closed-world rewriting claim isn't
> mechanically falsifiable, and an unprovable decision is worse than
> an honest invariant.

<!-- Owner: this is a recommendation, not your decision. Leave it
as-is to accept — the next /plan-sprint carries it, naming the
recommended batch at sign-off. Edit the text to redirect, empty
the section to discuss live, or delete this note to adopt the
ruling as your own. -->
