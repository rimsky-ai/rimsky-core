---
story: asset-management
status: as-is
---

# Operator manages instance-produced data assets

## Role

As an operator, I can list the data assets a running instance has produced, see the current version of each, materialize a new version on demand, walk the version history and materialization audit, retire an asset, and trace its lineage to consumers, so that I manage the data outputs nodes produce.

## Capability

Operator-driven asset lifecycle: list, version, materialize-on-demand, history, retire, lineage trace, through the control-api.

## Business value

Operators manage the data outputs nodes produce — driving re-materialization on demand, walking version history, retiring assets — without bouncing the template that produces them.

## Acceptance

Against an instance running a template whose nodes declare durable claims against a data-processing-capable producer (the asset construction per `concept:asset`), the operator queries the instance's assets through the control-api and sees each asset alias with its current version; triggering a re-materialization causes the supervisor to dispatch the producing node again and a new version row appears as a result of that real dispatch; the materialization-history surface lists each materialization with its outcome; deleting an asset removes the alias.

## Falsifier

Materialize trigger doesn't actually cause a producing dispatch, OR the version-history surface returns rows that don't match what really materialized.

## Proof

Executable proof.

## Notes

2026-06-08 — Story landed via spec 2026-06-08-design-corpus-bootstrap.
