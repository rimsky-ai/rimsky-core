---
decision: message-queue-mode-per-instance
---

# Message-queue coalesce mode is per-instance, not per-message-type

## Choice

An instance's pending-message coalesce behavior is a single per-instance setting (`message_queue_mode`), declared on the template and materialized onto each instance row at creation. Legal values: `backlog` (default) and `coalesce`. The setting applies uniformly to every message type on that instance's queue; there is no per-message-type variant of the setting and no per-message override. Template registration surfaces a non-fatal validator warning when a coalesce-mode template declares two or more distinct message types, since coalesce cancels pending messages across types — the author is choosing latest-wins for the whole queue, not per type.

## Rationale

The user need being served by the coalesce mode is "this instance is slow relative to its wake cadence; drop stale wakes so it doesn't fall arbitrarily far behind." That need is a property of the instance's throughput vs its message cadence — not a property of a particular message type. An operator saying "for this instance, I only care about the latest wake" naturally addresses the whole queue; splitting that decision across message types would leave the operator writing per-type rules that all fold into the same intent.

The alternative — per-message-type coalesce scope — was considered and rejected. It has two costs. First, it puts the operator on the hook to enumerate every declared message type and mark each one coalesce or backlog, which is coordination work for an outcome nobody asked for. Second, it creates a legibility trap: under per-type coalesce, "which pending messages get dropped when a new type-A arrives?" has an answer that depends on both settings and message-type sameness, whereas under per-instance coalesce the answer is simply "all prior pending, regardless of type." The simpler answer is also the one that matches the stated user need.

The naming — `message_queue_mode`, values `coalesce` and `backlog` — is deliberately distinct from the per-node `cascade_mode` (`most-recent` / `sequenced` / `idempotent-*`) that governs intra-frame cascade-round coalesce in the node-run queue. The two settings share the shape "coalesce older work when new arrives" but live at different layers, guard different invariants, and are set by different roles; sharing vocabulary is the collision that motivates the different verb here.

## Alternatives

- **Per-message-type coalesce scope.** Rejected for the reasons above: coordination cost without a user need pointing at it, plus legibility loss on the "which prior get dropped" question.
- **Per-instance override at creation only (no template default).** Rejected as inconsistent with how other queue-shape settings are declared on the template and inherited by every instance of it. The template is the natural home for the default; instances materialize the value they inherit.
- **Global default without opt-in.** Rejected because the two shapes have different failure modes: `backlog` drops nothing (payload preservation guaranteed), `coalesce` drops arbitrarily many older messages (payload preservation not guaranteed). A default that discards data by default is the wrong shape.
