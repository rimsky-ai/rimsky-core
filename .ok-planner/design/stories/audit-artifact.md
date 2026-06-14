---
story: audit-artifact
status: as-is
---

# Operator inspects the durable record of a completed one-shot run

## Role

As an operator, I can inspect the durable record of a completed one-shot run, so that I can debug failures and verify successful runs without re-running.

## Capability

Every one-shot run leaves a per-run artifact directory behind at a stable, predictable path. The directory contains the rimsky state database, the blob spill root, and the synthetic configuration the run used. The operator opens the artifact with widely-available tooling for the format — no rimsky-specific reader is required — and pulls instance terminations, node-run history, attribute values, and the event log out by hand.

## Business value

A completed run is reviewable without re-running: failures can be debugged from the on-disk record, successful runs can be audited or referenced, and a run can be shipped as a single archivable directory for later post-mortem.

## Acceptance

After the orchestrator exits, a durable record of the run lives at a stable, discoverable location; the operator opens it and reads the recorded instance terminations, node-run history, attribute values, and event log directly, using widely-available tooling for the format.

## Falsifier

No durable record survives the process exit; OR the record contains only state metadata (last-known status flags) without per-node-run history; OR the record is in a format that requires rimsky-specific tooling to query.

## Proof

Demo — drive a small failing manifest, then walk through opening the artifact and pulling the failing node-run's terminal event out by hand.
