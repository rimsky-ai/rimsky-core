---
issue: story-single-process-all-in-one-population-under-satisfied
kind: audit
category: proof
artifacts:
  - story:single-process-all-in-one
status: verified
opened: 2026-07-24T00:00:00Z
---

# A "missing" test turned out to exist — the whole issue reduces to one missing tag

Rimsky's design corpus backs each user promise ("story") with named proofs — tests carrying a matching tag in code so tooling can confirm the link. The story about all-in-one mode (one Docker image running all of rimsky's server roles in a single process, for simple local setups) names two tests in its proof: one asserting a single process really serves all three roles and that a large stored payload written by one role is readable by another; and one asserting a bundled in-process executor handles work with zero external services configured. A coverage check found only the second test tagged in code, making half the promised coverage look absent — the classic "the docs claim more than the code delivers" failure the check exists to catch.

Except re-verification found the first test *does* exist and does exactly what the proof describes — the process check, the storage spill, the cross-role read. It simply never received its tag, so the tag-based search couldn't see it. Both originally filed remedies are therefore moot: "write the missing test" targets a test that's already written, and "narrow the story to what's tested" would wrongly understate real coverage on the basis of a bookkeeping gap. What remains is a one-line code-side repair: add the tag.

## Options

- **Retire the issue, with the tag-add as a repair** in the next executing sprint — the population the proof names is satisfied the moment the tag lands.
- **Independently re-verify first** that the existing test truly covers every element the proof claims, before trusting this reading — belt-and-braces at the cost of one more review.

The ruling decides only whether the tag-add closes it outright or gets the double-check first.

## Ruling

> Recommended ruling (/recommend-rulings): retire: the premise rotted
> — both enumerated proof members now exist; the only gap is the
> missing @story: single-process-all-in-one annotation on
> single_process_allinone_test.go, a one-line code-side repair to make
> in the executing sprint.
>
> Rationale: Both filed candidates assumed the test didn't exist; with
> it written, the Proof field's population is satisfied the moment the
> annotation lands.

<!-- Owner: this is a recommendation, not your decision. Leave it
as-is to accept — the next /plan-sprint carries it, naming the
recommended batch at sign-off. Edit the text to redirect, empty
the section to discuss live, or delete this note to adopt the
ruling as your own. -->
