---
audit: mcp-http-parity
artifact: decision:mcp-http-parity
text: compliant
implementation: supported
commit: d977250c
audited: 2026-08-16T05:34:05Z
---

# The MCP tool catalog as a full projection of the HTTP control surface, on the same gates

Supported. Enumerated the control API's action registry from source: 49 actions. One is a permission marker with no HTTP route at all, so it is not on the surface to project. Of the 48 routed actions, 44 carry at least one MCP tool, and every one of those tools has a declared input schema; the four that do not are named in an in-tree exemption table with a stated reason each, and a test fails both when a routed action gains no tool and when an exempt action quietly gains one. Two further tests close the loop from the other side: every registered tool resolves back to an action that has an HTTP route to re-dispatch through, and no input schema is declared for a tool no action registers. The second half of the choice holds by construction rather than by convention: a tool call does not reach a handler directly — the catalog rebuilds the caller's request as an inner HTTP request against the same router, carrying the caller's own authorization header, so the identical per-action permission gate runs on it. The MCP entry route is itself permission-gated, and dry-run and idempotency arguments are translated into the same query parameter and header the HTTP surface reads.

## Remediation

- The four exempt routed actions — the MCP transport route itself, the unauthenticated health probe, the unauthenticated CA-root fetch, and the permissionless identity echo — are HTTP reads with no MCP tool, so the decision's unqualified "every read and mutation" is broader than what ships; the enforced population is every permissioned routed action.
