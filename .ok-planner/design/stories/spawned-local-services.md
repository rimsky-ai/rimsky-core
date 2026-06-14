---
story: spawned-local-services
status: as-is
---

# Operator declares local executor binaries spawned for a single run

## Role

As a developer or consumer-project author, I can declare local executor binaries that get spawned for a single run, so that consumer projects can ship a binary plus a one-line wrapper instead of a service installer.

## Capability

The one-shot verb accepts repeatable bindings that name a local executor binary by path (or by alias). For each binding, the verb spawns the binary as a child process, waits for it to bind its listening port, registers the resulting loopback endpoint in the run's service registry, and dispatches the manifest's referencing nodes to it. When the run exits, the spawned children exit with it — no separate daemon to start, no leaked process.

## Business value

A consumer project that ships an executor binary can offer a one-line wrapper around the one-shot verb instead of a service installer or a long-lived daemon configuration — the binary is owned by the run, lives for the duration of the run, and disappears with it.

## Acceptance

The operator initiates a one-shot run, naming a local executor binary by path. The orchestrator spawns the binary, the manifest's nodes that reference it execute through it, and when the run exits the spawned process exits with it.

## Falsifier

The binary spawns but the manifest's nodes don't reach it (their dispatches fail or hang); OR the binary spawns but is leaked after the verb exits (visible as a stray process); OR the spawn requires the operator to also run a separate long-lived daemon first.

## Proof

Executable proof — small manifest with one node referencing a stub local executor; the run launches the binary, drives the node through it to success, exits; a post-exit process check confirms no leak.
