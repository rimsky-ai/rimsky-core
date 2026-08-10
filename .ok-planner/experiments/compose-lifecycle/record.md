---
experiment: compose-lifecycle
commit: PENDING
---

# A manifest applied, reconciled, inspected, and torn down

## What it ran against

A `rimsky-all-in-one` container from this tree's image set, addressed through a
CLI context, driven only by the compose verbs. The manifest declares two
templates with their tags and two instances, all against the bundled
`verifier-shape-checks` executor.

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
single command, and both listings came back clean.

One limit the run also measured, on a second stack: the compose verbs send no
credential. Against a deployment where `auth init` has been run, `compose plan`
and `compose up` fail with `401 unauthorized` under every key-passing mechanism
the CLI offers — `--endpoint --key`, `RIMSKY_API_KEY`, and an `api_key` stored
in the current context — while an ordinary verb (`ls tags`) authenticates from
that same context. The lifecycle above is therefore reachable on an
unauthenticated deployment only.

RESULT: PASS (unauthenticated deployment)
