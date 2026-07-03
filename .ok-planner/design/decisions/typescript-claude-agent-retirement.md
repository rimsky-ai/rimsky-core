---
decision: typescript-claude-agent-retirement
status: as-is
aliases: []
---

# The TypeScript claude-agent was deleted atomically with the Go port

## Choice

The TypeScript claude-agent implementation — source tree, npm build machinery, compiled output, and its Node-based image — was deleted in the same change that landed the Go implementation. The image is now a Go build whose runtime carries the agent CLI's native (self-contained) binary distribution instead of a Node runtime. The fake-CLI test scaffolding was structurally rewritten: the stub is a small Go binary built into a test image layered on the production image and selected via the CLI-binary override env, speaking the same MCP-callback wire shape the real CLI uses. The former Node-based scaffolding (script, npm-install image pattern) is gone. The permissive-license carve-out that existed solely for the TypeScript reference was removed; the bundled service is copyleft like the rest of the services module.

## Rationale

Pre-v1 break-freely; no shim. Dual-implementation drift is not worth its cost once the Go replacement carries the full surface (proven by conformance and the cross-stack scenario suite).

## Alternatives

Keep the TypeScript reference alongside the Go port for permissive-surface parity — rejected: the licensing carve-out was written for the TypeScript executor as the sole entry; with the Go replacement, the carve-out serves no active purpose.
