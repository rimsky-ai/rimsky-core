---
experiment: assumption-error-types-catchall-supported
commit: PENDING
---

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
