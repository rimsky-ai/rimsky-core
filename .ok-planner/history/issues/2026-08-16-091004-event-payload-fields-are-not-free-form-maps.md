---
issue: event-payload-fields-are-not-free-form-maps
kind: audit
category: conflicting
artifacts:
  - decision:event-log-payload-shapes
status: promoted
opened: 2026-08-16T09:10:04Z
sprint: 2026-08-17-accepted-intake-drain.md
---

# The event-payload decision still says internal payloads are free-form maps

Event-log rows carry a structured payload. The decision that split the wire shape from the internal shape says the internal write and read fields are free-form JSON maps. They are not, and have not been since the project's own rules changed: the internal fields are an opaque payload type whose only constructor takes a generated protobuf message, so a map literal does not compile at any emit site — that is the enforcement the project relies on. The proto split and its two enforcing tests hold; only the closing sentence of the decision's Choice is stale. The ruling decides the replacement text.

The consequence of leaving it is concrete: a reader trusting the decision believes a map literal is legal at an event-payload emit site and writes one; the compiler refuses it, and the reader has no idea why the corpus said otherwise.

## Options

- Rewrite the closing sentence to say the internal path carries payloads as an opaque value constructed from a declared message, independent of the proto event wrapper, with the descriptor test named as the enforcement; cost: none beyond the edit.

The ruling decides the wording; there is one compliant end state.

## Ruling

> Generated ruling (/verify-issues): Replace the Choice's "free-form JSON for both event classes alike" with the current shape — the internal write and read path carries payloads as an opaque value constructed only from a declared generated message, so a map literal cannot compile at an emit site, and the descriptor test enforces the split — without source paths or symbol names in the artifact. The current-state-only rule forces it, and the project's binding rule on proto-declared payloads already states the same convention. Verified against the tree as it stands; nothing was applied.

<!-- Owner: this is a generated ruling, not your decision. Leave it as-is to accept — the next /plan-sprint carries it, naming the generated/recommended batches at sign-off. Edit the text to redirect, empty the section to discuss live, or delete this note to adopt the ruling as your own. -->
