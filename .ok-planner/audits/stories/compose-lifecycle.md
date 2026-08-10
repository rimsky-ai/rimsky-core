---
audit: compose-lifecycle
artifact: story:compose-lifecycle
determination: supported
compliance: compliant
commit: PENDING
audited: 2026-08-10T05:25:00Z
---

# A manifest applied to a running rimsky, reconciled, inspected, and torn down

Supported. Against a running deployment, `compose plan` listed all 8 steps
before anything was applied and named the namespaced identities it would create;
`compose status` reported each of the 4 declared resources as missing from the
API; `compose up` applied the 8 steps, after which every tag and instance
carried the `compose:<project>:` prefix and both templates read deployed; a
second `compose up` reported no changes, so the verb reconciles rather than
re-applies; and one `compose down` removed instances, deployments, tags, and
templates together. One measured limit bounds where this is reachable: the
compose verbs send no credential, so against a deployment where authentication
has been enabled they fail with 401 under every key-passing mechanism the CLI
offers — endpoint-plus-key flags, the api-key environment variable, and an
api-key stored in the current context — while an ordinary verb authenticates
from that same context.
