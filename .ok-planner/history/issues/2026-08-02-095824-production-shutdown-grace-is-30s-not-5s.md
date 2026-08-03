---
issue: production-shutdown-grace-is-30s-not-5s
kind: audit
category: decision-drift
artifacts:
  - decision:graceful-shutdown
status: promoted
opened: 2026-08-02T09:58:24Z
sprint: 2026-08-03-audit-gap-drain.md
---

# Production shutdown runs a 30-second grace with no second-signal escape; the decision promises 5 seconds and a hard exit

The graceful-shutdown decision commits to one hardcoded five-second grace everywhere, with a second interrupt escalating to immediate hard exit — its Alternatives reject a configurable grace on the grounds that "one conservative hardcoded value serves every deployment" (`decision:graceful-shutdown`). Only the CLI dev-tooling path honors that: the compose-run child shutdown uses a five-second window and a second-signal hard-exit handler. The paths that front actual deployments diverge on both counts: the all-in-one entrypoint and the shared boot path for the three standalone role binaries both hardcode a thirty-second deadline and read exactly one signal — a second Ctrl-C or SIGTERM does nothing (`code:cmd/rimsky-entrypoint/main.go`, `code:cmd/internal/roleboot/roleboot.go`). Separately, the supervisor's own in-flight-dispatch wait is another hardcoded thirty seconds that, on expiry, only logs a warning — it never terminates the dispatch goroutines.

So the decision's "one value everywhere" premise is already false in the tree, and the production paths lack the operator escape hatch the decision considers essential. The thirty-second drain itself is defensible — production dispatches genuinely in flight may need more than five seconds — which is what makes this a judgment call rather than a mechanical fix.

The ruling decides what the shutdown contract actually is, per path.

## Options

- Amend the decision to per-path values (five seconds plus escalation for CLI-spawned children; thirty-second drain for entrypoint, role binaries, and the supervisor's in-flight wait) and add second-signal hard-exit escalation to the production paths. Cost: concedes the one-value rationale was wrong, and the escalation is a small real code change.
- Unify everything on the decision as written: five seconds and hard-kill everywhere, including terminating the supervisor's in-flight dispatches on expiry. Cost: five seconds is a guess that production drains can finish in time; cutting real in-flight dispatches to honor a prose value is production risk with no operator benefit.

## Ruling

> Recommended ruling (/verify-issues): amend the decision to the per-path shape — five-second grace with second-signal hard exit for CLI dev tooling, thirty-second drain for the production entrypoint/role/supervisor paths — and, as the one code change, add second-signal hard-exit escalation to the production paths so an operator can always force an immediate exit.
>
> Rationale: the drain window and the escape hatch are separable concerns; the thirty-second drain reflects a real production need the five-second prose never met, while the missing second-signal handling is a pure operability gap with no defender — keeping the decision's escalation intent and correcting its arithmetic preserves what the decision was actually for. The unify option honors the letter at the cost of cutting live work. Flip case: if the owner values the one-hardcoded-value principle above drain semantics, unify — but on thirty seconds, not five.

<!-- Owner: this is a recommendation, not your decision. Leave it
as-is to accept — the next /plan-sprint carries it, naming the
generated/recommended batches at sign-off. Edit the text to
redirect, empty the section to discuss live, or delete this note
to adopt the ruling as your own. -->
