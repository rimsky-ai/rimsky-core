# Per-language executor SDKs

**Date:** 2026-05-13
**Status:** sketch / wishlist
**Companion sketches:** `2026-05-13-blessed-typed-attributes.md`,
`2026-05-13-verifier-executor-convention.md`

## The gap

Writing a rimsky executor today means:

1. Standing up a gRPC service that implements `proto:executor.proto::Execute`.
2. Handling the capabilities handshake, callback URL, terminal events, named
   events, attribute writeback.
3. Resolving claim addresses to substrate-specific clients by hand.
4. Managing the executor's deployment lifecycle.

For a data engineer who wants to write a Python function that takes data in
and emits data out, this is substantial ceremony. Each new executor
re-implements the same protocol plumbing in whatever language the consumer
uses. The friction shows up most acutely in two places:

- **Data-engineering consumers** writing transformations. They want
  pandas / polars / Arrow ergonomics, not gRPC servers.
- **Agentic consumers** wiring up LLM calls. Today the `executors/claude-
  agent` reference impl wraps Claude-specific concerns into a usable surface;
  consumers wanting other LLMs or other agent shapes re-implement that
  wrapping themselves.

Per-language SDKs close both gaps without changing the executor protocol.

## What an SDK is

A library, per language, that:

- Hosts the gRPC service for the consumer's executor.
- Handles the capabilities handshake, callback URL registration, terminal-
  event marshaling, named-event emission, attribute writeback.
- Provides a decorator / class / builder API that lets the consumer write
  a function and have it be the executor.
- Includes a **substrate-adapter registry** for blessed typed attributes
  (see `2026-05-13-blessed-typed-attributes.md`) — turns blessed-attribute
  handles into language-native types.
- Provides error and retry shapes idiomatic to the language.

Two SDKs in the first wave: **Python** (the data-engineering default) and
**TypeScript** (because `executors/claude-agent` is already TS and patterns
can share). Go is workable directly against the existing protocol bindings;
add a Go SDK if a consumer demand emerges. Rust later.

## Python SDK shape

Pseudocode for the user-facing surface:

```python
from rimsky import executor, Reads, Writes, Table, Blob

@executor(name="zone-normalize", version="1.0")
def normalize(
    inputs: Reads[
        ("raw_zoning", Table),
    ],
    outputs: Writes[
        ("normalized_zoning", Table),
    ],
    params: dict,
):
    df = inputs.raw_zoning.to_polars()
    df = df.with_columns([
        pl.col("zone_code").str.to_uppercase(),
        pl.col("geometry").apply(make_valid),
    ])
    outputs.normalized_zoning.write_polars(df)

if __name__ == "__main__":
    executor.serve(port=9090)
```

The SDK handles:

- Service hosting on the declared port.
- Capabilities advertisement (`userdata_schema`, `declared_events`).
- Attribute resolution: `inputs.raw_zoning` is a `Table` instance wrapping
  the handle; `.to_polars()` resolves the handle via the operator-configured
  substrate adapter.
- Writeback: `outputs.normalized_zoning.write_polars(df)` stages a new
  version; the SDK manages the protocol-level writeback at terminal.
- Lifecycle: a `Complete` is emitted automatically when the function
  returns; an `Error` if it raises.
- Named events: `executor.emit("milestone", {...})` available inside the
  function.
- Park: `executor.snooze(resume_at=...)` available inside the function.
- Retries / blocked: idiomatic exceptions (`raise rimsky.BlockedError(...)`)
  that map to the protocol's error_class semantics.

`Reads[...]` and `Writes[...]` are typed containers. The SDK uses Python
type annotations to validate that the function's signature matches the
template's declared attribute schema at dispatch time, before the function
body runs.

## TypeScript SDK shape

```typescript
import { executor, Reads, Writes, Table, Blob } from "@rimsky/sdk";

export const handler = executor({
  name: "zone-normalize",
  version: "1.0",
  inputs: { raw_zoning: Table },
  outputs: { normalized_zoning: Table },

  async run({ inputs, outputs, params, ctx }) {
    const df = await inputs.raw_zoning.toArrow();
    const normalized = await normalize(df);
    await outputs.normalized_zoning.writeArrow(normalized);
  },
});

executor.serve({ port: 9090 });
```

Same machinery: handler-as-function; typed input/output containers;
substrate-resolved at access; protocol plumbing hidden.

## Substrate adapter registry

Each SDK ships with adapters for the blessed typed attributes:

- `blob` → `bytes`, `stream`, `iter_chunks` (Python); `Buffer`, `ReadableStream`
  (TypeScript).
- `table` → `to_arrow`, `to_pandas`, `to_polars`, `iter_rows`, `to_records`
  (Python); `toArrow`, `toRecords`, `iterRecords` (TypeScript).
- `geo` → `to_geopandas`, `to_geoarrow`, `iter_features`, `spatial_query`
  (Python); `toGeoArrow`, `iterFeatures`, `spatialQuery` (TypeScript).

Adapter resolution is operator-policy-aware. If the operator configured
`table` to be backed by `s3-parquet`, the adapter does Parquet column-pruned
reads against the S3 prefix that the handle resolves to. The user-facing
API doesn't change based on backing store.

Third parties can register adapters for substrates rimsky doesn't bless —
that's a separate plug point, used when a consumer wants their domain-
specific substrate to feel native in the SDK without going through claim
producer machinery. Optional; not first-cut.

## Error / retry shape

The protocol's error_class machinery maps to idiomatic exceptions:

- `raise rimsky.RetryableError(reason)` → `Error{error_class: "executor_errored"}`
  with the policy's retry handling.
- `raise rimsky.BlockedError(reason, payload)` → `Error{error_class:
  "executor_blocked"}`.
- `raise rimsky.GiveUpError(reason)` → `Error{error_class: "give_up"}`.
- Uncaught exception → `Error{error_class: "executor_errored"}` with
  exception details in the `details` field.

The SDK's job is to make the wire-shape consequences of these visible to
the consumer without requiring them to understand the protocol.

## Named-event emission

```python
@executor(name="discover")
def discover(inputs, outputs, params):
    for endpoint in candidates:
        if works(endpoint):
            executor.emit("endpoint_found", {"url": endpoint, "kind": "arcgis-rest"})
    outputs.endpoints.write(found)
```

Each `executor.emit(name, payload)` produces a `NamedEvent` in the protocol
stream before terminal. Templates that subscribe via `on_event` handlers
fire invalidates accordingly.

## Park / resume

```python
@executor(name="await-external")
def await_external(inputs, outputs, params, ctx):
    if ctx.resume_reason == "external_invalidate":
        result = read_external_result(ctx.session_token)
        outputs.result.write(result)
        return
    session = start_external_work()
    executor.snooze(payload=session, session_token=session.id)
```

Park / resume is first-class in the SDK. The resume dispatch sees
`ctx.resume_reason` and `ctx.session_token`, populated by the SDK from the
protocol's `ResumeContext`.

## Coexistence with `executors/claude-agent`

The existing TS `claude-agent` executor predates this SDK proposal. Two
options:

1. **Refactor `claude-agent` to use the TS SDK** once the SDK exists. The
   claude-agent-specific logic (CLI wrapping, MCP tools, session handling)
   stays where it is; the protocol plumbing moves to the SDK.
2. **Leave `claude-agent` as the worked example of bespoke protocol use.**
   The SDK is for consumers; `claude-agent` is a bundled reference impl.

Lean toward option 1 — the maintenance cost of two protocol implementations
in the same repo (the SDK and `claude-agent`'s bespoke server) is real, and
the SDK should be a credible enough surface that the reference impl uses it.

## Open design questions

1. **Subprocess executors.** Should the SDK support "run a subprocess; treat
   stdin / stdout as the data plane"? This is a common shape for wrapping
   existing CLI tools. Probably yes for Python; less clear for TypeScript.
2. **Async vs sync.** Python: support both `async def` and `def` handlers.
   TypeScript: native async. Go: native goroutines, no decision needed.
3. **Hot reload / dev mode.** During development, restart-on-change for the
   executor binary. Nice-to-have, not blocking.
4. **Conformance.** Each SDK needs to pass `cmd:rimsky-executor-conformance`
   in stub mode. The SDK's tests should include conformance runs as
   regression nets.
5. **Versioning.** SDK version, protocol version, supervisor version — three
   axes. The SDK should detect protocol-version mismatch at startup and fail
   loud, not at runtime.
6. **Distribution.** Python SDK on PyPI (`pip install rimsky`). TypeScript
   SDK on npm (`@rimsky/sdk`). Versioned alongside rimsky core; published
   from CI on tag.
7. **Bundling vs separate repos.** Python and TypeScript SDKs could live
   in-tree (`sdks/python/`, `sdks/typescript/`) or as sibling repos. In-tree
   is operationally simpler for version coordination; sibling repos let the
   SDKs move at their own cadence. Lean in-tree until cadence pressure shows.

## Phasing

**Phase 1**: Python SDK first ship.
- gRPC service hosting, capabilities handshake.
- Function decorator API.
- Adapter registry surface (without typed-attribute adapters yet — they
  come with the blessed types).
- Error / retry / blocked / park shape.
- Named-event emit.
- Distribution via PyPI.
- Worked example in `docs/agents/examples/python-executor.md`.

**Phase 2**: TypeScript SDK first ship.
- Same surface as Python, idiomatic for TypeScript.
- `executors/claude-agent` refactor onto the SDK as the validation.

**Phase 3**: Typed-attribute adapters.
- Sequence with the blessed-types implementation: each blessed type ships
  with Python and TypeScript adapters.
- Conformance: SDK adapter tests cover round-trip integrity per type per
  language.

**Phase 4** (deferred): Go SDK, Rust SDK as consumer demand drives.
