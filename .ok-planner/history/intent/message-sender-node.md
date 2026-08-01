# Intent Dossier: message-sender-node

Distilled 2026-07-13 from session transcripts (2026-06-12..2026-07-13) and ok-planner
history artifacts (2026-05-04..2026-06-11). Transcript tier outranks artifact tier;
later intent supersedes earlier. Part of the drift-remediation intent ledger.

## Net position

All record on this concept is transcript-tier (user's own words or user-ratified) — highest authority.

- Message sending is a **dedicated node kind**, not a capability of arbitrary nodes. The sender node subscribes to any nodes in the graph with full attribute substitution; its dispatch builds a message whose body IS its resolved attribute set (2026-06-14).
- It is implemented as a **built-in utility node** — an in-process executor (`rimsky.emit_message` builtin / `inproc://emit_message`) wired by template sugar canonicalized at registration — using the standard dispatch interface. No special-case branches in dispatch, terminal-resolution, or the scheduler (2026-06-19).
- **Exact shape match**: the sender node's attribute schema must exactly match the destination message type's body schema — no mapping layer, no extras, no superset serialization (2026-06-14).
- The envelope inserts in its **own transaction during the handler call**, not the node's terminal-resolution transaction; at-most-once delivery across dispatch retries comes from the deterministic idempotency key (node-id, frame-id), not transaction coupling (2026-06-23). A sent message is never revoked on rollback (2026-06-19).
- Cascade-from-message is the **same code path** as cascade from any settled run (the message virtual node exists precisely for this); no special "this cascade came from a message" handling anywhere (2026-06-22).
- Vocabulary is load-bearing: messages are **SENT** (directed push to a specific instance's queue, mailbox semantics); signals are **EMITTED** (broadcast, receivers opt in). node_runs may emit events and send messages; nodes do not emit messages. The concept slug is message-**sender**-node, renamed from message-emitter-node (2026-07-03/05).

## Required behaviors (open promises)

- A node declares message emission instead of an executor, subscribes to any graph nodes with full attribute substitution, and its dispatch builds a message whose body is its resolved attribute set — "a message is literally a node. it can subscribe to any other node in the graph, with full attribute substitution. its terminal state enqueues a message. that way it can aggregate results from multiple nodes." (2026-06-14, bfc9febb, transcript, user)
- Exact attribute-set ↔ body-schema match enforced; authors with extra attributes keep them on a triggering node and add a dedicated message-dispatch node with the exact shape — "users can always add an extra message-dispatch node with exact match and leave the superset in a triggering node." (2026-06-14, bfc9febb, transcript, user)
- Implemented as a built-in utility executor through the standard dispatch interface — "the emit message node was designed before we had in-proc executors. now that we have those, emit message should be implemented as a utility node." (2026-06-19, 8a3b8c19, transcript, user)
- Envelope insert in its own transaction during the handler call; at-most-once across retries via the (node-id, frame-id) idempotency key (2026-06-23, 10cf843b, transcript — the concept doc's same-tx invariant was ruled false and rewritten).
- No special cascade path for message-originated cascades — "there should be no special 'this cascade came from a message'." (2026-06-22, 10cf843b, transcript, user)
- Repo-wide send-vs-emit vocabulary holds across proto, YAML DSL, executor SDK, slugs, and prose — "node_runs may *emit* events and may *send* messages. nodes do not *emit* messages. a message is a thing that is sent and goes into a queue, with its payload fully self-contained" (2026-07-05, 3f71f90a, transcript, user). Includes the rename of the `EmitCascadeMessage` code symbol and the message-side 'emit' phrasings; signal-side 'emit' language stays (2026-07-03, 3f71f90a).
- Two stories replace cross-frame-coupling, one per layer: an iteration story (cascade self-edge cycles bounded by CEL `when:` predicates over run-local data) and a message-send story (a node sends a message to its own instance's ledger, delivered like any message). Cross-frame loop bounding comes from `when:` predicates over message payloads, never the diff-gate (2026-07-03, 3f71f90a, transcript).

## Intentional absences

- **"Any node can emit a message when settled"** — rejected: emission is a dedicated node kind, because multi-source body composition needs the emitter to subscribe to its senders, and a hidden emit would have no topology entry, audit handle, or dashboard surface (2026-06-19, 8a3b8c19).
- **The original weld** — `emits_message` special-case branches in dispatch, terminal-resolution, and the scheduler, plus the panic guard against the sub-graph-entry combination — replaced by the utility-node implementation; must not resurface (2026-06-19, 8a3b8c19).
- **Per-node-type `emits:` block** from the earlier cascade-emits sketch — superseded by the node-kind design (2026-06-14, bfc9febb).
- **story:queue-drain-converges** — retired outright: queue drain needs no cross-frame support; the sender node decides within the current frame whether to send the next message; empty queue → no message → no next frame. Its substance folded into the existing message-sending story; the diff-gate cross-frame-convergence test was deleted (2026-07-05, 3f71f90a, reversal).
- **Diff-gate cross-frame convergence clause** of the old cross-frame-coupling story — dissolved entirely in the 2026-07-03 story split.
- **"Every node is a message"** (collapsing in-frame cascade) — explicitly deferred to a future brainstorm, not promised (2026-06-14, bfc9febb).

## Corrections and restorations (drift-fight record)

- **Same-tx invariant was false doc** (2026-06-23, 10cf843b): the concept doc claimed the envelope inserts in the node's terminal-resolution transaction; user ruled the doc wrong ("yes, fix the doc") — the emitter is a built-in utility executor whose insert is its own transaction, idempotency-keyed. Precedent: doc rewritten to match the implemented (and correct) architecture.
- **Misleading story slug** (2026-07-03, 3f71f90a): cross-frame-coupling wrongly suggested a frame-isolation violation; user ruled it is "literally just 'I want to send a message from a node to its own instance's message queue'" and the story was renamed/split.

## Superseded / historical

- `emits_message` as YAML surface and as runtime special case → utility-node builtin + send-vocabulary rename; `emits_message` also appears on the 2026-07-11 retired-mechanisms list (see _retired dossier).
- message-emitter-node slug and `EmitCascadeMessage` symbol → message-sender-node / send-side naming (2026-07-03).
- story:queue-drain-converges (created in the item-4 split, commit 51f21d65) → retired days later (2026-07-05).
