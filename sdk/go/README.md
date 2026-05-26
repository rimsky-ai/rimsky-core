# rimsky/sdk/go

The canonical Go-side implementer-facing surface for building services that
rimsky talks to. A peer Go module within the rimsky repo, alongside
`protocols/` and `foundation/`.

Houses:

- **Server scaffolding** (`server/`) — gRPC + HTTP+JSON bridge handlers for
  the claim-producer, executor, lifecycle-subscriber, blob-backend, and
  publisher protocols.
- **Publisher helpers** (`publisher/`) — publisher-side message-emit retry +
  backoff, idempotency-key header, callback POST handling.
- **Conformance library** (`conformance/`) — invocable from service authors'
  own Go tests in addition to the thin CLI wrappers in `cmd/rimsky-*-conformance`.
- **Testcontainer helper** (`testpg/`) — plain Postgres container for tests
  that need a Postgres backend without applying rimsky migrations.
- **Ops glue** (`ops/`) — stdlib `slog` setup, healthcheck HTTP endpoint, DSN
  env-var parser.

Dependency budget: `protocols/` + stdlib + minimal third-party
(`go-chi/chi`, `pgx/v5`, `testcontainers-go`, `grpc`, `protobuf`).
Enforced by the `sdk-purity` depguard rule.

See `concept:sdk` in `.ok-planner/design/concepts/sdk.md`.
