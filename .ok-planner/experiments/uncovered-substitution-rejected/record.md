---
experiment: uncovered-substitution-rejected
commit: PENDING
---

# Registration refuses a substitution ref no subscription covers

## What it runs against

`run.py` boots a `rimsky-all-in-one` container at `RIMSKY_IMAGE_TAG` and drives
it through the control API. One template reads an upstream node's attribute
without subscribing to it; a second reads a typed message's field without
subscribing to that message. Both are submitted to the register route, and the
first is also submitted to the validate route.

## What was observed

Registration of the attribute-ref template was refused with HTTP 400 and no
template id. The refusal carried a structured entry naming the ref
`{{nodes.producer.attribute.bar}}`, the receiver node, the schema property the
ref sits in, and the subscription entry that would cover it —
`{node: producer, type: attribute/bar/changed, force_upstream_refresh: false}`.
Adding exactly that entry to the same template made it register.

The typed-message template was refused the same way, naming
`{{messages.orders/created.id}}` and the entry
`{node: orders/created, type: terminal/success, force_upstream_refresh: false}`.

The validate route returned the same finding with `ok: false` before any
registration was attempted.

Eight checks, none failing.
