---
concept: node
definition: |
  The unit of work in a Rimsky template. A node is a named vertex in a template's graph, defined by its subscriptions, attributes schema, and the executor that runs it. At runtime, every node belongs to a specific instance and has one of five states.
proto_symbol: (none)
config_field: (none)
api_surface: GET /nodes/{id}
related: [node-state, template, instance, frame, cascade, subscription]
deprecated_terms: []
---

# Node

## Definition

The unit of work in a Rimsky template. A node is a named vertex in a template's graph, defined by its subscriptions, attributes schema, and the executor that runs it. At runtime, every node belongs to a specific instance and has one of five states.

## Why it exists

Rimsky orchestrates work as a directed graph. Each vertex of the graph is a node — an addressable, named computation that subscribes to zero or more upstream nodes and produces attributes that downstream nodes consume. The graph shape is declared in a template (the static artifact); the runtime state of a node lives in the instance that the template was launched from.

Nodes are the unit of cascade resolution: when an upstream value changes, every subscriber receives an `invalidate` and (if eligible) the scheduler recalculates them. Nodes are also the unit of dispatch: each node declares an executor, and when the node enters the `running` state the supervisor invokes that executor over the wire.

A node's identity is the pair `(instance_id, node_name)`. Within a template, node names are unique. Across instances of the same template, node names are reused (each instance is its own population).

## Anatomy of a node declaration

In a template's `nodes:` list, each entry has:

- `type:` — the node's name within the template (unique).
- `subscribes:` — zero or more subscription entries declaring "fire me when this upstream topic transitions." Substitution refs in the attribute schema auto-subscribe; explicit entries are added for non-reading coupling. See [`subscription.md`](subscription.md).
- `attributes:` — JSON Schema defining typed inputs and outputs (with `{{...}}` substitution directives in `source:` fields).
- `executor:` — reference to a configured executor.
- `stores:` — zero or more claim declarations (each binding an `alias`, `name` (the claim producer), `selector`, `intent`).
- `locks:` — zero or more named-lock acquisitions.
- `inherits:` — for downstream nodes that participate in a held claim's holding subgraph.
- `userdata:` — opaque bytes passed to the executor verbatim.

## How you encounter it

- **Wire**: every executor invocation carries a node identity in `ExecuteRequest`.
- **Control API**: `GET /nodes/{id}`, `POST /nodes/{id}/invalidate`, `POST /nodes/{id}/reset`, `GET /instances/{idOrKey}/nodes`.
- **Template DSL**: the `nodes:` list of a template spec.

### Lifecycle handlers

Each node may declare three lifecycle handler slots that customize the supervisor's behavior at terminal events. All three are optional; absent slots use today's hardcoded defaults.

- `on_acquire_unavailable: { resolve: pass | retry | error, error_class? }` — runs when any required claim's `Open` returned `Available=false`. Default (or explicit `retry`) is silent retry; `pass` transitions the node to `fresh+passed` without invoking the executor; `error` routes through the named `error_types[error_class]` policy.
- `on_executor_complete: { resolve: by_changed | always_propagate | never_propagate }` — runs at a `Complete` terminal (the StreamClose `Success` outcome on the wire). Default `by_changed` mirrors today's behavior; `always_propagate` forces the cascade gate to fire even on `changed:false`; `never_propagate` suppresses the cascade gate even on `changed:true`.
- `on_executor_errored: { resolve: error | pass, error_class? }` — runs at an `Error{error_class}` terminal (the StreamClose `Error` outcome on the wire, including the reserved `executor_blocked` class that collapsed into this slot post-2026-05-12). Default routes through `error_types[<executor-class>]`; `pass` transitions to `fresh+passed`; `error` overrides the routed class.

Cascade coupling is declared receiver-side via the `subscribes:` block (see [`subscription.md`](subscription.md)). The pre-2026-05-14 `on_event:` map and `invalidate.targets:` clauses on lifecycle handlers retired; their effects are now expressed as receiver-side subscriptions (`{node: <sender>, on: state | event, ...}`).

See [`handlers.md`](handlers.md) for the full handler vocabulary.

## Consumer-visible guarantees

- A node's value (its captured attributes) is set by exactly one source: the executor's writeback at commit time, validated against the node's `attributes:` schema. Validation runs twice — once at dispatch (post-substitution, on inputs) and once at commit (on the executor's writeback).
- Operator-originated invalidates do not preempt running work: an in-flight `running` node always runs to its terminal state before the invalidate takes effect.

## Common mistakes

- **Rimsky's node ≠ Node.js, Kubernetes node, or network node.** Rimsky nodes are vertices in a directed work graph, not JavaScript runtimes, container hosts, or network endpoints.
- Treating a node's name as a globally-unique identifier. The name is unique within a template; identity at runtime requires the `(instance_id, node_name)` pair.
- Expecting a node to "rerun" automatically when its source code changes. Rimsky's cascade is value-driven (changes to `attributes` propagate); template-spec changes require redeploying a template (which produces a new content hash).

## See also

- [`node-state.md`](node-state.md)
- [`template.md`](template.md)
- [`instance.md`](instance.md)
- [`cascade.md`](cascade.md)
- [`frame.md`](frame.md)
- [`subscription.md`](subscription.md)
