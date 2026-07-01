---
decision: env-as-substitution-source-kind
status: as-is
aliases: []
---

# Host-environment variables ride the substitution grammar as a sixth source kind

## Choice

The substitution grammar (per `concept:attribute`) includes `env.<VAR_NAME>` as a sixth source kind alongside `claim`, `params`, `nodes`, `messages`, and `child`. The directive `{{env.VAR}}` resolves at dispatch time by reading the supervisor process's environment for `VAR`. Name shape is `[A-Za-z_][A-Za-z0-9_]*`. Unset is a missing source; empty-but-set resolves to the empty string. The kind induces no subscription edge — like `params`, `claim`, and `child`, it reads non-graph context. The `?` lenient marker and `| <literal>` fallback operator apply uniformly to `env` directives.

## Rationale

One substitution surface, one idiom. Before this decision, host-env injection lived only in the claude-agent executor as an ad-hoc `${env:VAR}` resolver applied to HTTP MCP header values at spawn time. That scope was both too narrow (other surfaces where operators reach for env — MCP server URLs, stdio command paths, claim-producer scope-data strings — had no substitution at all) and gratuitously inconsistent with the in-graph grammar (Mustache braces with a dotted path on one side, shell-style dollar-brace with a colon separator on the other). A template author's first guess at substituting an env value was `${VAR}` (bare shell), which sat verbatim in the template, dispatched verbatim to the executor, and produced silent downstream auth failures with no error path back to the author.

The closed-kind-enumeration shape rimsky already uses for `nodes.X.attribute.Y` / `params.foo` / `claim.X.payload.bar` extends cleanly: add one switch case in the resolver, add `env` to the validator's recognized-kinds regex, leave the cascade-edge derivation alone (env is non-graph context, same as `params`). Discovery improves because there is one substitution grammar to document, one set of recognized kinds, one set of error messages. The registration-time validator rejects out-of-grammar references at template paste time rather than leaving them as silent literals.

The supervisor reads the env, not the executor. For all-in-one deployments this is identical to the prior behavior. For split deployments, operators set the env on the supervisor container. The resolved value lands in the dispatch attribute bag before the executor sees it.

Scope: `{{env.VAR}}` substitution is a general-purpose template-configuration mechanism (hostnames, URLs, feature flags, non-secret runtime knobs). It is **not** the mechanism for routing secrets to an executor. Operators are free to put anything they want in the supervisor's env — rimsky exerts no control over that — but the resolved value lands in rimsky's persisted attribute bag as plaintext, so a secret placed on this path is a secret rimsky is now handling. When a secret must reach an executor (e.g. a credential the agent CLI needs to authenticate against an MCP server), the executor-side pass-through is the intended path — see `decision:claude-agent-env-passthrough-allowlist` for the claude-agent shape — and rimsky's substitution surface stays out of the loop.

## Alternatives

`${env:VAR}` retained as a parallel syntax — rejected. Two substitution dialects on the same template surface is the exact uniformity violation Plumbline's "one idiom per job" rule exists to prevent. Authors guess the wrong one half the time; the discovery cost compounds.

`${VAR}` warning at registration with no grammar change — rejected. Catches the typo but does not provide a usable substitution surface for other operator needs (MCP URLs, stdio paths, etc.). Lower value than collapsing to one grammar.

Executor-side env resolution preserved — rejected. Keeps secrets on the executor container (an operational positive) but at the cost of two substitution layers and the inconsistent syntax. Pre-v1, the cleaner consolidation wins; if a future deployment shape demands per-executor secret locality, that lives in a separate concept (e.g., executor-scoped param injection) rather than as a parallel substitution dialect.

In-grammar functions for env (e.g., `{{env.VAR | required}}`, `{{env.VAR | base64decode}}`) — rejected. Out of scope per `concept:attribute`'s non-goal on function-form substitution; aggregation and transformation live in receiver executors, not the substitution layer. The existing `?` lenient marker and `| <literal>` fallback cover the common author needs.
