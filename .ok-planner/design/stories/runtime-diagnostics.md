---
story: runtime-diagnostics
status: as-is
---

# Operator inspects runtime wedge state

## Story

As an operator, I can inspect the parked nodes, the live wait-set edges, the frames a holding-subgraph is gripping, and the current holders of a claim, so that I see why the runtime is wedged when an instance isn't progressing.

Runtime-diagnostics read surfaces: parked nodes, wait-set edges, held frames, claim holders, through the control-api or MCP.

Operators see why the runtime is wedged when an instance isn't progressing — without ad-hoc database spelunking.
