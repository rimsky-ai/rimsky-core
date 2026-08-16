---
experiment: template-lifecycle
commit: PENDING
---

# Template catalog lifecycle through the CLI

## What it ran against

A `rimsky-all-in-one` container booted from this tree's image, with its
zero-config SQLite defaults and its in-process bundled executors. The probe
drives the `rimsky template` and `rimsky instance` verbs against the
container's control API. `run.sh` boots the container, runs the whole
lifecycle, and removes the container on exit.

## What was observed

The whole probe passed at this tree; every step of the catalog lifecycle
answered. `template register` returned a content-addressed template id and the
catalog listed the template in state `registered`; `template get` returned the
stored spec. `instance create` was refused before deploy and accepted after
it. With a live instance, `template undeploy` was refused ("template has
active instances") and `template rm` was refused ("undeploy first"). After the
instance was killed, `undeploy` moved the template to `undeployed` and further
`instance create` calls were refused. `template rm` was refused while the
terminated instance's record still referenced the template, and succeeded once
that record was deleted; the catalog then no longer listed the template.

That last refusal still arrives as an HTTP 500 carrying the raw SQLite text
`FOREIGN KEY constraint failed`, not as a conflict naming the referencing
rows. The operation is correctly refused; only its diagnosis is coarse.
