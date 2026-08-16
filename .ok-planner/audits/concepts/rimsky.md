---
audit: rimsky
artifact: concept:rimsky
text: compliant
implementation: unsupported
commit: d977250c
audited: 2026-08-16T04:44:00Z
checked: 24
unaccounted: 1
---

# The CLI concept's eight invariants and its six capability surfaces read against the rimsky binary's verb table

Every invariant holds, but the enumerated capability surfaces do not cover the binary's whole verb table, so the verdict is unsupported. Reading the binary's top-level dispatch table from source gives 24 dispatchable verb groups beside the version and help builtins; 23 of them fall under one of the six named surfaces — dev-loop, compose, resource, context, authentication, host-agent control — and one does not. The invariants themselves check out: the control-api client package speaks HTTP and JSON with no protocol-buffer or gRPC import anywhere in it; the compose workflow stamps a compose-origin header that the control API evaluates against a capability grant before permitting reserved-prefix tags or instance keys, and the compose plan, state, and teardown paths all scan by that prefix; every control-api verb resolves its key from a flag, then an environment variable, then the current context's stored key; the auth-status verb calls without a key and lets the server decide, and the anonymous bootstrap posts the key-creation request with no token after a pre-check that refuses when the deployment already reports itself authenticated; the ephemeral-run verb takes a template either as a positional file or as a named-template flag and rejects both together, merges a whole-params blob with repeatable per-entry flags later-wins, binds late-bound services by name to a local path, and chooses self-host versus remote from the endpoint with an explicit self-host flag that overrides a configured context and conflicts with an explicit endpoint; contexts carry an optional api-key field alongside the endpoint; and source-file references resolve relative to the template file's own directory with absolute paths and subtree escapes rejected before anything reaches the wire, covered by tests for both refusals. The self-hosted ephemeral run and the compose one-shot do share the same self-host machinery, and the binary does double as the host-agent daemon under the agent verb.

## Unaccounted

- The conformance verb group, which runs the protocol conformance suites over gRPC against an executor, claim-producer, publisher, validation, or data-processing endpoint: it fits none of the six named surfaces, and its gRPC and protocol-buffer usage sits beside the concept's "HTTP+JSON only; no proto" invariant, which the client package honours but the binary as a whole does not.
