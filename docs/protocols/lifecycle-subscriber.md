# Implementing a lifecycle subscriber

This guide is for developers implementing a lifecycle subscriber — a service that wants to react to template and instance state transitions in Rimsky. The wire contract lives at `protocols/proto/v1/lifecycle.proto`; this guide is the practical companion.

<!-- @source: ../../.ok-planner/design/concepts/lifecycle-subscriber.md -->
> An opt-in protocol for services that want to react to template and instance state transitions. Six methods: `OnTemplateRegistered`, `OnTemplateDeployed`, `OnTemplateUndeployed`, `OnTemplateDeregistered`, `OnInstanceCreated`, `OnInstanceTerminated`. Fires synchronously from the control-api process at each transition.

> **Auth-blind advisory.** Rimsky has no machinery for credentials, encryption, or access control. Service-to-service auth is operator-configured at the deployment layer.

---

## 1. The wire contract

```protobuf
service LifecycleSubscriber {
  rpc OnTemplateRegistered(OnTemplateRegisteredRequest)     returns (LifecycleAck);
  rpc OnTemplateDeployed(OnTemplateDeployedRequest)         returns (LifecycleAck);
  rpc OnTemplateUndeployed(OnTemplateUndeployedRequest)     returns (LifecycleAck);
  rpc OnTemplateDeregistered(OnTemplateDeregisteredRequest) returns (LifecycleAck);
  rpc OnInstanceCreated(OnInstanceCreatedRequest)           returns (LifecycleAck);
  rpc OnInstanceTerminated(OnInstanceTerminatedRequest)     returns (LifecycleAck);
}
```

Source: `protocols/proto/v1/lifecycle.proto`.

All six methods return `LifecycleAck` — there's no return data, just an acknowledgement that the subscriber processed the event.

## 2. Opting in

Lifecycle is the third Rimsky protocol; opting in is a per-service configuration flag. Add `lifecycle_subscriber` to the service's `protocols: [...]` list in `rimsky.yml`:

```yaml
claim_producers:
  my-store:
    endpoint: "grpc://my-store:9100"
    protocols: [claim_producer, lifecycle_subscriber]
    write_semantics_allowed: [sync]
```

Without that entry, the service is silently skipped during fan-out — there's no error, non-subscription is the default.

The flag is per-service, not per-protocol. A service that implements both `ClaimProducer` and `LifecycleSubscriber` lists both protocols; the gRPC server registers handlers for both.

For bundled producer binaries that ship a no-op `LifecycleSubscriber`, a separate config flag `enable_lifecycle: true` lets operators turn the lifecycle handlers on without forking the binary.

## 3. The six events

### `OnTemplateRegistered`

Fired when a template hash is added to the registry. Common subscriber response: provision idempotent infrastructure tied to the template (e.g. allocate an empty queue, prepare a sub-bucket).

### `OnTemplateDeployed`

Fired when a registered template moves to `deployed`. Templates must be deployed before instances can be created against them. Common subscriber response: warm caches, mark resources ready for instance traffic.

### `OnTemplateUndeployed`

Fired when a deployed template moves to `undeployed`. Existing instances continue; new instances cannot be created against this template. Common subscriber response: drain caches, mark resources for graceful winding-down.

### `OnTemplateDeregistered`

Fired when a template is removed from the registry. Common subscriber response: delete provisioned infrastructure tied to the template.

### `OnInstanceCreated`

Fired when an operator (or compose up) creates a new instance against a deployed template.

### `OnInstanceTerminated`

Fired when an instance moves to its terminal state — completed all frames or was deleted by an operator.

## 4. Idempotency

<!-- @source: ../../.ok-planner/design/concepts/lifecycle-subscriber.md -->
> An opt-in protocol for services that want to react to template and instance state transitions. Six methods: `OnTemplateRegistered`, `OnTemplateDeployed`, `OnTemplateUndeployed`, `OnTemplateDeregistered`, `OnInstanceCreated`, `OnInstanceTerminated`. Fires synchronously from the control-api process at each transition.

Rimsky tracks idempotency at its own boundary: each event is keyed by `(service-name, event-type, object-id)`. Replays — caused by retries, restarts, or operator-driven backfill — are no-ops at the rimsky side.

That's the rimsky-side guarantee; the subscriber must still handle replays correctly because its own internal effects (e.g. allocating a queue, sending a notification) may not be idempotent by default. The recommended pattern is to treat each handler as if it could be invoked multiple times for the same `(event-type, object-id)` and short-circuit early.

## 5. Synchronous fan-out

Lifecycle events are fired synchronously from the control-api process. A slow subscriber slows down the control-api response on the triggering operation (e.g. a slow `OnTemplateDeployed` makes `POST /templates/{id}/deploy` slow).

Implications:

- **Be fast.** Subscribers should acknowledge within hundreds of milliseconds. Push slow work into the subscriber's own internal queue.
- **Don't depend on inter-event ordering.** The control-api fans out to subscribed services in a fixed but unspecified order; an `OnTemplateDeployed` notification from service A may arrive before or after service B's notification.
- **Failures don't block other subscribers.** A subscriber returning an error is logged but does not block fan-out to remaining subscribers.

## 6. Reference impl

There's no standalone reference lifecycle-subscriber binary in the repo — lifecycle handlers ship inside the bundled producer binaries, gated by `enable_lifecycle: true` in their config.

The minimal opt-in shape is a service entry that lists both protocols and (for bundled producers) sets `enable_lifecycle: true`:

```yaml
claim_producers:
  my-store:
    endpoint: "grpc://my-store:9100"
    protocols: [claim_producer, lifecycle_subscriber]
    enable_lifecycle: true
    write_semantics_allowed: [sync]
```

The service's gRPC server registers both `ClaimProducer` and `LifecycleSubscriber` handlers; control-api fans out the six lifecycle methods at the matching state transitions.

## See also

- [`../../.ok-planner/design/concepts/lifecycle-subscriber.md`](../../.ok-planner/design/concepts/lifecycle-subscriber.md)
- [`../../.ok-planner/design/concepts/template.md`](../../.ok-planner/design/concepts/template.md)
- [`../../.ok-planner/design/concepts/instance.md`](../../.ok-planner/design/concepts/instance.md)
- [`../../.ok-planner/design/concepts/claim-producer.md`](../../.ok-planner/design/concepts/claim-producer.md)
