---
decision: env-as-substitution-source-kind
status: as-is
aliases: []
---

# Host-environment variables ride the substitution grammar as a sixth source kind

## Choice

The substitution grammar (per `concept:attribute`) includes `env.<VAR_NAME>` as a sixth source kind alongside `claim`, `params`, `nodes`, `messages`, and `child`. An `env` directive resolves at dispatch time by reading the supervisor process's environment; names take the conventional environment-variable shape. Unset is a missing source; empty-but-set resolves to the empty string. The kind induces no subscription edge — like `params`, `claim`, and `child`, it reads non-graph context. The lenient marker and literal-fallback operator apply uniformly to `env` directives.

## Rationale

One substitution surface, one idiom. A parallel executor-local resolver dialect (shell-style dollar-brace against a narrow surface such as MCP header values) is both too narrow — surfaces like MCP server URLs, stdio command paths, and claim-producer scope-data strings equally need env values — and gratuitously inconsistent with the in-graph grammar's Mustache-braced dotted paths. A bare-shell guess like `${VAR}` sits verbatim in the template, dispatches verbatim to the executor, and produces silent downstream auth failures with no error path back to the author; under one grammar the registration-time validator instead rejects out-of-grammar references at template paste time.

The closed-kind-enumeration shape the grammar already carries for the node, param, and claim kinds extends cleanly, and the cascade-edge derivation is untouched (env is non-graph context, same as `params`). Discovery improves because there is one substitution grammar to document, one set of recognized kinds, one set of error messages.

The supervisor reads the env, not the executor. For split deployments, operators set the env on the supervisor container. The resolved value lands in the dispatch attribute bag before the executor sees it.

Scope: `{{env.VAR}}` substitution is a general-purpose template-configuration mechanism (hostnames, URLs, feature flags, non-secret runtime knobs). It is **not** the mechanism for routing secrets to an executor. Operators are free to put anything they want in the supervisor's env — rimsky exerts no control over that — but the resolved value lands in rimsky's persisted attribute bag as plaintext, so a secret placed on this path is a secret rimsky is now handling. When a secret must reach an executor (e.g. a credential the agent CLI needs to authenticate against an MCP server), the executor-side pass-through is the intended path — see `decision:claude-agent-env-passthrough-allowlist` for the claude-agent shape — and rimsky's substitution surface stays out of the loop.

## Alternatives

- `${env:VAR}` retained as a parallel syntax — rejected: two substitution dialects on the same template surface is the exact uniformity violation Plumbline's "one idiom per job" rule exists to prevent; authors guess the wrong one half the time, and the discovery cost compounds.
- A registration warning on bare-shell references with no grammar change — rejected: catches the typo but does not provide a usable substitution surface for other operator needs (MCP URLs, stdio paths, etc.); lower value than collapsing to one grammar.
- Executor-side env resolution — rejected: keeps secrets on the executor container (an operational positive) but at the cost of two substitution layers and an inconsistent syntax; the cleaner consolidation wins, and per-executor secret locality, if a deployment shape ever demands it, is a separate concept rather than a parallel substitution dialect.
- In-grammar functions for env (required-markers, decoders) — rejected: out of scope per `concept:attribute`'s non-goal on function-form substitution; aggregation and transformation live in receiver executors, not the substitution layer, and the existing lenient marker and literal fallback cover the common author needs.
