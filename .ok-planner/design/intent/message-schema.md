# Intent Dossier: message-schema

Distilled 2026-07-13 from session transcripts (2026-06-12..2026-07-13) and ok-planner
history artifacts (2026-05-04..2026-06-11). Transcript tier outranks artifact tier;
later intent supersedes earlier. Part of the drift-remediation intent ledger.

## Net position

- Templates carry a `messages:` registry declaring message **types** (type-path grammar, e.g. `flush/cache`) with a body schema; the registry is the template-level contract for everything entering the instance as a message. (The 2026-06-06 artifact listing "template-level messages: schema" among dropped sketch features is fully superseded by the June 14+ transcript work that built it.)
- A message is **a node**: each declared type materializes as a literal message-receiver node row at instance creation (empty executor, pass-through); delivery creates a node-run in stale with `creation_reason: message_delivery`, populates the run's attribute bag from the body, and lets the scheduler settle it via the existing pure_cascade empty-executor path, firing standard terminal/success cascade to subscribers.
- Routing is subscription-pull, not schema-push: nodes subscribe to message types through the normal `subscribes:` block; the `invalidates:` field is gone; the envelope `target` field is gone; `kind` renamed to `type` and the legacy single value `invalidate` is retired.
- Message bodies are typed attribute blocks served by the same substitution code and grammar as node attributes; `{{messages.<type>.<field>}}` is sugar for `{{nodes.<type>.<field>}}` plus a registration-time check that the type is a declared message entry point.
- `frame: next` is retired entirely: cross-frame triggering (self-subscription, cross-cutting, cycle back-edges) is expressed as emit-message-then-new-frame.
- One message per frame is load-bearing: every frame carries at most one delivered message; N posts produce N frames.
- Every template's declared-types set carries an implicit `""` (empty-string) type with a null body schema — the empty wake — handled with **zero special-case code**.

## Required behaviors (open promises)

- A message whose type is not in the template's `messages:` registry is refused loudly at receipt (HTTP 400-class, naming the unknown type and the declared set), never silently dead-lettered; a declared type persists to the ledger and opens a frame (2026-06-14, bfc9febb, transcript).
- One-message-per-frame: substitution from the message body is always well-defined; no template ever faces a multi-message coalesced frame (2026-06-14, bfc9febb, transcript).
- Body bytes are NOT validated at receipt: `body_schema` serves registration-time substitution-ref checking; actual validation happens when a receiver pulls values via substitution at dispatch through the attribute-validation gate (2026-06-14, bfc9febb, transcript, user chose this explicitly).
- Registration-time validation in both directions: reading an undeclared message field rejects; composing an outgoing body writing a field the destination schema lacks rejects (2026-06-14, bfc9febb, transcript): "message payloads … are essentially just attribute blockes that can carry over between frames."
- Message emission is a node dispatch mode: a node declares `emits_message: <type>` instead of an executor, subscribes normally with full attribute substitution, and its dispatch builds a message whose body IS its resolved attribute set; the emit-node's attribute schema must exactly match the destination body schema — no mapping layer, no superset (2026-06-14, bfc9febb, transcript, user): "a message is literally a node… that way it can aggregate results from multiple nodes."
- Message types materialize as message-receiver node rows at instance creation; delivery = registry check, node-run in stale (`message_delivery`), attribute bag from body, scheduler settles via pure_cascade (2026-06-22, 10cf843b, transcript, user): "the message-node is basically a pass-through no-op."
- `{{messages.<type>.<field>}}` resolves through the same Deps lookup as `{{nodes.…}}`; registration checks the type is a declared message entry point (2026-06-18, 9fb55f08, transcript, user).
- Implicit `""` type seeded at registration with null body schema; author-declared `""` entries rejected as reserved-for-runtime; the receipt handler has no empty-case branch; the empty type's receiver node is an ordinary message-receiver node whose type is literally the empty string — no sentinel name; a non-root node may explicitly subscribe to `""` (no double-fire; auto-injection skips nodes with any subscribes entries) (2026-06-15/16, 4c42fe5b, transcript; reconfirmed binding 2026-07-03, 3f71f90a).
- Empty-wake messages follow the SAME code path as named messages — a separate empty-wake cascade path is drift by definition (2026-07-03, 3f71f90a, transcript, user): "the whole point of the syntetic message type injected in the template to handle empty wake was so that the code path would be the *same*."
- Bundled sensors work exactly as before the message-schema layer, absorbing only the new message format (2026-06-15, 91ec93d1, transcript, user).
- The five cross-stack e2e proofs (sensor cron/http/webhook/object-store + publisher example) exist, mechanically patched to the new wire shape (kind→type, drop target, new subscription DSL), all passing (2026-06-15, 91ec93d1, transcript, user).
- The control-API MCP `message_send` descriptor mirrors the current wire shape: advertises `type` (required), no retired `kind`/`target` fields (2026-06-15, 91ec93d1, transcript).
- Publisher-subscription `message_type` is validated against the target instance's message registry (2026-06-14, bfc9febb, transcript).

## Intentional absences

- **`frame: next` / the `frame:` subscription modifier** — retired in favor of message-driven cross-frame triggering (2026-06-14, 37e2ea5e, transcript, user).
- **`invalidates:` on messages entries** — dropped; nodes subscribe to messages, messages don't name targets (2026-06-14, bfc9febb, transcript, user).
- **Envelope `target` field and `kind: invalidate`** — dropped/renamed (2026-06-14).
- **`supersede_pending` (per-type latest-wins queue replacement)** — deliberately omitted despite acknowledged value (2026-06-14, bfc9febb, transcript, user): "let's also omit supersede_pending, though we see its value."
- **Sender-side `emits:` block (parallel to publishers:)** — superseded the same day it was sketched by the emit-node dispatch-mode model (2026-06-14).
- **Author override of the empty-message entry point** — out of scope; `""` is reserved for the runtime; a separate later spec if ever wanted (2026-06-15, 4c42fe5b, transcript, user).
- **"Every node is a message" (collapsing in-frame cascade)** — explicitly deferred to a future brainstorm, not promised (2026-06-14).
- **`concept:invalidate` and `concept:backfill` as standalone concepts** — retired with no successor slug; substance moved into the message machinery, the message-emitter node kind, and the operator message endpoint (2026-06-14, bfc9febb, transcript, user-confirmed catalog restructuring).

## Corrections and restorations (drift-fight record)

- **Deleted e2e proofs restored** (2026-06-15, 91ec93d1): the message-schema plan retired the five cross-stack proofs in favor of in-process tests; the user overruled — "crasy to throw them out (or rewrite them from scratch) when all that changes was the message shape" — and they were restored and mechanically patched. Precedent: wire-shape migrations patch existing proofs; they do not delete coverage.
- **Separate empty-wake code path** (2026-07-03, 3f71f90a): `cascadeEmptyMessageWakeInTx` as a distinct path was ruled drift against the committed empty-message-as-root-trigger decision; empty wake must run the ordinary message path. User ordered the fix and had the decision added to the ledger.

## Superseded / historical

- 2026-06-06 artifact scope-policy dropping "template-level messages: schema" and "one-message-per-frame invariant" as sketch-only → superseded by the June 14+ transcript decisions that made both real (transcript outranks artifact; later supersedes earlier).
- Sender-side `emits:` block (2026-06-14 morning) → emit-node dispatch mode `emits_message:` (2026-06-14 afternoon).
- `invalidates:` targeting on message entries → subscription-based routing (2026-06-14).
- Typed-message substitution via a dedicated TriggerMessagePayload channel → unified Deps lookup, `messages.` as sugar (2026-06-18).

## Conflicts needing human ruling

- **Receipt-time body validation**: 2026-06-14 rules body bytes are NOT validated at receipt (validation deferred to receiver dispatch), while the 2026-06-22 delivery model says "validate the body against the message schema" before populating the attribute bag ("push attributes into message node after checking schema"). Both are user-origin transcript entries; strict later-supersedes-earlier favors 2026-06-22, but the 06-22 phrasing may mean only the registry/shape check for attribute-bag population rather than full value validation. Adjudicators hitting a finding on receipt-time validation should get a human ruling on which check the delivery path is required to perform.
