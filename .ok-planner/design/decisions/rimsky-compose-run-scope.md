---
decision: rimsky-compose-run-scope
---

# The compose one-shot stays manifest-only

## Choice

The compose one-shot verb keeps its current shape: it self-hosts a stack and drives a compose manifest to terminal. It is not extended to accept template files; the ephemeral-run verb covers the single-template case (see `decision:rimsky-run-self-hosts-templates`). The other compose verbs (up, down, plan, status) are unchanged.

## Rationale

Verb-to-input is one-to-one with no autodetect ambiguity: a manifest goes to the compose verb, a template goes to the run verb.

## Alternatives

- Extend the compose one-shot via file-content autodetection — rejected: makes verb behavior depend on inspecting the file's shape and produces ambiguous error messages when the shape is wrong.
