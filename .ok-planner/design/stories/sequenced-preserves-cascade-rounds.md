---
story: sequenced-preserves-cascade-rounds
---

# Sequenced mode dispatches once per cascade round

## Story

As a template author whose workload must observe every distinct cascade round — audit trails, accumulators, rapid-flip detection — I can opt a node into the sequenced cascade mode (`concept:cascade-mode`), so that M cascade rounds produce M dispatches in arrival order, each seeing the inputs of its own moment, and no intermediate state my executor exists to capture is coalesced away.
