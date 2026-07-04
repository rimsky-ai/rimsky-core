---
decision: bundled-registry-entrypoint
status: as-is
aliases: []
---

# A single bundled registration entrypoint registers every bundled handler across the in-proc registries

## Choice

The services module exposes one registration entrypoint that constructs and registers every bundled service handler across the two in-process registries (executors and claim producers), registers each executor's name-to-endpoint alias, and advertises each handler's capabilities into the discovery cache. Its parameters are narrow interfaces declared in the services module in protocols-only types; the rimsky side supplies adapters onto its own registries, resolver, and discovery cache, and calls the entrypoint once per unified process after the builtin utility-executor registration. Failure to construct any single configured handler aborts the boot with an error naming which handler failed; unconfigured handlers are skipped with a log line. Bundled registrations never shadow operator configuration: an executor or claim producer name already declared in the unified config keeps its configured endpoint, and the bundled in-proc handler for that name is not wired.

## Rationale

Atomic, explicit registration point — each bundled service contributes through its handler package, and the entrypoint enumerates them statically so the set is grep-visible and compiler-checked. The interface indirection exists because the services module may import only the protocols module; the concrete registries live in rimsky-internal layers it cannot see. Config-wins precedence preserves the documented mixed-mode story: operators who need an external instance of a bundled service just declare it.

## Alternatives

- Per-service `init()` side effects — rejected: convention-based registration; the registration set becomes invisible to grep and the compiler.
- Registering handlers from each role's startup independently — rejected: the unified process runs three roles; per-role registration would construct handlers (and dial claim-producer stores) multiple times per process.
- Bundled handlers shadowing config-declared names — rejected: an operator pointing a name at an external endpoint must get that endpoint; silent in-proc override would be impossible to debug.
