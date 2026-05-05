---
concept: lifecycle-subscriber
definition: |
  An opt-in protocol for peers that want to react to template and instance state transitions. Six methods: `OnTemplateRegistered`, `OnTemplateDeployed`, `OnTemplateUndeployed`, `OnTemplateDeregistered`, `OnInstanceCreated`, `OnInstanceTerminated`. Fires synchronously from the control-api process at each transition.
proto_symbol: LifecycleSubscriber in protocols/proto/v1/lifecycle.proto
config_field: (none)
api_surface: (none)
related: [template, instance, claim-producer]
deprecated_terms: []
---

# Lifecycle subscriber

## Definition

An opt-in protocol for peers that want to react to template and instance state transitions. Six methods: `OnTemplateRegistered`, `OnTemplateDeployed`, `OnTemplateUndeployed`, `OnTemplateDeregistered`, `OnInstanceCreated`, `OnInstanceTerminated`. Fires synchronously from the control-api process at each transition.

## Why it exists

Some peers need to know when templates and instances change state — a postgres claim producer might want to provision a queue table when a template is deployed; an external observability system might want to record instance creations. Without a protocol, these peers would poll the control-api or scrape state from elsewhere.

The lifecycle protocol formalizes the hooks. The control-api fires events synchronously at each state transition; idempotency is tracked in the persistence layer so replays are no-ops. Peers that want the events declare `lifecycle_subscriber` in their `protocols: [...]` config; peers that don't subscribe silently skip fan-out.

The protocol is opt-in because most peers don't need it. A pure executor or a pure claim producer that wraps stateless storage has no use for state-transition hooks. Forcing every peer to implement six methods would be friction without benefit.

## The six methods

- **`OnTemplateRegistered`** — a new template hash entered the registry.
- **`OnTemplateDeployed`** — a registered template moved to the `deployed` state and is now eligible for instance creation.
- **`OnTemplateUndeployed`** — a deployed template moved to `undeployed`; new instances cannot be created against it.
- **`OnTemplateDeregistered`** — the template was removed from the registry.
- **`OnInstanceCreated`** — a new instance was created against a deployed template.
- **`OnInstanceTerminated`** — an instance moved to its terminal state (success or failure) or was deleted by an operator.

All subscribed peers implement all six methods; peers that don't react to a particular event return `nil` from that method.

Bundled producer binaries can ship a no-op `LifecycleSubscriber` via `enable_lifecycle: true` config without forking the binary.

## How you encounter it

- **Operator config**: peers that want lifecycle events list `lifecycle_subscriber` in their `protocols: [...]` config under `rimsky.yml`. Without that, the peer is silently skipped during fan-out.
- **Implementing a subscriber**: speak gRPC against `protocols/proto/v1/lifecycle.proto`. The protocol is implementable separately from `ClaimProducer` and `Executor` — a peer can opt into one, two, or three protocols by listing them.

## Consumer-visible guarantees

- Lifecycle events fire from the control-api process at the moment of state transition. Synchronous fan-out means a slow subscriber slows down the transition; subscribers should be fast.
- Idempotency is tracked per (peer, event-type, object-id). Replays — caused by retries, restarts, or operator-driven backfill — are no-ops at the rimsky side. Subscribers should still write idempotent handlers because their own internal effects may not be idempotent by default.
- Peers referenced by a template but not subscribed silently skip fan-out. There's no error — non-subscription is the default.

## Common mistakes

- Implementing lifecycle handlers that block on slow external work. Lifecycle fan-out is synchronous; a slow handler delays the control-api's response on the triggering operation. Push slow work into a queue inside the subscriber and acknowledge fast.
- Expecting lifecycle events to fire from the supervisor. They fire from the control-api process — the same process that handles `POST /templates/.../deploy` and `POST /instances`.

## See also

- [`template.md`](template.md)
- [`instance.md`](instance.md)
- [`claim-producer.md`](claim-producer.md)
