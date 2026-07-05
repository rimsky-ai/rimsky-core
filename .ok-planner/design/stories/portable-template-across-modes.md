---
story: portable-template-across-modes
status: as-is
---

# The same template file runs in both modes without edits

## Role

As a rimsky user (a developer promoting a locally-authored template to production, or an operator adopting a template someone else authored), I can use the same template file in both modes — all-in-one and multi-container — without editing it, so that there's no dev-vs-prod template dialect and locally-working templates are directly promotable.

## Capability

The template's node config, its structure, and its referenced service kinds are byte-identical across modes. What differs between modes is what belongs to modes — the rimsky.yml naming external service endpoints (containerized) or its absence (all-in-one), and per-service env vars set on the appropriate process (all-in-one process env vs container env).

## Business value

No dev-vs-prod template dialect; locally-working templates promote directly to production.

## Acceptance

A template that dispatches to bundled executors and claim producers runs to the same terminal graph shape under all-in-one (in-process handlers) and under a containerized deployment (standalone gRPC bundled-service images), with no changes to the template file, GIVEN a normalized operator context across the two runs (same env-var values readable by the bundled handlers, same clock domain, same input files).

## Falsifier

The template needs edits (any change to node config, template structure, referenced service kind names, or per-node service declarations) to run in the other mode; OR the same template file byte-for-byte reaches a different terminal graph shape under the two modes when the operator context is normalized (differences in blob-handle payloads and other persistence-backend-inherent opaque values are permitted; the graph's terminal shape and each node's terminal tag class must agree).

## Proof

Executable proof — a scenario test drives the same template file byte-for-byte through both modes: once through the all-in-one process (in-process handlers, no rimsky.yml) and once through a containerized deployment (standalone bundled-service images named in a rimsky.yml). The two runs share a normalized operator context. The test asserts template-byte-equality between the two runs, then asserts terminal-graph-shape equality (same nodes reach terminal, same terminal tag class per node), tolerating persistence-backend-inherent differences in opaque handle values.
