---
issue: executor-host-port-envs-inconsistently-prefixed
kind: audit
category: decision-drift
artifacts:
  - decision:operator-env-namespaced-per-service
status: promoted
opened: 2026-08-02T09:58:02Z
sprint: 2026-08-03-audit-gap-drain.md
---

# Three of the four bundled executors prefix their host/port env vars; the fourth and the decision say unprefixed

The four bundled executors (the standalone task-running services rimsky ships as images) disagree on how their listen host/port environment variables are named. The claude-agent executor reads the unprefixed forms (`env:RIMSKY_EXECUTOR_HOST`, `env:RIMSKY_EXECUTOR_PORT_GRPC`, `env:RIMSKY_EXECUTOR_PORT_HTTP`); http-node, verifier-http, and verifier-shape-checks each read per-service-prefixed variants. The governing decision says generic host/port knobs "stay unprefixed" — its namespacing rule exists for behavior-specific knobs like allowlists, where cross-service collision is real (`decision:operator-env-namespaced-per-service`).

The decision's own rationale draws no line between the four: all are standalone-binary-capable, and when bundled in-process none of them reads transport envs at all, so the collision argument for prefixing never materializes for host/port. An operator configuring standalone containers simply meets two naming dialects depending on which executor they deploy — exactly the coexisting-dialects state the repo's uniformity rule forbids.

The ruling decides which dialect survives; the direction is already forced.

## Options

- Rename the three prefixed executors' host/port envs to the unprefixed form. Cost: a breaking env-var rename for existing deployments (legal pre-v1).
- Amend the decision to bless the split. Cost: requires inventing a distinguishing principle that does not exist in the code, and leaves two dialects permanently.

## Ruling

> Generated ruling (/verify-issues): rename http-node, verifier-http, and verifier-shape-checks to the unprefixed host/port env forms, matching claude-agent and the decision as written. The decision's stated rationale applies identically to all four executors, so blessing the split would need a distinction the code doesn't have, and the uniformity rule (one idiom per job, no coexisting dialects) forces the sweep. Pre-v1 the breaking rename is legal; it lands via a sprint because it is operator-facing.

<!-- Owner: this is a recommendation, not your decision. Leave it
as-is to accept — the next /plan-sprint carries it, naming the
generated/recommended batches at sign-off. Edit the text to
redirect, empty the section to discuss live, or delete this note
to adopt the ruling as your own. -->
