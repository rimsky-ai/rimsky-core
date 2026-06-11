---
story: executor-trace-observability
status: as-is
---

# Operator queries/streams executor traces

## Role

As an operator running a dashboard against a rimsky deployment, I can fetch the structured trace records for a completed dispatch and stream live trace events while a dispatch is in flight, so that I see what an executor is doing in real time and after the fact.

## Capability

Executor-trace observability surface: live trace-stream subscription during a dispatch; structured trace history after the dispatch terminates. Available for executors that advertise trace support.

## Business value

Operators see what an executor is doing in real time and after the fact — for debugging, performance investigation, and operational awareness.

## Acceptance

With an executor advertising trace support and a node in flight, the operator's dashboard subscribes to the executor's trace stream through the observability surface and sees structured trace events as the executor emits them; after the dispatch terminates, the operator queries the trace history and receives the full record.

## Falsifier

Trace stream silently drops events under load, OR trace history returns rows that don't correspond to what the executor actually emitted, OR the trace surface is absent for an executor that advertised trace support.

## Proof

Executable proof.
