---
issue: services-harness-deadline-in-verdict-idiom
kind: audit
category: inconsistent
artifacts:
  - decision:testing-scenario-based-e2e
status: promoted
sprint: 2026-07-25-issue-drain-2026-07-22-batch.md
opened: 2026-07-25T03:18:31Z
---

# The integration-test harness is built on a pattern the project's own rules forbid

Rimsky's testing rules ban wall-clock timeouts from ever deciding whether a test passes: a test should wait, unbounded, for the event it needs, with only the top-level suite timeout (which kills a hung run and reports it broken) allowed to be time-based. The stated reasoning is that any chosen number of seconds is an arbitrary guess about machine speed — "why 30 and not 29?" has no answer — so a deadline that fails a test is a load-dependent coin flip, not a verdict. But the integration-test harness for the bundled services works the opposite way: its poll helpers ("did this node finish," "is this subscription active," "is the service healthy") each take an explicit time limit, commonly 90 or 180 seconds, and directly fail the test when the limit hits first. That's the forbidden pattern, and it isn't a stray — it's the established idiom the whole suite is built on, faithfully copied by every new test.

The design corpus doesn't resolve it: the doc defining this suite is silent on wait-idiom, and the nearest decision says deadline-polling tests "should be audited" without saying whether a bounded loop that fails on timeout counts as a legitimate poll. One practical wrinkle cuts the other way: today's bounded helpers fail with a specific "still waiting for X" message; a naive unbounded loop killed by the suite timeout fails with a generic stack dump unless the helpers are restructured to report their expected state on the way down.

## Options

- **Sweep the helpers to poll-until-success** — unbounded loops, suite timeout as the only backstop, restructured to keep a descriptive message on the timeout exit. A real sweep touching every helper and call site.
- **Write an explicit exception into the rules** for this class of real-infrastructure test — today's code stands; the corpus's strongest testing commitment gets a carve-out for its largest violation.

A separately filed issue covers the same question repo-wide; the ruling here should stay consistent with that one.

## Ruling

> Recommended ruling (/recommend-rulings): Sweep the services-harness
> poll helpers to poll-until-success (keeping a descriptive expected-
> state message on the suite-timeout exit path), consistent with
> rules.md as written. Execute in the same sprint as issue:test-
> wallclock-verdict-dialect's gate so the harness doesn't seed the
> ratchet baseline.
>
> Rationale: rules.md's own 'why 29 and not 30' argument is the
> project's stated position — an exception for the harness would amend
> the corpus's strongest testing commitment to preserve its largest
> violation.

<!-- Owner: this is a recommendation, not your decision. Leave it
as-is to accept — the next /plan-sprint carries it, naming the
recommended batch at sign-off. Edit the text to redirect, empty
the section to discuss live, or delete this note to adopt the
ruling as your own. -->
