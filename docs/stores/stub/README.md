# stub claim-producer

The `stub` claim-producer is an in-memory test fixture. It is not
intended for production and refuses to start unless an explicit
acknowledgement flag is passed.

## When to use it

- Conformance suites and scenario tests.
- Local development where you want claim semantics without the cost
  of a real Postgres or filesystem.

For production, use `docs/stores/postgres/README.md` or
`docs/stores/filesystem/README.md`. For custom producers, implement
`protocols/proto/v1/claim_producer.proto`.

## Configuration

```yaml
claim_producers:
  test_fixture:
    kind: stub
    endpoint: dns:stub-producer.svc.cluster.local:9002
    write_semantics_allowed: [exclusive, staged_async]
    config:
      stub_mode: true
```

`stub_mode: true` is required; without it the binary exits with a
non-zero status. The conformance probe at startup
(`rimsky-conformance-probe`) confirms stub mode is active before
running LLM-calling tests.

## Wire shape

The stub advertises whatever `write_semantics_allowed` the operator
declares. Open/Commit/Abandon/Release are no-ops on producer-side
state; the only side effect is the in-memory claim ledger that
enforces conflicts.

## Operating

Single-process, in-memory. State does not persist across restarts.
Suitable for a single-replica test deployment; multi-replica is
unsupported because the in-memory ledger does not coordinate.
