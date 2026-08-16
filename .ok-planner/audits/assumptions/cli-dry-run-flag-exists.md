---
assumption: cli-dry-run-flag-exists
commit: d977250c
disposition: trap
synthesized: 2026-08-16T05:48:16Z
---

# because the platform has a first-class dry-run mode resolved per request, the CLI exposes it as a `--dry-run` flag on every write verb.

As operator previewing a change, I would take it that because the platform has a first-class dry-run mode resolved per request, the CLI exposes it as a `--dry-run` flag on every write verb.

## Source

published-concept — `concept:dry-run` ("a per-request dry-run flag", "dry-run covers all write actions uniformly")

## What a run would observe

run `rimsky instance create --dry-run` and any other write verb with `--dry-run` and see whether the flag parses.

## Measured

`.ok-planner/experiments/assumption-cli-dry-run-flag-exists` — built for this
run — asked the parser for `--dry-run`, `--preview`, and `-n` on 31 write
verbs (every mutating verb in `rimsky --help`, across the dev-loop,
literal-API, auth, ctx, compose, and agent families), then booted one
`rimsky-all-in-one` from this tree's image set to check the capability against
the same deployment.

No write verb takes any of the three spellings — 0 of 31 for each, every one
rejected with `flag provided but not defined` and exit 2. The capability is
real and live on that same deployment: `POST
/v1/templates/{id}/deploy?dry_run=true` returned
`{"dry_run":true,"would_have_deployed":{...}}` and left the template
`registered`. So an operator holding the CLI cannot ask for the preview the
platform is serving one layer down, and `rimsky compose plan` — a reconciliation
preview for one family — is the closest thing on offer.

The identity-bound route is open but misreports. A key whose `template:deploy`
grant pins the action to `dry_run` does force the preview, and the run
confirmed the template stayed `registered` — but `rimsky deploy <hash>` exited
0 and printed "<hash> deployed", and the same command under `-o json` printed
`{}`. The CLI discards the synthetic envelope in both renderings, so the one
CLI path to a dry-run tells the operator the write happened. 2 checks, 0 pass,
2 fail.
