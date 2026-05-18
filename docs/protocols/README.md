# Protocol-implementation guides

These guides cover the gap between *understanding the concepts* and *implementing a custom service against the wire protocol in your language of choice*.

- [ClaimProducer](claim-producer.md) — implement the producer protocol: `Capabilities`, `Open`, `Commit`, `Abandon`, `Release`.
- [Executor](executor.md) — implement the dispatch protocol `Executor` (`Execute`) and the optional read-only observability protocol `ExecutorObservability` (`Capabilities`, `GetTrace`, `StreamTrace`).
- [LifecycleSubscriber](lifecycle-subscriber.md) — implement the lifecycle protocol: six template/instance state-transition hooks.
- [Publisher](publisher.md) — implement the publisher protocol: `Capabilities`, `Subscribe`, `Unsubscribe`, `ListSubscriptions`. Bundled sensors (cron / http / object-store / webhook) all implement Publisher.

The proto definitions are at [`protocols/proto/v1/`](../../protocols/proto/v1/). Generate language bindings with `protoc` (the rimsky build uses `make proto-gen`).
