---
story: claude-agent-expose-env-per-node
status: as-is
---

# Template authors declare per-node expose-env; operators bound them

## Story

As a template author using the bundled claude-agent executor, I declare per node which environment variables that node's agent may read, while the operator running the claude-agent service separately bounds which variables any template may expose, and the service enforces the intersection — so that template authors own per-node secret needs and operators own the exposure boundary, with secret values never landing in rimsky's persisted state.
