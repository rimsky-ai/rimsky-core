---
issue: test-wallclock-verdict-dialect
kind: human
category: determinism
status: verified
opened: 2026-07-22T02:38:08Z
---

# The testing rules ban clock-based verdicts; two hundred tests use them anyway

Rimsky's testing rules ban any test whose pass/fail depends on wall-clock time — no "fail if a timer fires before the background work finishes," no "poll for five seconds then give up," no "sleep, then assert nothing happened." The reasoning: any finite timeout is a guess about machine load, so a correct system can still fail the test on a slow CI box, and every such failure generates review noise. The only sanctioned timeout is the suite-level hang-killer. The problem is that the rule exists only as prose: a structural scan found ~63 timeout-race waits and ~157 deadline-bounded poll loops across 76 files, plus one whole suite built on sleep-then-assert-nothing. Nothing mechanical stops a new violation, so the banned dialect re-enters with every new test.

Complications the ruling has to reckon with: many fixes aren't mechanical — replacing a timed wait with a true synchronization often requires the code under test to expose a signal it doesn't have yet. A widely-used third-party helper (`require.Eventually`, which polls up to a deadline) is ambiguous under the rule as written — its deadline is still a verdict input. And a narrower sibling issue (`services-harness-deadline-in-verdict-idiom`) covers the same tension for one test harness specifically; ruling broadly here effectively pre-decides it, so the two belong in one ruling.

## Options

- **Sweep and gate in one motion**: fix all ~220 sites, add a lint that blocks new ones. Most complete; largest effort, and it churns 76 files against whatever work is in flight.
- **Gate now, drain later**: land the lint with a one-way "count may not increase" baseline over the existing backlog; violations shrink as files get touched anyway. Stops the bleeding immediately at minimal churn.
- **Audit only**: periodically walk the sites and fix the ones masking real ordering bugs — the approach a prior narrower cleanup took. Cheapest; leaves the rule permanently unenforced.
- **Loosen the rule** to sanction generous documented timeouts where a true synchronization point is genuinely hard to build.

The ruling decides: enforce mechanically or keep auditing; if enforcing, sweep-then-gate or gate-then-drain; what the sanctioned suppression looks like for legitimate timed waits; whether `require.Eventually` counts as a violation; and whether the harness sibling rides in the same ruling.

## Ruling

> Recommended ruling (/recommend-rulings): Gate now, drain later: a
> lint forbidding wall-clock verdict idioms in _test.go (fail-on-
> timeout selects, deadline polls — require.Eventually included, its
> deadline is a verdict input) with a budget-ratchet baseline over the
> existing ~100+ sites and a per-site suppression comment for
> legitimate fixture sleeps. Rule issue:services-harness-deadline-in-
> verdict-idiom in the same sprint, consistently.
>
> Rationale: rules.md already retired the dialect — the only open
> question was mechanism. The ratchet (the ok-plumbline:budget
> precedent) stops new instances immediately without churning 76 files
> against in-flight work; the backlog drains as files are touched.

<!-- Owner: this is a recommendation, not your decision. Leave it
as-is to accept — the next /plan-sprint carries it, naming the
recommended batch at sign-off. Edit the text to redirect, empty
the section to discuss live, or delete this note to adopt the
ruling as your own. -->
