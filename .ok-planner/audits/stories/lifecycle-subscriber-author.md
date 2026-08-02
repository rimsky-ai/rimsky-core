---
audit: lifecycle-subscriber-author
artifact: story:lifecycle-subscriber-author
determination: supported
commit: 3918d24e
audited: 2026-08-02T09:28:57Z
---

# Service author implements the seven-callback LifecycleSubscriber protocol and registers it

Supported. `lifecycle.proto` declares the `LifecycleSubscriber` gRPC service with exactly seven RPCs — `OnTemplateRegistered`, `OnTemplateDeployed`, `OnTemplateUndeployed`, `OnTemplateDeregistered`, `OnInstanceCreated`, `OnInstanceTerminated`, `OnRunScopeTerminal` — mirrored by the Go `lifecycle.LifecycleSubscriber` interface with matching request types carrying template hash, instance ID, run-scope ID, service bindings, owner key ID, and terminal reason. A service registers as an active subscriber by declaring the `lifecycle_subscriber` protocol alongside a claim-producer/executor entry in `rimsky.yml`/compose config; `lib/control/controlapi/lifecycle.go`'s `dispatchTemplateEvent`/`dispatchInstanceEvent`/`OnRunScopeTerminal` call each callback synchronously and in-request at the corresponding transition, tracked via the `rimsky_lifecycle_idempotencies` table. Coverage spans 15 unit tests in `lib/control/controlapi/lifecycle_test.go`, an executor-conformance check (`lifecycle_check.go`) exercising all seven RPCs, and a full-sequence scenario test (`test/scenarios/lifecycle/lifecycle_e2e_test.go`) driving deploy → instance-create → terminate → undeploy through a real registered subscriber peer.
