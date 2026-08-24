---
concept: executor
---

# Executor

## What it is

An executor is the party that performs a node's work. It implements the executor protocol: rimsky sends it one dispatch, and it returns exactly one outcome. Executors come in two forms — handlers registered inside a rimsky process, and services running outside it — and the outcome vocabulary is identical across both. The observability handshake is not: an out-of-process service advertises its capabilities through the observability protocol, while an in-process handler advertises them straight into the discovery cache when it registers and never receives a handshake. An outcome is either one of a closed family of settling verdicts, or a deferral that hands the verdict to a later callback against the supervisor whose body settles the dispatch with one of those same verdicts. A park verdict names when to resume (see `concept:parked-state`). An executor also advertises an expected-attributes schema that declares every node attribute the executor reads, and registration rejects a node that sets an attribute the schema does not declare (see `decision:expected-attributes-schema-closed`).

## Purpose

An executor performs a template's actual work. An out-of-process executor lets an author write that work in any language, scale it on its own, and — through the deferred verdict — run it for far longer than one call. An in-process executor delivers small utility primitives, such as counters and gates, without a separate deployment. Both forms present the same protocol surface, so the dispatch path treats them uniformly.

## Boundaries

An executor owns the work of one dispatch, the outcome it returns, and the opaque scratch bytes it attaches to that outcome (see `decision:scratch-protocol`). It owns the deadlines that bound a dispatch (see `decision:three-dispatch-deadlines`). It owns the keepalive and incremental attribute-writeback channels a running dispatch uses, and the durable registry that lets a deferred verdict arrive after the supervisor restarts.

An executor does not own dispatch routing, which is the supervisor's. It does not own attribute-schema validation, which rimsky performs at dispatch and at commit, or substitution, which rimsky performs before dispatch. How scratch carries across a dispatch that supersedes an earlier one belongs to `concept:node-run`. Stitching a terminal event to a producer verb belongs to `concept:terminal-resolution`, the operator's choice of what to do with an error outcome belongs to `concept:error-policy`, and the observability protocol surface itself — the handshake, the advertisement mechanics, the refresh policy — belongs to `concept:observability`. See also `concept:attribute`, `concept:terminal-tag`, `concept:service`, and `concept:service-auth`, which authenticates a dispatch and its callback return leg.
