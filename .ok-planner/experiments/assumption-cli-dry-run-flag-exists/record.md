---
experiment: assumption-cli-dry-run-flag-exists
commit: d977250c
---

# A dry-run flag on the CLI's write verbs

## What it ran against

The CLI built from this tree and one `rimsky-all-in-one` container from this
tree's image set on a free port. Stage 1 asks the parser alone over 31 write
verbs — every mutating verb in `rimsky --help` across the dev-loop,
literal-API, auth, ctx, compose, and agent families — for three spellings:
`--dry-run`, `--preview`, `-n`. The compose family stops parsing at its first
positional, so its probe puts the flag ahead of the manifest. Stage 2 registers
a template through the CLI and asks the control API's own route for the same
preview, so the CLI's silence is measured against a working capability. Stage 3
mints a key whose `template:deploy` grant pins the action to `dry_run` and
drives `rimsky deploy` with it, since that is the one route to a preview the
CLI does leave open.

## What was observed

No write verb takes any of the three spellings: `--dry-run` is accepted by 0 of
31, `--preview` by 0 of 31, `-n` by 0 of 31, each rejected with "flag provided
but not defined" and exit 2. The capability is live on the same deployment —
`POST /v1/templates/{id}/deploy?dry_run=true` returned
`{"dry_run":true,"would_have_deployed":{...}}` and left the template
`registered`. The identity-bound route works but is silent: with a dry-run-mode
key, `rimsky deploy <hash>` exited 0 and printed "<hash> deployed" while the
template stayed `registered`, and the same command under `-o json` printed
`{}` — the CLI discards the synthetic envelope in both renderings. 2 checks,
0 pass, 2 fail.
