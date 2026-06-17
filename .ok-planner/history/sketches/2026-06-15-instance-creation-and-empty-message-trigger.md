# Instance creation and the empty-message trigger

Sketch surfaced during the walk of the 2026-06-14 message-schema-layer plan's completion report. The plan's instance-creation behavior auto-synthesizes an `instance/root` envelope that opens the first frame and stale-marks every structural root. That behavior conflates two concerns the user model wants kept separate: **creating an instance** and **invoking work on the instance**.

## The shape the user wants

**Instance creation creates a row in `rimsky_instances`. That is the entire effect.** No frame opens. No envelope is written. No `instance/root` synthesis. The instance is left unpaused and waits for messages.

**Invocation always happens through a message landing.** An operator (or sensor, or any other sender) wanting to wake the instance posts a message. To wake "everything" (every structural root) without crafting a typed message, the operator sends an **empty message** — empty body, no type field (or type explicitly `""`).

Empty-message handling reuses the standard message-delivery path — same receipt, same idempotency, same frame opening, same cascade walker. No special-case code in the frame engine, no asymmetric synthesis. The mechanism is:

- At instance creation, the runtime injects one extra node into the instance: a virtual **root message node** with `type: ""`, no executor, no attributes. It exists only to be subscribed-to.
- Same step injects subscription edges from every structural root node to that virtual node (`subscribes: [{ node: "", type: terminal/success }]`).
- An incoming empty message arrives with `type: ""` (either by validator default or by receipt-time injection of the empty string when the field is missing). Its body is empty.
- The frame opens through the normal `EnqueueMessage` → `EnqueueFrame` path. `triggering_message_id` points at the empty-message envelope row.
- At frame promotion, the virtual `""` node settles with `terminal/success`. The standard cascade walker fires every subscriber — i.e. every root node — stale-marking them in the new frame. Dispatch follows.

The architectural win is that frame creation and cascade follow exactly the same rules for the empty trigger as for any typed message. The `instance/root` runtime-internal type-path, the `wake_node_ids` payload field, and the receipt-side carve-out for runtime-synthesis all disappear.

## Author override

A template can declare its own `type: ""` node. When present at instance-create time, the runtime detects it and **skips injection**. The author's node is the entry point. This makes the implicit default explicit only when an author wants control over its shape (e.g. an executor body that runs at empty-message receipt, or attribute substitution from an explicit empty-message body).

## Open implementation questions

1. **Where the injected node and edges live.** Today `rimsky_nodes` is the per-instance projection of template-declared node types; the subscription-edge inverse map is built from the template at registration. Adding instance-only edges needs a place to live. Three candidates worth weighing:
   - **Per-instance overlay** — a per-instance subscription-overlay table (or `instance_id` column on the existing edge representation). Cleanest separation; templates stay immutable.
   - **Template augmentation at registration** — augmented graph hashes differently from the author's source YAML; complicates author intuition. Probably wrong.
   - **Instance-level augmentation at instance-create** — keep the template untouched; expand the node list and rebuild a per-instance edge map cached alongside the instance row. Adds an instance-level cached structure.
2. **Empty string as a type.** The receipt-time registry validator rejects undeclared types. Two readings:
   - Allow `""` as a universal sentinel that bypasses the registry check (small special case at the receipt handler).
   - Have every template's `messages:` registry implicitly include the `""` entry (no special case at receipt; the rest of the model handles it via the standard path).
3. **`rimsky_instances.paused`** survives this redesign unchanged. It pauses the supervisor's dispatch for the instance. Since frames are serial, "pause the instance" is operationally equivalent to "pause the current and future frames." The debug-channel gate predicate continues to read it.

## What this means for the just-landed plan

The plan delivered: `messages:` registry, typed-message substitution grammar, the emit-message node-kind, the `triggering_message_id` column, the universal `/instances/{id}/messages` receipt, the debug-override endpoint. All of those stay — they're the foundation this proposal builds on.

What the plan got wrong and would need to back out for this shape:

- The `EnqueueSyntheticWakeFrame(... "instance/root" ...)` call at instance creation.
- The structural root-set computation at instance-create time (moves to instance-create-injection logic if the path is per-instance overlay, or to template-augmentation logic if the other path is chosen).
- The runtime-internal `instance/root` type-path entirely. The runtime never emits it; templates never see it.
- The receipt-side carve-out exempting runtime-internal types from the registry lookup (only needed because the runtime synthesizes envelopes). If `instance/root`, `node/invalidate`, `node/reset`, `asset/materialize` all retire (or move to per-message-type author declaration), the carve-out goes.

The other synthesis sites — `asset/materialize` and `node/reset` — are separate questions. They're operator side-effect requests, not message-shaped events the template anticipates; whether they should follow the same retire-the-synthesis pattern needs its own walk.

## Status

Sketch only. Surfaced during the report-walk; not yet a spec. The operator-invalidate route retirement is in flight as a separate fix in the current session. The instance/root refactor and the materialize/reset questions await a deliberate design pass.
