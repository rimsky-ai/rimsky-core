---
issue: sixteen-design-log-contradictions-at-v0-13-0
kind: human
category: conflicting
artifacts:
  - concept:service
  - concept:instance
  - concept:terminal-resolution
  - concept:validation
  - concept:cascade-mode
  - concept:wait-set
  - concept:attribute
  - concept:node-run
  - concept:cascade
  - concept:claim-handle
  - concept:frame
  - concept:auto-terminal
  - concept:inertness
  - concept:executor
  - concept:supervisor
  - concept:message
  - concept:publisher
  - concept:message-schema
  - concept:cancel-siblings
  - concept:fan-out
  - concept:child-execution
  - concept:terminal-tag
  - concept:signal
  - concept:claim-tree
  - concept:lineage
status: answered
opened: 2026-08-06T08:07:24Z
github: https://github.com/rimsky-ai/rimsky-core/issues/53
---

# Sixteen design-log contradictions found by a full byte-exact mirror review at v0.13.0

Question: do the sixteen numbered doc-vs-code / doc-vs-doc contradictions filed against the v0.13.0 concept mirror still hold at HEAD?

Answer: no. Every one of the sixteen has already been repaired by intervening corpus work (concept docs are refreshed alongside code by sprint execution; several of these look like fallout from the same "v0.14.0 comment sweep" that issue `event-log-kind-decision-states-five-class-taxonomy` also traced). Re-verified each individually against the current corpus and, where cited, current code:

1. `concepts/service.md` Invariants now explicitly say "Lifecycle-subscriber has its own dedicated conformance subcommand" — matches `cmd/rimsky/conformance.go`.
2. `concepts/instance.md`'s kill-path invariant now reads "active claims are abandoned through their producers (falling back to a record-only abandon when a producer is unreachable, so the kill always lands)" — outbox-primary with record-only as the documented fallback, consistent with `terminal-resolution.md`'s outbox section.
3. `concepts/validation.md`'s "a lifecycle subscriber is never named by the template" is accurate: `peersReferencedBySpec` (`lib/control/controlapi/lifecycle.go`) collects only claim-producer, executor, and publisher names; `TemplateSpec` (`lib/foundation/spec/template.go`) has no lifecycle-subscriber field at all.
4. `terminal-resolution.md` is internally consistent: Stage 2 routes completion/error/infra/park through terminal-application, and the Invariants section confirms "Every kind except await-async-callback flows through the terminal-application step" — park is included; it's only excluded from the *lock-release* step.
5. `cascade-mode.md`'s `most-recent` row now explains the delete's purpose directly: "This delete runs before the gate evaluator's advanced-sibling check... it is what clears that check for `most-recent`."
6. `attribute.md`'s "Clarifying note on when defaults materialize" now explicitly unifies the two: the persisted bag is "the receiver's carry-forward-hydrated bag per the Self-state carry-forward section above — schema defaults on the scope's first dispatch."
7. `node-run.md` now states the parked-wake carve-out explicitly, twice: "with one narrow exception, the parked-wake carve-out (see `concept:cascade`)" and again in the in-flight-seal invariant.
8. `claim-handle.md` now defines the poison rule verbatim in its own Held-variant invariants ("**Poison rule (forward-propagation through abandoned claims).**"); `frame.md` doesn't own held-claim semantics (out of its stated boundaries), so it has nothing to omit.
9. `inertness.md`, `executor.md`, and `supervisor.md` agree: scratch has no mid-dispatch write channel (attached only to a settling outcome); "mid-dispatch" in supervisor.md's liveness list qualifies only "attribute writebacks," not "scratch writeback."
10. `message.md`'s payload-inertness invariant and `publisher.md`'s message-inertness invariant both now explicitly list the receipt-time body-schema validation site alongside the others.
11. `cancel-siblings.md` now attributes the strict policy's semantics to fan-out explicitly ("owned by `concept:fan-out`; `concept:node-run` only snapshots the value").
12. `cancel-siblings.md` no longer contains the phrase "strict error policy" anywhere; the malformed-value invariant now says "aggregation-policy value."
13. `terminal-tag.md` now says "Tags also ride `transient/park` payloads (a single leaf — see `concept:signal`)" — matches `signal.md`.
14. `signal.md` now carries the signal-channel qualifier in both cited spots: "on the signal channel, attribute mutation is a feature of the run-terminating verdicts only; the coexisting mid-dispatch attribute-writeback callback... also writes attributes but emits no signal at all."
15. `claim-tree.md` consistently attributes run-tree *structure* to the run-scope ledger in both cited spots, and only its *consumption for state aggregation* to node-run — not a contradiction, a coherent split.
16. `lineage.md` uses "not reconstructed from them" consistently in both the "What it is" section and the Invariants section.

The coda (flat-catalog headers instructing readers to read `stories/<slug>.md` and grep `@concept:` in source) describes a rimsky-docs publishing choice about its own external mirror, not a defect in this repo: `.ok-planner/design/stories/` and `@concept:` annotations both genuinely exist and work as documented here.

No corpus edits were needed — every cited line was already brought into line with its counterpart by prior work.
