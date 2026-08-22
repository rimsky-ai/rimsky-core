---
issue: structured-log-kind-case-convention
kind: audit
category: design-convention
artifacts:
  - concept:event-log
  - decision:event-log-kind-enum
status: verified
opened: 2026-08-21T23:55:20Z
---

# Which channel does the events standard's kind format govern?

Rimsky emits structured events on two channels, and the ambient events standard's naming rule (`SUBSYSTEM.NOUN.VERB`, upper-case segments) matches neither. The event log's kinds are a closed lower-snake enum, ruled as a recorded departure (`decision:event-log-kind-enum`). The structured process log's kinds — 107 distinct at 118 sites, all product code — are lower-case dotted string literals declared at the emit site, exactly the standard's mechanism, in nobody's ruled case: no corpus artifact governs slog kind naming at all. The standard's own scan treats a literal without an upper-case segment as not kind-shaped, so over this tree it reports zero kinds and zero violations — which, as the standard itself says, settles nothing about conformance.

Eight literals do carry a capitalised segment: five name a Go function for error context, three are kinds with a camel-case middle segment. Whether a literal naming a Go symbol is a kind at all belongs to the same ruling.

History: a certification round adopted the upper form for eleven new kinds, and the gate's architect reverted it — a partial adoption is the split the uniformity rule forbids, and the upper form was undeclared in the corpus. The revert restored one uniform lower-case idiom and left this question open. Renaming touches no wire contract: the slog kinds are disjoint from the event-log enum, and signal type paths are slash-separated.

## Options

- **Adopt the standard's upper form for the structured log.** Sweep all 107 kinds in one change and add a check so the lower dialect cannot return; the `/events` inventory then sees the channel. Cost: a 118-site rename with ten test literals, authorized by a ruling rather than a review round.
- **Ratify the lower dotted form as this project's structured-log convention.** Zero code change; the event log already departs by decision, and this records the second channel's departure so the question stops recurring. Cost: the `/events` inventory stays blind to the channel.
- **Hold both channels outside the standard's naming rule and write rimsky's own convention per channel.** Same zero code cost, but the stronger claim that the ambient standard's format binds nothing here.

The ruling decides which channel, if either, the events standard's kind format governs in rimsky.

## Ruling

> Recommended ruling (/verify-issues): ratify the lower-case dotted
> form as rimsky's structured-log kind convention — a recorded
> departure beside the event log's — and settle the eight
> capitalised literals with it (Go-symbol error contexts are not
> kinds; the three camel-case kinds are renamed into the convention).
>
> Rationale: the tree is already uniform in the lower form across
> both channels, both departures then share one recorded rationale,
> and the sweep option buys only `/events` inventory coverage at the
> price of a 118-site rename; the standard itself leaves format to
> the project once the departure is declared. Flip case: if the owner
> wants `/events` to inventory the structured log — kinds enumerable,
> orphans and near-duplicates surfaced mechanically — the upper-form
> sweep with its guard check is the only option that delivers it.

<!-- Owner: this is a recommendation, not your decision. Leave it
as-is to accept — the next /plan-sprint carries it, naming the
generated/recommended batches at sign-off. Edit the text to
redirect, empty the section to discuss live, or delete this note
to adopt the ruling as your own. -->
