---
decision: claude-agent-env-passthrough-allowlist
---

# claude-agent exposes executor-container env to the CLI child via a declared allowlist

## Choice

The claude-agent handler reads an operator allowlist of env-variable names from its own process environment (the same env-var name across containerized and all-in-one modes), plus a per-dispatch expose-env list from each node's config. The intersection governs exposure: on CLI spawn (and resume), the handler looks up each node-declared, operator-allowed variable from its own process environment and adds it to that node's CLI child env, alongside the fixed rimsky-callback plumbing already passed to the child. A node declaring a name outside the operator allowlist fails that dispatch with an error naming the disallowed variable, the template instance, and the node. Unset operator allowlist plus unset per-node list is the safe default: only callback plumbing and Claude auth reach the child. The security invariant: rimsky never sees the plaintext env values — exposure is a per-node declaration in the template, guarded by the operator allowlist.

## Rationale

Secrets that an agent needs to authenticate against upstream services (MCP server auth headers, third-party API keys, per-catalog credentials) sit on the executor container, not in rimsky's substitution surface. Rimsky's `{{env.VAR}}` grammar (per `decision:env-as-substitution-source-kind`) is a general-purpose template-configuration mechanism whose resolved values land in the persisted attribute bag; putting a secret on that path makes rimsky a secret handler. The pass-through allowlist keeps rimsky out of the credential-handling loop entirely: the operator sets the secret as an env var on the executor process, adds the variable name to the operator allowlist, the template declares it on the node that needs it, and the agent reads it from its own environment at runtime via whatever mechanism the CLI already supports.

The allowlist is explicit rather than a whole-env spread for two reasons. First, it makes what the CLI child can see auditable at the executor's config layer — an operator reading the container's env config knows exactly which variables flow through. Second, it prevents accidental exposure of executor-internal env (Claude auth tokens, rimsky-callback tokens, other config knobs) that the CLI has no business seeing.

The split gives each party the control that belongs to it: the operator allowlist lives in the handler's process env because "which secrets are visible at all" is a deployment concern owned by whoever runs the service; the per-node list lives in node config because "which of the permitted secrets this node actually needs" is a template-author concern. Neither reaches across: a template author cannot widen the operator boundary, and the operator's allowlist alone exposes nothing until a node asks.

## Alternatives

- Whole `process.env` spread into the CLI child — rejected: exposes every executor-container env variable (auth tokens, rimsky-callback tokens, unrelated ops env) to the agent CLI; the allowlist is a small amount of code for a large audit-and-blast-radius benefit.
- Per-node declaration in node config WITHOUT an operator allowlist safeguard — rejected: template authors would gain unilateral control over which secrets the CLI child sees; the intersection preserves the "operator owns which secrets are visible at all" property.
- Rimsky-side `{{env.VAR}}` substitution as the secret path — rejected: lands plaintext in rimsky's persisted attribute bag and makes rimsky a secret handler; the substitution grammar stays for non-secret template configuration only.
- Executor-side `${env:VAR}` in-config substitution — rejected: a second substitution dialect on the same template surface violates one-idiom-per-job; the pass-through exposes plain env vars the CLI reads via ordinary environment access.
