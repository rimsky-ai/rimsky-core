---
story: substitution-doc-accuracy
status: as-is
---

# Substitution doc matches resolver

## Role

As a template author reading the substitution documentation, I can trust that the listed source kinds match exactly what the resolver actually recognizes, so that I don't silently miss a supported source.

## Capability

Automated accuracy gate: the documented list of substitution source kinds matches the runtime resolver's dispatch set.

## Business value

Template authors don't silently miss supported substitution sources because the doc undercounts; the gate catches drift at build time, not at template-author confusion time.

## Acceptance

An automated accuracy check asserts the documented list of substitution source kinds matches the runtime resolver's dispatch set. The check fails when the documented list undercounts, omits a real kind, or lists a kind the resolver doesn't handle.

## Falsifier

The check is informational only (doesn't fail CI), OR text-matches the doc without inspecting the resolver's runtime dispatch set.

## Proof

Executable proof.
