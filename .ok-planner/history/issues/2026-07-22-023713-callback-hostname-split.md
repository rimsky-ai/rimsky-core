---
issue: callback-hostname-split
kind: human
category: unclear
artifacts:
  - concept:executor
  - concept:supervisor
status: promoted
sprint: 2026-07-25-issue-drain-2026-07-22-batch.md
opened: 2026-07-22T02:37:13Z
---

# A wrong callback hostname makes async job results silently vanish — how much should rimsky guard against it?

When a long-running job finishes, the worker that ran it posts the result back to a URL rimsky handed it at dispatch time. One operator-set config value — the hostname rimsky writes into that URL — decides whether that post can ever arrive. If it's missing or wrong, nothing errors: the system accepts the job, the worker completes it, and the result just never comes back. The worst version of this now fails fast at boot. This issue asks how much of the remaining misconfiguration space to close, and by what mechanism.

The mechanism, briefly. Rimsky's supervisor is the process that hands jobs to worker services ("executors"). Quick jobs reply on the same connection, so they're immune to all of this. Long-running jobs are different: the supervisor includes a callback URL in the dispatch, and the worker posts its outcome there later. The supervisor builds that URL itself — and here's the catch: it listens on `0.0.0.0`, "every interface," which is not an address anyone can dial, so it cannot derive a routable hostname from its own listener. The operator has to supply one (`callback.advertise_host` in the supervisor's config file, or an env var on the supervisor process). Whatever they supply gets baked into every callback URL, correct or not.

What's already handled: if the advertise host is unset *and* the listener binds the wildcard address, the supervisor refuses to start. Three gaps remain:

1. **The quiet fallback.** If the operator binds an explicit address (say, loopback) and leaves the advertise host unset, that bind address silently becomes the advertised hostname. Right for a single-machine dev setup — a mode this repo ships and relies on — and silently wrong everywhere else.
2. **No validation of a set value.** A typo'd or unroutable hostname is accepted as-is; nothing ever checks it.
3. **The failure doesn't say its own name.** A job whose result never arrives is eventually cleaned up as "abandoned" — with no mention that its callback URL was never called. Diagnosing one today means manually correlating supervisor logs, worker logs, and network policy.

## Options

- **Close the fallback**: any unset advertise host refuses to boot. Deterministic, but breaks the zero-config single-machine mode unless that mode starts requiring explicit config too.
- **Startup self-probe** of the advertised URL. Catches typos — but the supervisor reaching itself proves nothing about whether workers on another network can, so it gives false confidence in both directions, and it only runs once.
- **Auto-derive the hostname** from the environment (container hostname, cloud metadata). Trades "operator forgot" for "heuristic guessed wrong" — the same silent failure, relocated — and nothing else in this codebase auto-detects config this way.
- **Make the failure legible**: when an abandoned job is cleaned up, record the callback URL nobody ever called. Prevents nothing, but the failure then names its own cause. Composes with any of the above.

The ruling picks a point on this line: prevention (which of the first three, if any) and/or diagnosability (the fourth) — knowing each prevention option costs either the zero-config path or a new class of wrong guesses.

## Ruling

> Recommended ruling (/recommend-rulings): Work item: link orphan-reap
> diagnostics to the stamped callback URL (a reaped async dispatch
> names the callback URL it never heard from). No further prevention:
> keep the shipped wildcard-bind fail-fast and the explicit-non-
> wildcard fallback; no self-probe, no environment auto-derivation.
>
> Rationale: The wildcard case — the one silent-failure mode with a
> deterministic fix — is already shipped; the remaining prevention
> shapes either burden the zero-config local path or guess unreliably
> (the self-probe's vantage point proves nothing about executors).
> Diagnosability closes the actual cost the Problem names: the
> expensive three-log correlation hunt.

<!-- Owner: this is a recommendation, not your decision. Leave it
as-is to accept — the next /plan-sprint carries it, naming the
recommended batch at sign-off. Edit the text to redirect, empty
the section to discuss live, or delete this note to adopt the
ruling as your own. -->
