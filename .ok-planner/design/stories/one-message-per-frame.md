---
story: one-message-per-frame
status: as-is
---

# Template author relies on one message per frame for well-defined substitution

## Role

As a template author,

## Capability

I can rely on substitution from the message body always being well-defined in a node that's reacting to a message,

## Business value

so that no template ever has to refuse a multi-message coalesced frame at runtime.

## Acceptance

Across all instances and templates, a frame carries at most one delivered message. A node whose attribute schema substitutes from a typed message body always has exactly one body to read; the substitution never refuses or returns an ambiguous value. Two messages posted in close succession produce two frames (one each).

## Falsifier

Two messages share a frame; OR a template that substitutes from a message body fails at substitution time with a "multiple messages" error.

## Proof

Executable proof. N messages posted in succession produce N distinct frames, each carrying one message, each settling cleanly with body substitution resolving the expected values.
