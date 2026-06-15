---
story: uncovered-substitution-rejected
status: as-is
---

# Template author gets a registration error for an uncovered substitution ref

## Role

As a template author, I get a registration error when a substitution ref has no covering subscription, naming the ref and showing the subscription entry that would cover it.

## Capability

The template-registration validator walks every node's attribute schema, parses the substitution refs, and matches each against the receiver's `subscribes:` block. Any ref with no covering entry produces a structured registration error whose body names the receiver, the literal ref text, the schema path the ref appears in, and a copy-pasteable subscription entry the author can drop in.

## Business value

Template authors — human or LLM agent — get a precise, programmatically-consumable fix suggestion at registration time. No silent acceptance with deferred runtime failure; no orphan reads slipping through to dispatch.

## Acceptance

An author writes a template where node A reads `{{nodes.X.attribute.Y}}` (or `{{nodes.X.event.Y}}`, or the whole-pull `{{nodes.X.attribute}}`) but A's `subscribes:` block contains no entry whose `node:` plus `type:` would deliver that signal. The template-registration endpoint returns a registration error whose body names the uncovered ref (the receiver, the ref text, the schema path the ref appears in) and includes a copy-pasteable subscription entry the author could add.

## Falsifier

The template registers despite the uncovered ref (silent acceptance with deferred runtime failure), or registration fails with a generic error that doesn't name the specific uncovered ref or doesn't show a copy-pasteable fix.

## Proof

All-of-the-above — example templates exhibiting each uncovered shape (attribute field ref, event ref, whole-pull), plus an executable proof asserting the registration response body shape and content.
