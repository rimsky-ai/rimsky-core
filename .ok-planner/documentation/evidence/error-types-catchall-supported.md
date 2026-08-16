---
trap: error-types-catchall-supported
release: d977250c
---
# Evidence set — a bare `*` entry in `error_types` catches every class not otherwise mapped, so a template can declare one fallback policy instead of enumerating classes.

Source of the prior: published-concept — `concept:error-policy` (trailing-wildcard vocabulary entries, "the same trailing-wildcard convention `concept:signal` fixes")

## What the audit ran and observed (assumption record)

The experiment `assumption-error-types-catchall-supported` registered four
versions of one node against a URL that does not resolve, so each run raises
the same `http/network_error`. The exact key works: `{"http/network_error":
{action: pass}}` registered clean and the node settled fresh. The bare `*`
registers and routes nothing — the template was accepted with the warning
`error class "*" is not in any declared vocabulary … the policy registers but
will only match if a peer emits this exact class`, and the node failed.
`http/*` behaved identically. Both are indistinguishable from declaring
nothing: the node with an empty `error_types` failed the same way. Runtime
policy lookup is an exact match on the class name, so no fallback entry exists;
an author who declares one fallback policy instead of enumerating classes gets
default handling for every class they did not name, and the only signal is a
registration warning that does not fail the register.

## Experiment record (experiment:assumption-error-types-catchall-supported)

# Declaring one fallback policy with a bare *

## What it ran against

A `rimsky-all-in-one` stack with an ordinary `rimsky-executor-http-node`. Four
versions of one node — keyed on the exact class, on a bare `*`, on the
emitter's `http/*` family, and with no `error_types` at all — each dispatched
against a URL that does not resolve, so every run raises the same
`http/network_error`.

## What was observed

The exact key works and is the baseline: `error_types: {"http/network_error":
{action: pass}}` registered with no warning and the node settled `fresh`.

The bare `*` registers and routes nothing. The template was accepted, with a
warning reading `error class "*" is not in any declared vocabulary … the policy
registers but will only match if a peer emits this exact class`, and the node
`failed`.

`http/*` behaved the same way — the node `failed`.

Both are indistinguishable from declaring nothing: the node with an empty
`error_types` failed identically.

Runnables: `src:.ok-planner/experiments/assumption-error-types-catchall-supported/` at the stamped commit.
