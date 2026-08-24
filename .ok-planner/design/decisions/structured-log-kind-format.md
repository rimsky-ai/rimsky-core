---
decision: structured-log-kind-format
---

# Structured process-log kinds follow the events standard's format

## Choice

Every structured process-log emit site names its kind as a raw string literal in the form `SUBSYSTEM.NOUN.VERB`, upper-case dotted segments, declared at the site and nowhere else. Prose lives in a field, never in the kind. A lint over the tree fails on a literal at a process-log emit site that does not match the form, with an empty baseline, so the lower-case and prose dialects cannot return. The event log is a different system — a durable product surface whose kinds are a closed enum under `decision:event-log-kind-enum` — and this decision does not reach it.

## Rationale

The events standard governs every log that represents code flow for debugging, and the structured process log is that channel; its inventory sees only kinds in the standard's form. A partial adoption is the two-dialect split the one-idiom-per-job rule forbids, and the certification gate's architect reverted an earlier round's partial rename for exactly that reason, so the sweep is whole or not at all. The event log serves a different purpose and already records its own departure.

## Alternatives

- Ratify the lower-case dotted form as this project's process-log convention — rejected: the inventory stays blind to the channel, and the project would carry a second departure from the standard for no gain.
- Rename the dotted kinds and leave prose messages as they are — rejected: two dialects on one channel.
- Hold both channels outside the standard's naming rule — rejected: the process log is precisely the channel the standard is for.
