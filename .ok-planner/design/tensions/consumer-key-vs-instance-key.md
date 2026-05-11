---
tension: consumer-key-vs-instance-key
category: inconsistent
status: open
affects:
  - instance
---

# Legacy spelling `consumer_key` coexists with current `instance_key`

## What is muddy

`instance_key` is the current name for the optional dedup hint on `rimsky_instances`. CLAUDE.md "Non-obvious gotchas" notes: "Instances bind to `template_hash` at creation. FK is `template_hash TEXT`. `instance_key` (formerly `consumer_key`) is nullable. The instance HTTP create body is `{template, instance_key?, params}`."

But the old name `consumer_key` survives in:

- Early prose, sketches, and design docs.
- (Likely) some Go field-name or comment references.

The rename happened with the template-redesign; the old name has not been fully scrubbed.

## Why it matters

A reader grepping for `consumer_key` finds stale references; a reader grepping for `instance_key` finds current code. Both terms refer to the same column. Documentation drift accumulates as long as both spellings live.

## Resolution candidates (do NOT pick)

- Hard rename: grep and replace, kill `consumer_key` everywhere.
- Add a `vocabulary-lint-ignore: consumer_key` for the historical references that must remain (e.g. CHANGELOG entries).

## Evidence

- CLAUDE.md "Non-obvious gotchas" — "instance_key (formerly consumer_key)".
- `_discover/2026-05-10-content-addressed-templates.md` does not cite `consumer_key` directly but mentions instance HTTP body shape.

