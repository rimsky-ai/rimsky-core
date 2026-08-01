---
decision: race-injection-hooks
status: as-is
---

# Deterministic race injection at defended seams

## Choice

The runtime's deterministic injection-hook pattern (a post-commit hook a test can use to force a precise interleaving) extends to deterministic injection tests at the defended concurrency seams: the acquire-unavailable abandon path, the folded ownership-bail path, the held-claim aggregate check-and-fire, and the orphan-reaper vs in-flight-terminal overlap.

## Rationale

These are designed defenses against inherent multi-replica collisions; deterministic forcing proves the defense and pins it against refactors — strictly stronger than probabilistic race-detector luck.

## Alternatives

- Relying on the repeated race-detector gate alone (see `decision:race-gate-split`) — rejected: probabilistic; a scheduler-dependent interleaving can survive any finite repetition budget unexercised.
- Sleep-based timing tests to provoke the interleavings — rejected: nondeterministic verdicts, which the project's test discipline forbids outright.
