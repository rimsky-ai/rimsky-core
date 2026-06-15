---
story: backfill-ops
status: as-is
---

# Operator re-processes historical data via backfill

## Role

As an operator, I can start a backfill on an instance's asset, override which partitions get re-processed, watch per-partition progress, and cancel a running backfill mid-flight, so that I re-process historical data through the live pipeline without bouncing the template.

## Capability

Operator-driven backfill lifecycle: start, partition-override, per-partition progress, cancel, through the control-api or CLI.

## Business value

Operators re-process historical data through the live pipeline without bouncing the template — including overriding which partitions get re-processed and canceling in-flight backfills cleanly.

## Acceptance

Through the control-api or the backfill CLI verb, an operator starts a backfill on an asset with a partition-selector override; the supervisor materializes runs against the overridden selector (not the template default) and drives them to terminal through the real dispatch path; the per-partition progress surface reflects what actually happened; cancelling a running backfill aborts the in-flight partitions through the real supervisor cancel path.

## Falsifier

Override silently dropped (supervisor uses template default), OR cancel is recorded but in-flight partitions keep running, OR the per-partition progress lies about what dispatched.

## Proof

Executable proof.
