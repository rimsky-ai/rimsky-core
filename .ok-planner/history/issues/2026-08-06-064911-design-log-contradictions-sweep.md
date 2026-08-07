---
issue: design-log-contradictions-sweep
kind: audit
category: corpus-hygiene
artifacts:
  - concept:terminal-resolution
  - concept:instance
  - concept:inertness
status: promoted
opened: 2026-08-06T06:49:11Z
sprint: 2026-08-06-ruled-intake-drain.md
---

# Two survivors of the sixteen-item design-log contradiction sweep

A full review of the published concept mirror filed sixteen
contradictions in the concept catalog. Investigation resolved fourteen
of them: eleven were expression-only drift and are repaired in place
in the concept files (both sides already agreed on the commitment;
the wording now matches), and three turned out not to be
contradictions on close reading. Two remain, and both survive for the
same reason: settling them changes what the corpus commits to, which
only a sprint may do.

**First: the kill path keeps a record-only fallback the catalog
half-describes.** When an instance is killed, claims normally resolve
through the durable outbox — a queue that makes producer terminals
land without needing the producer reachable. But the kill path
(`lib/runtime/instance_kill.go`) still carries a genuine fallback:
if the producer isn't in the registry at all, or synchronous
resolution errors, the claim is promoted record-only, bypassing the
outbox so the kill always lands. The instance concept describes the
fallback, the terminal-resolution concept describes only the outbox,
and the code does both. The question is whether the fallback is
committed resilience (document it in both places) or legacy debt (a
work item to unify everything through the outbox).

**Second: message inertness doesn't sanction a read site the code
has.** The inertness concept enumerates where message payload bytes
may be read; receipt-time schema validation reads them too and appears
in no enumeration — the message, message-schema, and inertness
concepts disagree on the count. Reconciling means either extending the
sanctioned-site list (a commitment widened) or declaring the
validation site a violation (code change).

## Options

- Kill fallback: commit to it as resilience and document it in both
  concepts, or schedule unification through the outbox and keep the
  catalog as-is until then.
- Inertness sites: extend the sanctioned enumeration to include
  receipt-time schema validation, or rule the validation read a
  violation and change the code.

The ruling settles both survivors; the other fourteen items are done.

## Ruling

> Recommended ruling (/verify-issues): commit to both code realities.
> Document the kill path's record-only fallback as intended
> resilience — a kill must land even when a producer is unregistered
> or resolution fails synchronously — in both the instance and
> terminal-resolution concepts; and extend the message-inertness
> sanctioned-site enumeration to include receipt-time schema
> validation.
>
> Rationale: in both cases the code's behavior is defensible on its
> own terms (a kill that can hang on an unreachable producer is
> worse than a record-only terminal; schema validation must read the
> payload to validate it), and the corpus is the party that lagged.
> The flip case for each: if the owner regards the record-only
> fallback as debt, the resolution becomes an outbox-unification
> work item instead of a doc change; if receipt-time validation is
> meant to be structurally impossible to misuse, the enumeration
> stays closed and the validator moves behind a sanctioned site.

<!-- Owner: this is a recommendation, not your decision. Leave it
as-is to accept — the next /plan-sprint carries it, naming the
generated/recommended batches at sign-off. Edit the text to
redirect, empty the section to discuss live, or delete this note
to adopt the ruling as your own. -->
