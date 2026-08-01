---
decision: topology-test-coverage
status: as-is
---

# Both deployment topologies are integration-tested

## Choice

The services integration harness carries a standing proof for each supported deployment shape: the single-process all-in-one and the three-container split (scheduler, supervisor, and control-api as separate containers against shared storage). Each proof boots the real topology and drives the same scenario to terminal through it.

## Rationale

The single-process mode (see `decision:single-process-mode`) changes the default deployment's process model; both supported topologies need a standing proof, not just the one the harness happens to boot.

## Alternatives

- Integration-test only the default topology and treat the other as a configuration variant — rejected: the untested topology's failure class is cross-role wiring (role-boundary assumptions, shared-state reachability), which would then surface only in deployments.
- Cover the second topology with per-role unit tests instead of a booted stack — rejected: per-role tests cannot observe the cross-process seams that distinguish the topologies in the first place.
