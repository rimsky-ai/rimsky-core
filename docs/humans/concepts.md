# Rimsky concepts — narrative walk

This page walks Rimsky's vocabulary in learning order. Each section names the concept once, links to its concept file, and walks the concept narratively.

## 1. Nodes and node states

<!-- @source: concepts/node.md -->
> The unit of work in a Rimsky template. A node is a named vertex in a template's graph, defined by its dependencies, attributes schema, and the executor that runs it. At runtime, every node belongs to a specific instance and has one of five states.

The five states are exhaustive — every node, at every moment, is in one of them.

<!-- @source: concepts/node-state.md -->
> The five named runtime states a node can occupy: `fresh`, `stale`, `running`, `failed`, `parked`. The state-machine vocabulary covers every legal combination of "do we have a value?" and "is work pending?" plus the `failed` distinction (work attempted, no value, no auto-recovery scheduled) and the `parked` distinction (non-terminal hold awaiting time-based wake or external invalidate).

Transitions are explicit. A `running` node moves to `fresh` (success), `failed` (failure with no auto-recovery scheduled), or `parked` (non-terminal hold); the system does not silently coerce same-state transitions.

See [`concepts/node.md`](../concepts/node.md) and [`concepts/node-state.md`](../concepts/node-state.md).

## 2. Templates and instances

<!-- @source: concepts/template.md -->
> A content-addressed bundle of node definitions, attribute schemas, claim and lock declarations, and frame-resolution config. The id is `sha256-<64-hex>` over an RFC 8785 JCS-canonicalized spec. Re-registering the same spec is a cheap no-op. Templates persist through four lifecycle states: registered, deployed, undeployed, deregistered.

Templates are the static artifact; instances are the live executions.

<!-- @source: concepts/instance.md -->
> A running execution of a template, identified by a Rimsky-generated UUID. Instances bind to a specific template content hash at creation. An optional `instance_key` is a caller-supplied dedup key. Tag movement does not migrate live instances.

The content-addressing has practical consequences: re-registering the same spec is idempotent; tag movement is explicit and atomic; running instances are pinned to their original hash. See [`concepts/template.md`](../concepts/template.md), [`concepts/instance.md`](../concepts/instance.md), [`concepts/tag.md`](../concepts/tag.md).

## 3. Frames

<!-- @source: concepts/frame.md -->
> The unit of cascade resolution. A frame begins when a node receives an invalidate and ends when no node remains in `stale` or `running` for the instance. The template's `frame_resolution:` field decides how concurrent invalidates are handled — `serial_queue` (each invalidate produces its own frame; frames run one at a time) or `coalesce` (new invalidates merge into a single pending row).

A frame begins when a node receives an invalidate (or is force-fired); it ends when no node remains in `stale` or `running` for the instance. Frame-end is computed at every scheduler tick.

See [`concepts/frame.md`](../concepts/frame.md).

## 4. Cascades and invalidation

Rimsky's reactive model rests on one cascade message and one scheduler action.

<!-- @source: concepts/cascade.md -->
> The propagation of `invalidate` through the node graph. When a node loses or replaces its value, downstream dependents are marked `stale` so the scheduler can recalculate them from the new value. Cascade is the reactive-computation engine at the heart of Rimsky.

Cascade is a pure reachability walk; it does no I/O and no executor dispatch on its own. The scheduler picks up newly-stale nodes on subsequent ticks and recalculates them by dispatching their executors.

<!-- @source: concepts/invalidate.md -->
> Rimsky's only graph-level message. Sent to a node, it marks the node `stale` and cascades the same message to dependents. The cascade engine is a pure reachability walk over the dependency graph rooted at the invalidated node.

There is no second message. "Recalculate" is a verb describing what the scheduler does to a stale node — not a service message that travels alongside `invalidate`.

See [`concepts/cascade.md`](../concepts/cascade.md), [`concepts/invalidate.md`](../concepts/invalidate.md).

## 5. Claims, claim handles, scopes, named locks

Rimsky's coordination primitives split into two: claims (producer-mediated, scoped) and named locks (deployment-level scalars).

<!-- @source: concepts/claim.md -->
> A node-declared assertion that the node will read or read-write a producer-defined slice of state for the duration of its run. Claims are acquired before the node's executor runs and resolved at terminal. Each claim binds an alias, an intent (`r` or `rw`), a producer name, and a selector.

Each claim materializes into a claim handle:

<!-- @source: concepts/claim-handle.md -->
> The persistent row asserting "holder H has acquired scope S for purpose P." Implementation of an acquired claim. Carries the rimsky-generated `claim_id`, holder identity, scope bytes, producer-returned address and payload, the realized write semantics, and a held flag.

The scope bytes are how Rimsky detects conflict:

<!-- @source: concepts/scope.md -->
> The slice of a producer's namespace under claim. Both the conceptual `(producer, selector)` pair and the concrete opaque bytes that identify it on the claim-handle row. Conflict checking compares scope bytes byte-for-byte; producers canonicalize scope bytes such that two claims that should conflict produce byte-equal scopes.

When the coordination need is scalar (no scope), use a named lock:

<!-- @source: concepts/named-lock.md -->
> A scalar capacity counter at the deployment level. Configured in `rimsky.yml` under `named_locks:`, each entry binds a name to a numeric capacity. Nodes declare lock acquisitions via the `locks:` block; dispatch is gated when the current holder count equals capacity.

See [`concepts/claim.md`](../concepts/claim.md), [`concepts/claim-handle.md`](../concepts/claim-handle.md), [`concepts/scope.md`](../concepts/scope.md), [`concepts/named-lock.md`](../concepts/named-lock.md).

## 6. Write semantics

<!-- @source: concepts/write-semantics.md -->
> The per-claim verdict from `ClaimProducer.Open` describing how writes against the claimed scope are visible to other claims. One of `sync`, `staged_async`, `blocking_async`, `read_only`. The conflict matrix is parameterized over write-semantics so different producers can offer different concurrency models.

Different producers offer different concurrency models. Filesystems serialize; postgres can MVCC. Rimsky parameterizes the conflict matrix over a per-claim write-semantics value so each producer can declare its envelope and the conflict check works the same way for all of them.

See [`concepts/write-semantics.md`](../concepts/write-semantics.md).

## 7. Holding subgraphs and inheritance

When a claim's value extends past the acquiring node's run — a snapshot multiple downstream nodes need to read, a queue item that must be operated on by several nodes — declare the claim's lifetime extension explicitly via inheritance.

<!-- @source: concepts/holding-subgraph.md -->
> The set of nodes a held claim's lifetime spans: an acquirer and the directly-declared inheritors. Computed at template deploy from explicit `inherits:` declarations. When all members reach a terminal state, Rimsky fires the producer verb (commit on all-success, abandon on any-failure) and the claim ends.

<!-- @source: concepts/inheritance.md -->
> The DSL mechanism by which a downstream node declares it will use a live claim from an upstream acquirer. Direct only — does not propagate transitively through dep chains. Inheriting nodes can substitute the inherited claim's address, payload, and scope into their own attributes.

See [`concepts/holding-subgraph.md`](../concepts/holding-subgraph.md), [`concepts/inheritance.md`](../concepts/inheritance.md).

## 8. Service protocols

External services integrate with Rimsky via three wire protocols.

<!-- @source: concepts/claim-producer.md -->
> The protocol-level term for a service that produces claim handles for Rimsky's lock-and-claim primitives. Implements five methods (`Open`, `Commit`, `Abandon`, `Release`, `Capabilities`). Out-of-process; rimsky talks to claim producers over gRPC.

<!-- @source: concepts/executor.md -->
> The protocol-level term for the service that runs a node's work. Implements the dispatch protocol `Executor` (one method, `Execute`) and optionally the paired read-only `ExecutorObservability` protocol (`Capabilities`, `GetTrace`, `StreamTrace`). Out-of-process; supervisors dispatch to executors over gRPC, with an HTTP+JSON bridge available for non-Go services.

<!-- @source: concepts/lifecycle-subscriber.md -->
> An opt-in protocol for services that want to react to template and instance state transitions. Six methods: `OnTemplateRegistered`, `OnTemplateDeployed`, `OnTemplateUndeployed`, `OnTemplateDeregistered`, `OnInstanceCreated`, `OnInstanceTerminated`. Fires synchronously from the control-api process at each transition.

For implementation guides, see [`protocols/`](../protocols/).

## 9. Attributes and userdata

A node has two distinct ways of carrying input: the typed, substituted, schema-validated `attributes:` block; and the opaque, never-substituted `userdata:` block.

<!-- @source: concepts/attributes.md -->
> The typed inputs and outputs of a node, declared by a JSON Schema in the template's `attributes:` block. Attributes are the substitution boundary: `{{deps.<source>.<field>}}`, `{{claim.<alias>.<path>}}`, `{{params.<key>}}` directives in the schema's `source:` fields are resolved at dispatch.

<!-- @source: concepts/userdata.md -->
> Free-form opaque bytes a template author attaches to a node's executor invocation. Rimsky never inspects, parses, substitutes, or validates `userdata`. The executor receives the bytes verbatim. This is distinct from `attributes`, which are typed, substituted, and schema-validated.

See [`concepts/attributes.md`](../concepts/attributes.md), [`concepts/userdata.md`](../concepts/userdata.md).
