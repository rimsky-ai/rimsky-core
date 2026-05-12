---
tension: mcp-server-fold-into-control-api
category: overloaded
status: resolved
spec: 2026-05-11-design-log-convergence
affects:
  - mcp-server
  - control-api
  - executor
resolution:
  shape: fold-into-control-api
  dropped: concepts/mcp-server.md
  folded-into: concepts/control-api.md (Agentic MCP shim subsection)
  summary: |
    Folded mcp-server into control-api as the "Agentic MCP shim"
    subsection. Strict pass-through frontend, no business logic — not
    a standalone noun. Dual-MCP-role distinction (claude-agent's
    per-run internal MCP server vs. operator control-plane shim)
    noted inside the new subsection.
---

# `mcp-server` is structurally a thin frontend to `control-api`; promote-as-concept overstates its standalone weight

## What is muddy

`concepts/mcp-server.md` documents the standalone Go module under `mcp-servers/control-api/` that wraps rimsky's HTTP control-api as MCP (Model Context Protocol) tools. Strict pass-through (`Boundaries`: "no validation, no caching, no synthesis"). Its only structural identity is "alternate frontend to control-api speaking MCP instead of HTTP+JSON." A reader walking the noun catalog finds one concept entry for the shim and one for control-api; the shim entry's content is mostly "this is how the control-api is exposed to MCP-speaking agents."

The dual-MCP-role observation (operator control-plane MCP in `mcp-servers/control-api/` vs. per-run executor-local MCP embedded in `executors/claude-agent/`) is a useful distinction but is one paragraph that can live under whichever concept hosts the operator-side shim.

## Why it matters

- Catalog parsimony: 46 concepts is well over the 15–25 heuristic; concepts whose body is structurally "thin alternate-frontend wrapper, no runtime logic" are the most defensible consolidation candidates.
- A reader navigating the catalog learns nothing about the operator surface from `mcp-server.md` that `control-api.md` doesn't already imply once a one-paragraph "Agentic MCP shim" sub-section is added.

## Resolution candidates (do NOT pick)

- **Fold** `mcp-server.md` into `control-api.md` as a sub-section ("Agentic MCP shim: a standalone Go module that wraps the control-api as MCP tools; strict pass-through, no business logic; forwards `Authorization: Bearer <CONTROL_API_TOKEN>`; catalog hand-curated in `tools.go`"). Move the dual-MCP-role aside (claude-agent's per-run internal MCP server is a distinct surface) into the same sub-section or into `executor.md`'s Adjacent block. Drop `concepts/mcp-server.md`. Update any `Adjacent: mcp-server` references to point at `control-api`.
- **Keep standalone** (status quo).

(Pre-decided shape: fold.)

## Evidence

- `concepts/mcp-server.md`.
- `concepts/control-api.md` Adjacent block (already cites `mcp-server`).
- `_discover/control-api-mcp-server.md`.
- `_discover/2026-05-10-typescript-executor-claude-agent.md` (per-run internal MCP server in claude-agent).
- `review-notes.md` "Possible merges / splits to reconsider" / `mcp-server` bullet.

