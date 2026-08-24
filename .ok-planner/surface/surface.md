# Surface intent

Which classes of element are public, and which specific elements depart from those rules. The audit's surface extractor reads this document and classifies what it finds; the owner is its authority.

## The general rule

Rimsky is a platform whose product is its interfaces. A consumer embeds it as a Go module, runs its images, implements its protocols, writes its templates, and operates its deployments. Every element that reaches such a consumer is public. An element is internal only when a rule below says so.

## Go modules

The root module `github.com/rimsky-ai/rimsky-core` and the protocols module `github.com/rimsky-ai/rimsky-core/lib/protocols` are the public embedding surface. A consumer fetches either at a release version and imports it. The foundation module and the services module are internal: they carry no tags, they resolve only through workspace replacements, and the lint denies a consumer the foundation module's internal packages.

## CLI verbs

Every verb and subcommand the `rimsky` binary dispatches is public, `rimsky conformance <protocol>` included.

## Images

Every image the `push-images` target publishes to the registry is public. Images built only for the test harnesses (`make test-images`) are internal.

## gRPC RPCs

Every RPC declared under `lib/protocols/proto/v1` is public. Third parties implement these protocols against rimsky, so each RPC is a contract rimsky owes them.

## HTTP routes

Every route the control API serves under `/v1` is public. The supervisor's callback routes are public, because an external executor must call them to report an outcome. The `sensor-webhook` ingress is public, because external systems post to it.

A bundled service's HTTP listeners split by what they carry. The protocol bridge, the lifecycle bridge, and the observability bridge are public: each carries the same contract as a gRPC protocol the rule above calls public, in another encoding, and a service author is told to expect them. A bundled service's admin listener and the claude-agent executor's internal MCP server are internal: they serve the service itself.

## Metrics

The metrics endpoint each core role can serve is public, and the metric names and labels it serves are surface: the environment variables that place the listener are public, and an operator who graphs the endpoint is a consumer.

## MCP tools

Every tool the control API's MCP catalog lists is public. An agent discovers the catalog and invokes the tools its grant permits, so every listed tool is a contract rimsky owes that agent.

## Template keys

Every key a template author writes in `rimsky.yml` is public.

## Config keys

Every key an operator sets in deployment configuration is public.

## Environment variables

Every `RIMSKY_*` variable the shipped code reads is public. This includes variables rimsky sets for itself when it spawns its own processes, such as `RIMSKY_PROCESS_ROLE`, and variables a spawned third-party binary must read, such as `RIMSKY_DAEMON_PORT`. Three variables outside the prefix are public exceptions: `ANTHROPIC_API_KEY` and `CLAUDE_CODE_OAUTH_TOKEN`, the vendor credentials the claude-agent executor reads and passes to its child, and `NO_COLOR`, the convention the CLI honours when it decides whether to colour its output. Platform variables the shipped code reads — `HOME` and `PATH` — are not surface. Variables read only by tests are not surface and are not enumerated.
