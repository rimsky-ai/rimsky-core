---
experiment: compose-lifecycle
commit: d977250c
---

# A manifest applied, reconciled, inspected, and torn down

## What it ran against

A `rimsky-all-in-one` container from this tree's image set, published on a port
the script picks free at start, addressed through a CLI context, driven only by
the compose verbs. The manifest declares two templates with their tags and two
instances, all against the bundled `verifier-shape-checks` executor. The
deployment is in the shipped default posture, with no keys — which is the only
posture in which the compose verbs work at all, measured separately by the
compose-namespace-guard experiment's authenticated way.

## What was observed

`compose plan` listed all eight steps before anything was applied, naming the
namespaced identities it would create (`compose:lifecycle-demo:alpha@1`,
`compose:lifecycle-demo:one`). `compose status` listed each declared resource as
`manifest-missing-from-api`. `compose up --yes` applied the eight steps;
afterwards the tag, template, and instance listings carried the
`compose:lifecycle-demo:` prefix on every resource, and the templates read
`deployed`. A second `compose up` reported `no changes`, so the verb reconciles
rather than re-applies, and `compose status` then read `in-manifest` for all
four. After driving both instances terminal with `instance kill`, one
`compose down --yes` removed instances, deployments, tags, and templates in a
single command — eight steps again — and both listings came back clean.

Eighteen checks, none failing.

RESULT: PASS
