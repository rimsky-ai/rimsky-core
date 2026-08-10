---
experiment: verifier-shape-checks
commit: PENDING
---

# Declared shape checks over tabular data, run by the bundled verifier

## What it ran against

A `rimsky-all-in-one` container booted from this tree's image at
`RIMSKY_IMAGE_TAG`, on its zero-config SQLite defaults. The bundled
shape-checks verifier runs in-process inside that container and is reachable as
the executor name `verifier-shape-checks` with no service wiring of any kind.
`run.py` registers and deploys four templates through the control API, drives an
instance of each to rest, and reads each verifier node back off the node
observability route. It boots and removes its own container.

## What was observed

Four legs, seven checks, none failing.

Three rows satisfying all three declared checks — `pk_unique`,
`numeric_range`, `row_count_absolute` — settled the node fresh, and the
verifier's own output recorded that it ran 3 checks over 3 rows. Three rows
violating two of them left the node with no fresh run and one failed run, and
the terminal signal was `terminal/error/verifier/check_failed/pk_unique`.

The verdict follows the declaration, not the data alone: the clean rows,
re-submitted under a declaration carrying one extra `no_nulls` check on a field
the rows do not have, were rejected with
`terminal/error/verifier/check_failed/no_nulls`. A check kind the verifier does
not implement failed the node with `verifier/attribute_invalid` rather than
passing silently.

The rows and the checks both reach the verifier as node attributes. An earlier
leg tried to feed the rows from an upstream node through an attribute
substitution (`source:` reading `nodes.<type>.attribute.rows`); the template
validator requires the receiver to subscribe to the upstream's
`attribute/<field>/changed` signal for that, and the upstream passthrough node
never emitted that signal, so the verifier was never dispatched. That leg was
replaced by the stricter-declaration leg above, which settles the same question
the story asks — whether the checks a template author declares are what govern.
