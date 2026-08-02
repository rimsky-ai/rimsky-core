---
decision: per-service-load-opts-from-env
---

# Each bundled service owns one shared env-options loader

## Choice

Each bundled service's handler package exposes a single construction function reading that service's operator env vars into a per-service options struct. The standalone main calls it to construct the gRPC-hosted handler; the bundled registration entrypoint calls the same function to construct the in-process handler. Neither surface re-parses the same env vars twice. Absence of a service's required configuration is expressed in the options (not as a load error): a claim producer whose config env is unset, or the CLI-spawning executor with no credentials and no stub mode, reports itself unconfigured. The bundled registration entrypoint skips unconfigured services with a log line; the standalone main treats the same condition as a startup error, because a standalone container exists only to run that one service. Present-but-invalid configuration is an error on both surfaces.

## Rationale

Single source of truth for each service's operator env parsing: the same env produces the same handler behavior across modes without duplicated parsing code. Splitting "unconfigured" from "misconfigured" is what keeps the zero-config all-in-one boot possible — a local process without database credentials or CLI credentials must still boot and serve the services that need neither.

## Alternatives

- Have the bundled registration entrypoint accept pre-constructed options from callers — rejected: pushes env-parsing responsibility onto every all-in-one entrypoint invocation and defeats the same-behavior-across-modes property.
- Treat unset config as a boot error in both modes — rejected: makes credential-less zero-config boot impossible (a database-backed producer can never invent a connection string).
- Register unconfigured handlers anyway and fail at dispatch — rejected: a dispatch-time construction failure surfaces as a confusing per-run error instead of a clear registration-time skip.
