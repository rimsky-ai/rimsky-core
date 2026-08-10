---
audit: portable-template-across-modes
artifact: story:portable-template-across-modes
determination: supported
compliance: compliant
commit: PENDING
audited: 2026-08-10T07:30:00Z
---

# One template file drives both deployment modes unedited

Supported. One template file, naming a bundled executor and carrying its own
inline dataset, was run against both of the 2 modes the story names: a
zero-config all-in-one deployment, and a multi-container deployment of postgres,
a separately containerized executor, and three role containers whose commands
name control-api, scheduler and supervisor. The same file registered, deployed
and instantiated on each, each run reached a terminal state, and the verifier
node reported one fresh run on each. The file's hash was unchanged after both
runs, and both deployments content-addressed it to the same template hash — the
same spec, not a mode-specific dialect. What differed between the modes was the
deployment's own configuration, which is the operator's file, not the author's
template.
