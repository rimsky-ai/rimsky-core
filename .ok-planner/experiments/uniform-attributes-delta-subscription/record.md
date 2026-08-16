---
experiment: uniform-attributes-delta-subscription
commit: d977250c
---

# One predicate on the verdict's attributes, across success and error

## What it runs against

`executor_bridge.py` is a third-party executor speaking the HTTP-bridge
transport: it answers `POST /v1/Execute` with a success outcome or an error
outcome, each carrying the same `verdict` attributes-delta, chosen by the
dispatched node's type. `run.py` starts it on a free host port and boots a
`rimsky-all-in-one` container at `RIMSKY_IMAGE_TAG` with a mounted `rimsky.yml`
registering it as the executor `verdict`.

The template declares four producer nodes on that executor — success and error,
each with each verdict value — and four watcher nodes. Every watcher carries the
identical subscription: type `terminal/*`, predicate
`payload.attributes_delta.verdict == "red"`. One operator wake runs the graph,
and the run reads the event log.

## What was observed

The executor wrote the same verdict attribute alongside a success and alongside
an error, and the two producers settled on different terminal kinds:
`terminal/success` and `terminal/error/probe/refused`. The one subscription
form, predicated only on the attribute value, fired on both — the watcher over
the succeeding producer ran once and the watcher over the erroring producer ran
once — with no per-kind entry written for either.

The same form stayed silent over the two producers whose verdict carried the
other value, on both terminal kinds. All four producers ran, so the two silent
watchers were silent by predicate and not by a missing signal. The erroring
producer's attribute was not lost to its error: its watcher settled.

Seven checks, none failing.
