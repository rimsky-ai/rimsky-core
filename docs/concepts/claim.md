---
concept: claim
definition: |
  A node-declared assertion that the node will read or read-write a producer-defined slice of state for the duration of its run. Claims are acquired before the node's executor runs and resolved at terminal. Each claim binds an alias, an intent (`r` or `rw`), a producer name, and a selector.
proto_symbol: OpenRequest in protocols/proto/v1/claim_producer.proto
config_field: (none)
api_surface: (none)
related: [claim-producer, claim-handle, scope, write-semantics, holding-subgraph, inheritance]
deprecated_terms: []
---

# Claim

## Definition

A node-declared assertion that the node will read or read-write a producer-defined slice of state for the duration of its run. Claims are acquired before the node's executor runs and resolved at terminal. Each claim binds an alias, an intent (`r` or `rw`), a producer name, and a selector.

## Why it exists

Workflow nodes that touch shared state need coordination — two nodes that both want to write to the same data must not run concurrently. Rimsky models this with claims. Each claim names what slice of producer state the node is operating on (the scope) and how (the intent). The conflict matrix tells the supervisor which combinations of claims can coexist.

The producer-vs-rimsky split is deliberate. Rimsky knows nothing about the structure of the underlying state — paths, tables, queues, S3 manifests — only that "this claim's scope is byte-equal to that one" or "this claim and that one have intents that conflict." The producer parses the selector and decides what physical state to expose; Rimsky compares scope bytes for byte-equality and gates concurrent acquisition through the conflict matrix.

## Anatomy of a claim

A claim has four named-field components. Each is substitutable from `{{claim.<alias>.*}}` directives in attribute schemas of the same node and any inheriting nodes.

- **`alias`** — per-node name. Defaults to the producer name; settable when a node has multiple claims on the same producer.
- **`intent`** — `r` (read) or `rw` (read-write). The graph author's declaration of usage.
- **`address`** — producer-supplied pointer the executor uses to access state (path, table reference, snapshot handle, etc.). Returned by `Open`.
- **`payload`** — producer-supplied data captured at acquisition (e.g. a picked queue item's user data).
- **`scope`** — the resolved selector or pick-policy-picked identifier; opaque bytes from rimsky's perspective.

A claim becomes a [claim handle](claim-handle.md) once acquired.

## How you encounter it

- **Wire**: every claim acquisition produces an `OpenRequest` to the producer and an `OpenResponse` carrying address, payload, scope, and `realized_write_semantics`.
- **Templates**: the `stores:` block under each node declaration declares its claims. Each entry has `name` (the claim producer / store), `selector`, `intent`, and an optional `alias`.
- **Substitution**: `{{claim.<alias>.address}}`, `{{claim.<alias>.payload.<field>}}`, `{{claim.<alias>.scope}}` in the same node's `attributes:` paths, and in inheriting nodes.

## Consumer-visible guarantees

- Claim content (address, payload, scope) is opaque to Rimsky. Rimsky reads claim content by named-field path only at substitution-leaf extraction; it does not log, validate, transform, or otherwise act on the bytes. Operators who need to keep field values out of Rimsky's address space can encrypt before passing.
- Claim acquisition is atomic with the run's dispatch: either the supervisor takes ownership of the run and all required claim handles are inserted, or none of these. There's no in-between state where rimsky thinks it acquired a claim that the producer doesn't actually have.

## Common mistakes

- **Rimsky's claim ≠ JWT claim, insurance claim.** A Rimsky claim is a scoped acquisition of producer-managed state; nothing to do with JSON Web Token claims (key/value assertions in tokens) or insurance terminology.
- Conflating claims with named locks. Claims are `(producer, selector)`-shaped and gated by the per-producer conflict matrix; named locks are scalar capacity counters and gated purely by count.
- Treating claim payload as structured data Rimsky understands. Rimsky never parses payload; the path-walk for substitution is the only operation. Schema validation of payload bytes happens against the node's attributes schema, not by the foundation.

## See also

- [`claim-producer.md`](claim-producer.md)
- [`claim-handle.md`](claim-handle.md)
- [`scope.md`](scope.md)
- [`write-semantics.md`](write-semantics.md)
- [`holding-subgraph.md`](holding-subgraph.md)
- [`inheritance.md`](inheritance.md)
