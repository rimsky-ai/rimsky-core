---
issue: terminal-event-precedes-producer-ack
kind: human
category: inconsistent
artifacts:
  - concept:claim-producer
  - concept:terminal-resolution
  - concept:event-log
status: promoted
sprint: 2026-08-08-ruled-intake-drain.md
opened: 2026-08-08T05:02:02Z
---

# The terminal event is written before the producer is told anything

When a node finishes and its claim settles, two things happen: rimsky tells the
claim's producer how it went — commit on success, abandon on failure — and rimsky
writes a forensic event recording the settlement.

The order is: write the event, then tell the producer. Both the outbox row that
will carry the verb and the forensic event are written inside the same
transaction, and the actual call to the producer goes out afterwards, from the
dispatcher, once that transaction has committed
(`lib/runtime/terminal_decision.go:127-131`).

So an observer reading the event log sees a settled claim before the producer has
heard about it, and if the producer never acknowledges — it is down, it rejects
the verb — the event still stands. The event is a record of rimsky's decision,
not of the producer's agreement.

## Why it surfaced, and why it is a question rather than a bug

The examples module's claim-producer README asserted the opposite: that rimsky
emits the completion event only after the producer acknowledges, and it named a
specific check as the falsifier that would catch a violation. That check runs
inside the same transaction, before any call reaches the producer, so it cannot
fail regardless of what the producer does.

The README is being deleted with the rest of the module, and a false claim in a
deleted file needs no fix. What survives is that the guarantee a reader
reasonably expected is not one the corpus states either way. `concept:claim-producer`
and `concept:terminal-resolution` describe the verbs and the settlement, and
neither says what the forensic event means with respect to producer
acknowledgement. Whichever answer is right, it is currently unwritten and
untested.

## Options

- **Say the event records rimsky's decision, not the producer's agreement**, and
  write the invariant down. Matches the code exactly, keeps the settlement path
  in one transaction, and makes the outbox's at-least-once delivery the thing that
  guarantees the producer eventually hears. The cost is that a consumer treating
  the event log as proof of producer state is wrong, and the corpus must say so
  plainly rather than leaving it to be inferred.
- **Move the event after acknowledgement.** The event log then means what a reader
  assumed. The cost is real: the settlement stops being one atomic transaction,
  and a producer that is slow or unreachable delays or blocks the record of a
  decision rimsky has already made.

The ruling decides what a terminal event asserts about the producer.

## Ruling

> State that the terminal event records
> rimsky's settlement decision, not the producer's acknowledgement, and add the
> invariant to the claim-producer concept so the question stops being inferable
> from code alone. Prove it with a test that a settlement whose producer verb has
> not yet been delivered still emits the event.
>
> Rationale: the current shape is not an accident — settling in one transaction
> and delivering the verb through an outbox is the pattern rimsky uses elsewhere,
> and it is what keeps a slow or unreachable peer from blocking a decision the
> orchestrator has already taken. Reordering to satisfy the intuitive reading
> trades that away for a guarantee nobody has asked for outside a deleted README.
> The real defect is silence: the corpus lets a reader assume either answer.
>
> One thing to check while drafting: whether anything downstream already treats a
> terminal event as evidence that the producer's state changed — an audit read, a
> reconciliation path, an external consumer of the event log. Writing the weaker
> meaning down is only safe if nothing is leaning on the stronger one.
