---
story: asset-management
status: as-is
---

# Operator manages instance-produced data assets

## Role

As an operator, I can list the data assets a running instance has produced, see the current version of each, walk the version history and materialization audit, retire an asset, and trace its lineage to consumers, so that I observe and govern the data outputs nodes produce.

## Capability

Operator-driven asset observation and governance: list, version, history, retire, lineage trace, through the control-api.

## Business value

Operators observe and govern data outputs — walking version history, retiring assets — without bouncing the template that produces them; re-materialization is expressed through messages (the empty-message trigger for a whole-instance re-run, or a typed message the template author designs for partial paths).

## Acceptance

Against an instance running a template whose nodes declare durable claims against a data-processing-capable producer (the asset construction per `concept:asset`), the operator queries the instance's assets through the control-api and sees each asset alias with its current version; the materialization-history surface lists each materialization with its outcome; deleting an asset removes the alias.

## Falsifier

The materialization-history surface returns rows that don't match what really materialized, OR delete fails to remove the alias.

## Proof

Executable proof.
