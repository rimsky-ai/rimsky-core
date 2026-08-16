---
assessment: lifecycle-subscriber-author--lifecycle-callbacks
subject: story:lifecycle-subscriber-author
way: lifecycle-callbacks
release: d977250c
outcome: held
warrant: experiment:lifecycle-subscriber-author
---
# Being told, from my own service, when rimsky's templates, instances and run-scopes change state

A third-party subscriber built for the run — its own Go module depending only on `catalog:published-packages/github.com/rimsky-ai/rimsky-core/lib/protocols (Go module)` — was registered as an ordinary peer of a `catalog:images/rimsky-all-in-one` stack and driven through one instance's whole life. All seven callbacks fired and none is unaccounted for: `catalog:grpc-rpcs/LifecycleSubscriber.OnTemplateRegistered`, `catalog:grpc-rpcs/LifecycleSubscriber.OnTemplateDeployed`, `catalog:grpc-rpcs/LifecycleSubscriber.OnInstanceCreated`, `catalog:grpc-rpcs/LifecycleSubscriber.OnInstanceTerminated`, `catalog:grpc-rpcs/LifecycleSubscriber.OnTemplateUndeployed`, `catalog:grpc-rpcs/LifecycleSubscriber.OnTemplateDeregistered` and `catalog:grpc-rpcs/LifecycleSubscriber.OnRunScopeTerminal`. Nothing fired before anything happened, and six of the seven were already delivered by the time the control-API call that caused them returned, checked without waiting, which is what synchronous means from the caller's side. Every context element the promise names came through: the template hash on all four template callbacks and the spec on registration, the deployment's tags on deploy, and on instance creation the instance id, template hash, instance key, params, caller-supplied service bindings, owner key and routing identity; the run-scope callback carried its scope id, instance and terminal reason, and the termination callback its instance, template and termination time. The seven fired in the order the transitions happened. Thirty-seven checks, none failing.

## Unverified remainder

The seventh callback, run-scope terminal, has no caller call to be synchronous with; it arrived from the runtime when the frame settled, so the synchronous-on-return property was established for the other six only.
