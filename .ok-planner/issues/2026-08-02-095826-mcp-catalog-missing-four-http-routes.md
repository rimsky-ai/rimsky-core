---
issue: mcp-catalog-missing-four-http-routes
kind: audit
category: decision-drift
artifacts:
  - decision:mcp-http-parity
  - story:mcp-transport
status: verified
opened: 2026-08-02T09:58:26Z
---

# Four of the 45 control-API actions have no MCP counterpart, so an MCP-only client can't do everything

The control API promises transport parity: every read and mutation the HTTP surface offers is also an MCP tool, so an agent speaking only MCP is a first-class operator (`decision:mcp-http-parity`, `story:mcp-transport`). Enumerating all 45 registered HTTP actions against the MCP tool catalog and the one registered resource catalog finds four with no counterpart: the two instance frame reads, the observability read, and service enrollment (`code:lib/control/controlapi/actions.go`, `code:lib/control/controlapi/mcp/catalog.go`).

The parity machinery itself is sound — every MCP tool re-dispatches through the same router HTTP uses, and a test proves every mounted route has a registry entry — but nothing proves the converse, that every registry entry has a tool, which is the direction this gap lives in. Three of the four are mechanical additions following the ~41 existing registrations. The fourth is the reason this needs a ruling: the observability read is a wildcard route (a path-suffix pattern, not a fixed path), and the catalog's parameter substitution only handles named placeholders — no existing tool wraps a wildcard, so its tool shape is a small genuine design choice (a path-suffix argument on one tool, versus enumerating known sub-paths as separate tools).

The ruling decides how the four close and how the gap class is prevented from recurring.

## Options

- Close all four: three mechanical tool registrations, plus a path-suffix-argument tool for the observability wildcard, and add the converse coverage test (every action has a tool or resource) so the class can't silently reopen. Cost: the wildcard tool exposes an open-ended path argument through MCP, which is a coarser interface than the catalog's named-parameter idiom.
- Close three mechanically and carve enrollment (or the wildcard) out of the parity claim as named exceptions. Cost: the parity promise becomes "parity, except…", and the story's MCP-only operator loses named capabilities.

## Ruling

> Recommended ruling (/verify-issues): close all four — the three mechanical registrations, a single observability tool taking a path-suffix argument for the wildcard route, and the converse coverage test so every future action must ship its tool.
>
> Rationale: both artifacts state unqualified parity, the mechanism makes additions cheap, and the coverage test converts a recurring audit finding into a compile-time-ish guarantee; the carve-out option spends the promise's simplicity to avoid one modestly awkward tool. The path-suffix argument mirrors what the HTTP route already accepts, so it adds no capability MCP callers couldn't have via HTTP. Flip case: if enrollment is deliberately machine-bootstrap-only and the owner wants it invisible to MCP operators, name that single exception in both artifacts — but keep the coverage test, with an explicit allowlist for the named exception.

<!-- Owner: this is a recommendation, not your decision. Leave it
as-is to accept — the next /plan-sprint carries it, naming the
generated/recommended batches at sign-off. Edit the text to
redirect, empty the section to discuss live, or delete this note
to adopt the ruling as your own. -->
