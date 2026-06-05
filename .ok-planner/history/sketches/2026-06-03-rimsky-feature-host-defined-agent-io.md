# Feature need: host-defined typed I/O for the claude-agent executor

This is a need description, not an implementation plan. We know there
are multiple ways to build this; this writeup is about the **why** so
the rimsky team can choose the shape that fits the rest of the
platform best.

## The need in one paragraph

When rimsky's `claude-agent` executor runs a dispatch, the agent
currently has a fixed pair of terminal tools —
`mcp__rimsky-callback__report_complete` and
`mcp__rimsky-callback__report_error` — that it calls with a free-form
payload to end the dispatch. The host application (the project using
rimsky for orchestration) has **no way to constrain the shape of that
payload at the agent's boundary**. The agent picks the output
attribute keys; rimsky records whatever the agent wrote; downstream
consumers may or may not find what they expected. The host needs a
way to define a **typed tool surface** the agent sees — tools whose
input is schema-validated by the host (with errors returning to the
still-running agent for in-session retry) and whose successful
invocation **is** the dispatch's terminal signal. No separate "I'm
done" tool the agent has to remember and can misuse.

## A concrete failure that motivates this

A real example from a zonebase pipeline running against rimsky v0.5.0:

- A node `discover-gis-endpoints` (executor: `claude-agent`)
  declares its output attribute schema:
  ```yaml
  endpoints:
    type: array
    readOnly: true
    items: { type: object, ... }
  ```
- The system prompt asks the agent to discover GIS endpoints for a
  jurisdiction.
- A downstream node consumes that output via the substitution
  `{{nodes.discover-gis-endpoints.attribute.endpoints}}`.

What happened on the run: the agent did its work, then called
`report_complete` with a payload that contained five attribute keys
of its own choosing (`jurisdiction`, `service_directories`,
`overlay_districts`, `proposed_rezonings`, `zoning_boundaries`) —
**none of them `endpoints`**. The control-api accepted the report
(the schema is open under `additionalProperties: true`); the
downstream node tried to substitute `{{nodes.X.attribute.endpoints}}`;
substitution failed; the dispatch terminal-errored with
`template_resolution_failed`; the pipeline blocked.

The agent's work was real and useful. The shape didn't match. There
was no point at which rimsky could have told the agent "wrong shape,
try again," because the agent's report was *already the terminal
signal* by the time rimsky saw the payload.

Tightening the system prompt is a probabilistic mitigation, not a
fix. The shape mismatch is a contract problem, not a prompt-quality
problem.

## The pattern, generalized

This is **not a one-off**. Any rimsky user wiring agentic work into a
typed downstream pipeline hits the same wall:

- The downstream graph has typed expectations of the agentic node's
  output (`{{nodes.X.attribute.<field>}}` reads, schema validation
  at receiver dispatch).
- The agent has free latitude inside its tool calls and report
  payloads.
- There's a gap between the two — that gap is where most agentic-
  pipeline production bugs live.

The fix has to be structural: make the agent's I/O surface a contract
**enforced at the boundary**, where invalid attempts come back to the
agent as errors it can act on, not as terminal failures of the whole
dispatch. The agent should not be able to declare "done" with a
payload that doesn't match the host's expectations.

## What the feature needs to enable

These are capabilities, not API specs. Pick the implementation shape
that fits rimsky's roadmap.

- **The host defines which MCP tools the agent sees on a given
  dispatch.** Configurable per-template / per-executor / per-dispatch
  — implementation choice. The current pair of rimsky-callback tools
  is one possible "profile"; the host should be able to define
  alternatives.

- **Each tool in the profile is schema-validated by the host.**
  Invalid input does not terminate the dispatch — it returns an
  actionable error to the agent's in-session MCP loop. The agent
  retries until it submits a valid call (subject to a host-defined
  retry budget) or hits its own give-up condition.

- **Successful invocation of a host-defined tool can drive a dispatch
  state transition.** A tool can be declared as "this call terminates
  the dispatch with state X and attributes derived from the payload."
  Calling `record_endpoints(valid_payload)` IS the terminal/success
  event; calling `record_error(class, payload)` IS the
  terminal/error/<class> event. The agent never emits a separate
  "I'm done" — terminal state is a consequence of the work the agent
  did, not a declaration the agent makes.

- **The default `mcp__rimsky-callback__report_complete` /
  `report_error` tools can be excluded** from the agent's visible
  tool set. The host can opt out of the free-form report path
  entirely. No escape hatch the agent can find when it's confused.

- **The host's MCP server is the validation surface**, and the host
  is responsible for declaring how its tools map to rimsky's terminal
  states. Rimsky's responsibility is to honor that mapping in the
  dispatch lifecycle.

## Why this belongs in rimsky, not in each host application

The dispatch lifecycle is rimsky's. The terminal signal is rimsky's.
The MCP server configuration for `claude-agent` is rimsky's. So the
place that says "these tools the agent sees, those tools terminate
the dispatch" has to be rimsky too. Otherwise every host application
ends up reimplementing the same bridge — a custom MCP proxy that sits
between the agent and rimsky's callback, validates, and translates —
and each implementation fights against the rimsky-callback default
that's still in scope.

This is general enough that any rimsky user doing typed agentic I/O
will want it. Pushing it into the host doesn't eliminate the
complexity; it just moves it N times.

## Adjacent surfaces this would clean up

- **Prompt engineering load.** The output shape stops needing to be
  documented redundantly in the system prompt; the validated tool's
  schema is the contract. Prompt revisions stop being a load-bearing
  defense against shape drift.
- **Retry semantics for "wrong shape".** Currently the dispatch
  terminal-errors and the cascade stops. With validated tool calls,
  shape errors stay inside the dispatch's lifetime; the cascade only
  sees terminal/success with a valid payload.
- **The `additionalProperties: true` cosmetic problem.** The
  claude-agent's `expected_attributes_schema` is permissive precisely
  because the agent's output keys aren't knowable ahead of time. If
  the host's MCP tool defines the keys, the rimsky-side schema can
  be tighter.

## What this writeup deliberately does NOT specify

- Whether the profile config lives per-template (alongside `cli:` and
  `system_prompt:`), per-executor (in rimsky.yml), per-dispatch (on
  the instance-create body), or some combination.
- Whether rimsky's claude-agent composes multiple MCP servers,
  proxies through one, or uses a discovery mechanism the host
  provides.
- Whether terminal-state declarations live in the MCP tool definition
  itself (a custom schema annotation), in a rimsky-side mapping
  table, or are inferred from tool semantics.
- Whether this is a `claude-agent`-only feature or a property of any
  future "agent-shaped executor" rimsky ships.
- Whether other rimsky-callback verbs (`heartbeat`, `report_progress`
  if those exist) should also be profile-controlled.

All of these are real design choices, and they're rimsky's to make.
This writeup is just the why.
