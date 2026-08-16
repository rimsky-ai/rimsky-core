---
experiment: assumption-cli-output-flag-is-json-superset
commit: PENDING
---

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
