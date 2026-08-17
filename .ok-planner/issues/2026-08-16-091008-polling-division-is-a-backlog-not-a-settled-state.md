---
issue: polling-division-is-a-backlog-not-a-settled-state
kind: audit
category: conflicting
artifacts:
  - decision:polling-audit
status: verified
opened: 2026-08-16T09:10:08Z
---

# The polling decision describes a finished division of test waits that is still a backlog

A decision says the test suites divide their waits in two. A wait on an outcome that will happen may poll. A wait on a transient in-flight state must block on the event tail, the log's own "this happened" signal. The event-tail helper exists and six scenario files use it. But the wall-clock ratchet records 234 of them across 115 files. That ratchet is the project's own count of remaining wall-clock verdict idioms. Some of those 234 are deadline-polls on transient states whose pass depends on catching an ordering window. The sub-graph delegation suite is a clear case. The decision supplies no marker, lint or population that separates its two classes, so nothing checks the division. Its sibling, the ratchet decision, treats the backlog as work in progress. The corpus therefore contradicts itself on whether the division is settled. The ruling decides how the polling decision states the truth and whether the division becomes checkable.

A poll around an ordering-dependent assertion can sample past the transition it means to catch. That is the load-dependent verdict the project's testing rules forbid.

## Options

- Restate the decision as a standing rule plus a named backlog still to convert; cost: converts nothing, but stops claiming a finished state.
- Add a marker or lint that separates outcome-polls from transient-state waits, so a new ordering-dependent poll fails loudly; cost: real tooling work, and it is the only option that makes the population enumerable.
- Convert the ordering-dependent subset of the 234 idioms now and retire the ratchet; cost: nothing classifies which of the 234 are ordering-dependent, so the work carries the scope judgment inside it.

The ruling decides whether the division is a rule with a backlog or a checkable property.

## Ruling

> Recommended ruling (/verify-issues): Restate the decision as a rule with a named backlog. In the same sprint, make the division mechanical with a lint or marker that classifies each wait, so an ordering-dependent poll fails review, then work the backlog down under the existing ratchet.
>
> Rationale: the project pins every other universal with a check, because where prose and lint disagree, lint wins. A division no tool can see will drift again after conversion. The ratchet already carries the count, so the marker gives it a class to count against. Flip case: if every remaining idiom in the ratchet turns out to be an outcome-poll once classified, the second option collapses to the first and no conversion sprint is needed.

<!-- Owner: this is a recommendation, not your decision. Leave it as-is to accept — the next /plan-sprint carries it, naming the generated/recommended batches at sign-off. Edit the text to redirect, empty the section to discuss live, or delete this note to adopt the ruling as your own. -->
