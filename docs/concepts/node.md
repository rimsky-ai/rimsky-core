---
concept: node
definition: |
  The unit of work in a Rimsky template. A node is a named vertex in a template's graph, defined by its dependencies, attributes schema, and the executor that runs it. At runtime, every node belongs to a specific instance and has one of four states.
proto_symbol: (none)
config_field: (none)
api_surface: GET /nodes/{id}
related: [node-state, template, instance, frame, cascade]
deprecated_terms: []
---

# Node

## Definition

The unit of work in a Rimsky template. A node is a named vertex in a template's graph, defined by its dependencies, attributes schema, and the executor that runs it. At runtime, every node belongs to a specific instance and has one of four states.

## Why it exists

Rimsky orchestrates work as a directed graph. Each vertex of the graph is a node — an addressable, named computation that depends on zero or more upstream nodes and produces attributes that downstream nodes consume. The graph shape is declared in a template (the static artifact); the runtime state of a node lives in the instance that the template was launched from.

Nodes are the unit of cascade resolution: when an upstream value changes, downstream nodes receive an `invalidate` and (if eligible) the scheduler recalculates them. Nodes are also the unit of dispatch: each node declares an executor, and when the node enters the `running` state the supervisor invokes that executor over the wire.

A node's identity is the pair `(instance_id, node_name)`. Within a template, node names are unique. Across instances of the same template, node names are reused (each instance is its own population).

## Anatomy of a node declaration

In a template's `nodes:` list, each entry has:

- `type:` — the node's name within the template (unique).
- `dependencies:` — zero or more sibling node names whose `fresh` state gates this node's eligibility.
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
