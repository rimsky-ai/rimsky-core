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

## Acceptance

I author a template with two claude-agent nodes, each declaring a different expose-env list in its node config (node A wants `VALIDATOR_TOKEN` exposed; node B wants some other env-var name exposed). The operator's allowlist covers both names. Each node dispatches; each spawned CLI child sees only the env vars its own node declared present in `process.env` (VALIDATOR_TOKEN visible to A's child but not B's; the converse for B). No plaintext env value appears in rimsky's persisted attribute bag. Separately, when the operator's allowlist excludes an env-var name that a node declares, the service rejects that dispatch with an operator-facing error naming the disallowed env-var name, the template, and the node.

## Falsifier

Two claude-agent nodes with different declared expose-env lists observably see the same env-var set in their CLI children's `process.env`; OR a node declaring an env-var name outside the operator's allowlist dispatches anyway and gets that env var passed through; OR the per-node expose-env list requires operator-side keying (rimsky.yml, service container env keyed by node); OR the rejection error is generic (doesn't name disallowed env-var, template, or node); OR a plaintext env-var value appears in rimsky's persisted attribute bag; OR rimsky's own dispatch payload or protocol shape gains an expose-env field (the redesign must live inside the claude-agent service).

## Proof

Executable proof — a scenario test registers a template with two claude-agent nodes declaring different expose-env sets; the operator's allowlist covers both names. The test dispatches both nodes and asserts observable per-node env visibility — each spawned CLI child sees exactly the env vars its own node declared (via witness — the child writes the env vars it saw to a per-node file, or the child's MCP calls carry the values back). An additional assertion greps rimsky's persisted attribute bag for the plaintext values and confirms neither appears. A second scenario adds a third node declaring an off-allowlist env-var name; the test asserts the service rejects that dispatch with an error naming the disallowed env-var, template, and node. An additional assertion greps rimsky's protocol surface for expose-env-related fields and confirms none were added.
