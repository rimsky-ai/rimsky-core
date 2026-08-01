---
story: claude-agent-expose-env-per-node
status: as-is
---

# Template authors declare per-node expose-env; operators bound them

## Role

As a template author using the bundled claude-agent executor, I declare on each node the list of env-var names that should be exposed to that node's CLI child (so the agent can read them from its own environment) inline in the node config, while the operator running the claude-agent service separately declares an allowlist restricting which env-var names any template may expose; the service enforces the intersection. So that template authors own per-node secret needs and operators own the boundary of exposable env, without either reaching across into the other's territory and without secrets ever landing in rimsky's persisted attribute bag.

## Capability

Per-node inline `cli.expose_env` declarations name the env-var names each node's spawned CLI child should see. The operator allowlist lives on the claude-agent process's env; the handler enforces the intersection at dispatch, reads each allowed name from its own env, and adds it to the spawned child's env alongside callback plumbing. No operator-side rimsky.yml block keyed by node name is needed, and no plaintext env value ever lands in rimsky's persisted attribute bag.

## Business value

Template authors own per-node secret needs; operators own the boundary of exposable env; secrets never round-trip through rimsky.

