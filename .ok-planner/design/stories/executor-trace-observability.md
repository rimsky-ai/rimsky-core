---
story: executor-trace-observability
status: as-is
---

# Operator queries/streams executor traces

## Story

As an operator running a dashboard against a rimsky deployment, I can fetch the structured trace records for a completed dispatch and stream live trace events while a dispatch is in flight, so that I see what an executor is doing in real time and after the fact.

Executor-trace observability surface: live trace-stream subscription during a dispatch; structured trace history after the dispatch terminates. Available for executors that advertise trace support.

Operators see what an executor is doing in real time and after the fact — for debugging, performance investigation, and operational awareness.
