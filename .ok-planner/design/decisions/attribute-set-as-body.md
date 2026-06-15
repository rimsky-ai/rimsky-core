---
decision: attribute-set-as-body
status: as-is
---

# Attribute set as body

## Choice

A message-emitter node's `attributes:` block is the message body. The attribute schema must match the destination message type's body schema exactly — same field names, same types. No mapping layer between attributes and body fields.

## Rationale

One source of truth for attribute field names. A mapping layer would mean two separately-named things kept in sync by a third layer; that is the redundancy the design avoids. The "message body is an attribute block that carries across frames" framing is literal: the emit-node's attribute resolution at dispatch is the body construction.

## Alternatives considered

A mapping sub-block under the emit-node's dispatch field, mapping attribute names to body field names (or substituting from arbitrary sources into body fields). Rejected as redundant; users who want to decouple shapes can author a separate triggering node feeding a separate emitter node.
