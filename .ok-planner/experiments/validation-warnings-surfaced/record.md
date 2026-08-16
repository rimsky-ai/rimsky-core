---
experiment: validation-warnings-surfaced
commit: d977250c
---

# The static validator's advisories in the validate and register responses

## What it ran against

A `rimsky-all-in-one` container booted from this tree's image at
`RIMSKY_IMAGE_TAG`, on its zero-config SQLite defaults. `run.py` posts one
template to `POST /v1/templates/validate` and `POST /v1/templates`, with and
without `warnings_as_errors=true`, and drives the same template through
`rimsky template lint` and `rimsky template register` with and without
`--warnings-as-errors`. The template declares an `error_types` policy for the
class `totally/made-up`, which no executor and no producer declares — the
static validator's "not in any declared vocabulary" advisory, and nothing else.
`run.py` requires the CLI built at `bin/rimsky` and removes its own container.

## What was observed

Five legs, thirteen checks, none failing.

`POST /v1/templates/validate` answered `ok: true` and carried the advisory in
`validation_warnings`. With `warnings_as_errors=true` the same request answered
`ok: false` and still named the advisory that flipped it.

`POST /v1/templates` answered `201` and carried the advisory in
`validation_warnings`. With `warnings_as_errors=true` it answered `400`, echoed
`warnings_as_errors: true`, named the advisory, and persisted no template row —
the catalog held the same count before and after.

`rimsky template lint` printed the advisory, and `--warnings-as-errors` turned
its verdict to `ok: false`. `rimsky template register --warnings-as-errors`
refused and printed the advisory.

`rimsky template register` without the flag still printed only
`{"template_id": "sha256-…"}` under `-o json`. The HTTP response it was reading
carried `validation_warnings`; the CLI's own projection of a successful
registration drops them.
