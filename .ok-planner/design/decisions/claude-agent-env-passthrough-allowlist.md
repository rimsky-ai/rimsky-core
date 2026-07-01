---
decision: claude-agent-env-passthrough-allowlist
status: as-is
aliases: []
---

# claude-agent exposes executor-container env to the CLI child via a declared allowlist

## Choice

The claude-agent executor reads an allowlist of env-variable names from its own container environment. On CLI spawn (and resume), it looks up each named variable from the executor process's `process.env` and adds it to the CLI child's env dict, alongside the fixed rimsky-callback plumbing already passed to the child. Variables not on the allowlist are not exposed. Empty/unset allowlist is the safe default: the CLI child gets only the rimsky-callback plumbing and Claude auth, and no operator env leaks through.

## Rationale

Secrets that an agent needs to authenticate against upstream services (MCP server auth headers, third-party API keys, per-catalog credentials) sit on the executor container, not in rimsky's substitution surface. Rimsky's `{{env.VAR}}` grammar (per `decision:env-as-substitution-source-kind`) is a general-purpose template-configuration mechanism whose resolved values land in the persisted attribute bag; putting a secret on that path makes rimsky a secret handler. The pass-through allowlist keeps rimsky out of the credential-handling loop entirely: the operator sets the secret as an env var on the executor container, adds the variable name to the executor's expose-env allowlist, and the agent reads it from its own `process.env` at runtime via whatever mechanism the CLI already supports.

The allowlist is explicit rather than a whole-env spread for two reasons. First, it makes what the CLI child can see auditable at the executor's config layer — an operator reading the container's env config knows exactly which variables flow through. Second, it prevents accidental exposure of executor-internal env (Claude auth tokens, rimsky-callback tokens, other config knobs) that the CLI has no business seeing.

The allowlist lives in the executor container's env rather than in each template's node config for the same "one control plane per concern" reason: secrets and their exposure policy are a deployment concern owned by whoever runs the container, not a per-template author decision. A template author's node config doesn't declare secrets and shouldn't have a knob that changes which env vars the CLI child sees.

## Alternatives

Whole `process.env` spread into the CLI child — rejected. Simpler code, but exposes every executor-container env variable — auth tokens, rimsky-callback tokens, unrelated ops env — to the agent CLI. The allowlist is a small amount of code (one config parse, one env-var-name loop) for a large audit-and-blast-radius benefit.

Per-template allowlist declared in node config — rejected. Puts the "which secrets are visible" decision at the template layer, where template authors don't own secrets. The right split is: operators own the executor container's env AND its expose-env policy; template authors write templates that reference the agent's expected behavior without touching credentials.

Preserve rimsky-side `{{env.VAR}}` substitution as the secret path (drop this executor-side allowlist) — rejected. That path lands plaintext in rimsky's persisted attribute bag and makes rimsky a secret handler. The trade-off is deliberate: rimsky's substitution grammar handles non-secret template configuration; secrets stay executor-side and travel through the pass-through allowlist to the agent.

Reintroduce executor-side `${env:VAR}` in-config substitution (the retired shape) — rejected. Two substitution dialects on the same template surface violate one-idiom-per-job. The pass-through allowlist doesn't add a dialect; it exposes plain env vars to the agent, which the CLI reads via its ordinary `process.env` access.
