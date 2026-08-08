---
issue: atomic-staging-readme-release-semantics-wrong
kind: human
category: doc-drift
artifacts:
  - concept:atomic-staging
  - concept:claim-producer
  - decision:doc-accuracy-gates
status: retired
sprint: 2026-08-08-ruled-intake-drain.md
opened: 2026-08-07T21:55:24Z
github: https://github.com/rimsky-ai/rimsky-core/issues/100
---

# The atomic-staging example's README tells copiers that Release is safe; it deletes their data

rimsky ships worked examples meant to be copied and modified. One demonstrates
atomic staging — write into a side area, then swap it into place atomically on
commit. Its README states that Release is a no-op for read-intent claims, and
that only Abandon drops the staging directory.

Release is a direct alias for Abandon. It removes the staging directory
unconditionally and never inspects intent. Because rimsky issues Release when an
instance terminates, someone who copies this pattern and trusts the README loses
staged work on ordinary teardown, silently.

The corpus already states the correct rule — the atomic-staging concept says
Release of a claim whose staging was never committed is equivalent to Abandon.
The README contradicts a concept doc that got it right.

Four smaller defects sit in the same file, all re-verified live:

- The template snippet uses two configuration keys that don't exist on the node
  definition. Both ingestion paths reject unknown fields, so the snippet cannot
  be loaded as written.
- The README credits the sweeper as a reaper for *leaked* staging directories.
  It is wired with an empty live-handle set, so it reaps everything past its TTL
  — live claims included.
- "Two-rename atomic swap" describes the overwrite path. The first write does one
  rename; the second only fires when a canonical version already exists.
- The producer returns the staging path as raw bytes into a JSON column. Not a
  README sentence to correct, but worth a copier knowing.

## Ruling

> Retired: the examples module is being removed in full, so the README this issue
> reports on ceases to exist and the drift it documents is dissolved rather than
> corrected. The documentation project maintains the cookbook that replaces it.
>
> Findings underneath these issues that concern rimsky rather than the examples
> were pulled out as their own issues before retirement — an unenforced publisher
> kind, an unproven ordering guarantee on terminal events, and a lifecycle
> callback that cannot refuse — so nothing about the platform is lost with the
> module.
