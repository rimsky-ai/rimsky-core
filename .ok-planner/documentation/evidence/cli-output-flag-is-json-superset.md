---
trap: cli-output-flag-is-json-superset
release: d977250c
---
# Evidence set — `--output`/`-o` selects a format and `-o json` is interchangeable with `--json`; other formats (`yaml`, `table`) are available through it.

Source of the prior: name-promise — `--output` and `--o` alongside `--json` in `cli-flags`

## What the audit ran and observed (assumption record)

`.ok-planner/experiments/assumption-cli-output-flag-is-json-superset` — built
for this run — settled all three claims at the parser, plus one byte
comparison on `rimsky ctx list`, which needs no server.

`-o json` and `--json` are never interchangeable, because no verb accepts
both: of 30 read verbs, 25 take `-o json` only, `auth list` takes `--json`
only, and 4 take neither. The two spellings sit on disjoint verb sets, so on
any given verb exactly one of them is a parse error.

`-o yaml` is rejected — `unknown output format "yaml" (want human|json)`.
`-o table` is worse than rejected: it is accepted and silently means human.
`ctx list -o table` returned the `-o human` rendering byte for byte, so an
operator who asks for a table gets no error and no table. `-o text` behaves
the same way. There are two formats behind the flag, and the error message
names them both.

## Experiment record (experiment:assumption-cli-output-flag-is-json-superset)

# `--output` as a superset of `--json`

## What it ran against

The CLI built from this tree, with no server: an undefined flag and an unknown
format value are both rejected before any request is dialled, so pointing the
endpoint at a closed port turns "connection refused" into the signal that the
spelling was accepted. `rimsky ctx list` needs no server at all, so the
format-vs-format comparison runs on real bytes.

Three claims, one check each: is `-o json` interchangeable with `--json` (does
any single verb accept both, over the same 30 read verbs stage 1 of the
`--json` experiment enumerates); is `-o yaml` available; is `-o table`
available as a rendering distinct from `-o human`.

## What was observed

No verb accepts both spellings. Of 30 read verbs, 25 take `-o json` only, 1
(`auth list`) takes `--json` only, and 4 take neither — the two flags sit on
disjoint verb sets, so neither can substitute for the other anywhere.
`-o yaml` is rejected: `unknown output format "yaml" (want human|json)`.
`-o table` is accepted but is a synonym: `ctx list -o table` returned the
`-o human` rendering byte for byte. `-o json` does render differently from
human, so the switch itself works — there are two formats behind it, not four.
3 checks, 0 pass, 3 fail.

Runnables: `src:.ok-planner/experiments/assumption-cli-output-flag-is-json-superset/` at the stamped commit.
