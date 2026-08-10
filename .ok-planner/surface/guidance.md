# Surface guidance

Rules for ruling each enumerated element public or private. The audit run
applies these rules; it does not write them.

## The general rule

Rimsky is a platform whose product is its interfaces. A consumer embeds it as a
Go module, runs its images, implements its protocols, writes its templates, and
operates its deployments. Every element that reaches such a consumer is public.

An element is private only when a rule below says so.

## cli-verbs

Every verb the `rimsky` binary dispatches is public.

## images

Every image the `push-images` target publishes to the registry is public.

## grpc-rpcs

Every RPC declared in `lib/protocols/proto/v1` is public. Third parties
implement these protocols against rimsky, so each RPC is a contract rimsky owes
them.

## http-routes

Every route the control API serves under `/v1` is public. The supervisor's
callback routes are public, because an external executor must call them to
report an outcome. The `sensor-webhook` ingress is public, because external
systems post to it.

## template-keys

Every key a template author writes in `rimsky.yml` is public.

## config-keys

Every key an operator sets in deployment configuration is public.

## env-vars

Every `RIMSKY_*` variable the shipped code reads is public. This includes
variables rimsky sets for itself when it spawns its own processes, such as
`RIMSKY_PROCESS_ROLE`, and variables a spawned third-party binary must read,
such as `RIMSKY_AGENT_PORT`.

All of them should be documented. A variable that governs behavior an operator
can observe is a variable an operator can set, and rimsky does not ship a
variable it declines to explain.

Variables read only by tests are not surface and are not enumerated.
