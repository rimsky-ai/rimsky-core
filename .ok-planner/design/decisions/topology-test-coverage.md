---
decision: topology-test-coverage
status: as-is
---

# Both deployment topologies are integration-tested

## Choice

The services integration harness covers both supported shapes: the single-process all-in-one (boot, assert one rimsky process serves all three role surfaces, drive a node to terminal, round-trip a memory-backend blob across roles) and the three-container split topology (boot scheduler, supervisor, and control-api as separate containers against shared Postgres, drive the same scenario to terminal).

## Rationale

The single-process mode (see `decision:single-process-mode`) changes the default deployment's process model; both supported topologies need a standing proof, not just the one the harness happens to boot.
