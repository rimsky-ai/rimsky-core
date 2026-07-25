---
issue: story-host-agent-anonymous-mode-proof-under-exhibits
kind: audit
category: proof
artifacts:
  - story:host-agent-anonymous-mode
  - code:test/scenarios/host_agent_anonymous_multi_agent_isolation_test.go
status: verified
opened: 2026-07-24T00:00:00Z
---

# The test that promises "work never reaches the wrong agent" can't actually tell which agent got the work

Rimsky's design corpus backs each user promise ("story") with a proof — a test that must genuinely be able to catch the failure it guards against. The story about anonymous host agents (developer machines that connect without credentials, each under a distinct routing label, to run dispatched work) promises isolation: work targeted at one agent's instances reaches that agent, never a concurrently-connected other. Its proof — a test spinning up two anonymous agents and one targeted instance each — asserts only that both instances finish with exactly one dispatch apiece. It never checks *which* agent did the work, because the stub program standing in for a real agent records nothing about its own identity. A routing bug that silently swapped the two agents' work — completing everything correctly, on the wrong machines — would pass this test green.

The gap isn't an overreaching proof description that could be quietly trimmed: the story's Acceptance and Falsifier both independently promise the never-the-other-agent property, so weakening the proof text would leave the story contradicting itself. The proof was authored recently, which makes this the cheap moment to fix it — and a sibling issue proposing to split this story in two means the routing-specific proof would travel with whichever half keeps the isolation claim.

## Options

- **Strengthen the test**: thread the routing identity into the stub's environment at spawn (the spawn path already knows it), have the stub report it back, and assert per instance that the right agent did the work. Cross-routing then genuinely fails the test.
- **Weaken the story** to what's tested today ("each instance terminates with its target agent connected") — requires rewriting Acceptance and Falsifier too, surrendering a real guarantee.
- **Keep this test as the no-duplicate-dispatch proof and add a companion test** for routing correctness — stories may cite multiple proofs.

The ruling decides: strengthen (or add a companion) versus narrow the promise — coordinated with the story-split sibling.

## Ruling

> Recommended ruling (/recommend-rulings): Preserve intent: strengthen
> the proof — the stub child records its agent/routing identity
> (threaded into its environment at spawn) and the test asserts it per
> instance, so a cross-routed dispatch genuinely reddens. Lands with
> the isolation story from the bundling split.
>
> Rationale: The Acceptance already promises 'never reaches the other
> agent'; relaxing the Proof would leave the artifact contradicting
> itself. The strengthening is small — the spawn path already knows
> the routing identity.

<!-- Owner: this is a recommendation, not your decision. Leave it
as-is to accept — the next /plan-sprint carries it, naming the
recommended batch at sign-off. Edit the text to redirect, empty
the section to discuss live, or delete this note to adopt the
ruling as your own. -->
