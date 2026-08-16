---
issue: verbose-flag-inert-and-progress-axes-do-not-compose
kind: audit
category: conflicting
artifacts:
  - decision:progress-flags
status: verified
opened: 2026-08-16T08:48:08Z
---

# The compose run's progress flags do not do what their decision says

The progress-flags decision describes two orthogonal axes for the compose one-shot verb — volume (quiet, default, verbose) and format (human, JSON) — that compose, so an operator can ask for quiet JSON in CI. In the code the printer is chosen by precedence: JSON first (so quiet has no effect under JSON), then quiet, then verbose; and verbose is byte-for-byte identical to default because the only method it overrides — a frame-tick emitter — is called from no production site, while its help text promises frame ticks and claim events that no printer method emits. The ruling decides whether to build the composable behaviour, freeze the current shape as the spec, or retire the flag.

## Options

- Make selection a real product of the two axes, give the frame-tick emitter a call site, and either build or drop the claim-events promise; cost: real work on the printer.
- Restate the decision as three exclusive modes with fixed precedence, matching the code; cost: gives up quiet JSON, the CI shape the decision was written for.
- Retire verbose and its emitter and reduce volume to two levels; cost: removes a flag operators may already pass.

The ruling decides what the flags mean.

## Ruling

> Recommended ruling (/verify-issues): Build it — printer selection becomes volume × format, the frame-tick emitter gets its call site in the poll loop, and the claim-events promise is dropped from the help text unless it ships in the same change.
>
> Rationale: quiet JSON is the one combination a CI pipeline actually wants and the decision names it; freezing precedence would document away the reason the flags exist, and retiring verbose leaves the CI shape unmet too. Flip case: if nobody drives the compose verb from CI, the second option is honest and cheap.

<!-- Owner: this is a recommendation, not your decision. Leave it as-is to accept — the next /plan-sprint carries it, naming the generated/recommended batches at sign-off. Edit the text to redirect, empty the section to discuss live, or delete this note to adopt the ruling as your own. -->
