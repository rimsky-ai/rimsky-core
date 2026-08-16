# Surface intent

Which classes of element are public, and which specific elements depart from those rules. The audit's surface extractor reads this document and classifies what it finds; the owner is its authority.

## The general rule

Rimsky is a platform whose product is its interfaces. A consumer embeds it as a Go module, runs its images, implements its protocols, writes its templates, and operates its deployments. Every element that reaches such a consumer is public. An element is internal only when a rule below says so.

## CLI verbs

Every verb and subcommand the `rimsky` binary dispatches is public, `rimsky conformance <protocol>` included.

## Images

Every image the `push-images` target publishes to the registry is public. Images built only for the test harnesses (`make test-images`) are internal.

## gRPC RPCs

Every RPC declared under `lib/protocols/proto/v1` is public. Third parties implement these protocols against rimsky, so each RPC is a contract rimsky owes them.

## HTTP routes

Every route the control API serves under `/v1` is public. The supervisor's callback routes are public, because an external executor must call them to report an outcome. The `sensor-webhook` ingress is public, because external systems post to it.

## Template keys

Every key a template author writes in `rimsky.yml` is public.

## Config keys

Every key an operator sets in deployment configuration is public.

## Environment variables

Every `RIMSKY_*` variable the shipped code reads is public. This includes variables rimsky sets for itself when it spawns its own processes, such as `RIMSKY_PROCESS_ROLE`, and variables a spawned third-party binary must read, such as `RIMSKY_AGENT_PORT`. Variables read only by tests are not surface and are not enumerated.
