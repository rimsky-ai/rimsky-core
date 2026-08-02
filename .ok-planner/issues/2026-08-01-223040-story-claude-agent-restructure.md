---
issue: story-claude-agent-restructure
kind: sprint
category: stories-splits
artifacts:
  - story:claude-agent
  - decision:cli-spawn-mechanism
  - decision:signoff-crypto-ed25519
  - concept:error-policy
status: open
opened: 2026-08-01T22:30:40Z
---

# claude-agent: split, duplicated prescriptions, and an unhomed error taxonomy

## Problem

`story:claude-agent` bundles at least five outcomes (CLI subprocess dispatch, per-node MCP declarations + allowlist, per-node expose-env + allowlist, sign-off gating, error-class observability); the MCP and expose-env outcomes already have their own stories (`story:claude-agent-mcp-servers-per-node`, `story:claude-agent-expose-env-per-node`). Its prose also prescribes mechanisms that duplicate `decision:cli-spawn-mechanism` and `decision:signoff-crypto-ed25519`, and enumerates a thirteen-class declared error taxonomy that no concept or decision states anywhere — the one commitment in the prose with no home.

## Candidates

- Split the story, strip the duplicated prescriptions, and home the error-class taxonomy in a new decision (or an amendment to concept:error-policy / concept:executor).
- Keep the umbrella story; home only the taxonomy.
- Rule the taxonomy below corpus altitude (protocol docs + conformance own it) and reduce the story.
