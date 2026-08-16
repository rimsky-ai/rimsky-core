---
trap: cli-destructive-verbs-confirm
release: d977250c
---
# Evidence set — destructive verbs (`rm`, `delete`, `revoke`, `undeploy`, `admin reset`, `lineage prune`) prompt for confirmation interactively, and `--yes` is what suppresses the prompt.

Source of the prior: craft-convention — a `--yes` flag existing at all implies a prompt it answers

## What the audit ran and observed (assumption record)

`.ok-planner/experiments/assumption-cli-destructive-verbs-confirm` — built for
this run — seeded a `rimsky-all-in-one` from this tree's image set with a
template, two instances, a node, a tag, and a spare api-key, then ran each of
11 destructive verbs on a real pty with `n` already waiting on stdin.

None of the 11 asked anything. Seven destroyed their subject on the spot with
the operator's `n` sitting unread: `tag rm`, `instance kill --force`,
`rm-instance`, `instance delete`, `undeploy`, `template rm`, `auth revoke`.
`admin reset`, `lineage prune`, and `asset delete` were equally silent, going
straight to the request. The one verb that stops is `instance kill` without
`--force`, and it stops by refusing — "refusing to terminate without --force",
exit 2 — not by asking, so there is nothing for an operator to answer.

The control proves the probe: `compose down`, run the same way, printed the
three destructive operations it had scheduled, asked `Proceed? [y/N]`, read
the `n`, and exited 2 having changed nothing. The CLI knows how to prompt; it
does it in the compose family and nowhere else.

The prior's second half inverts too. `--yes` is accepted by every destructive
verb as a common flag and suppresses no prompt anywhere, because there is no
prompt to suppress. Its only effects are on `instance kill`, where it stands
in for `--force` and so *enables* a destruction rather than confirming one,
and in the compose family, where it answers the real prompt. 11 checks, 0
pass, 11 fail.

## Experiment record (experiment:assumption-cli-destructive-verbs-confirm)

# Whether the CLI's destructive verbs ask before they mutate

## What it ran against

One `rimsky-all-in-one` container from this tree's image set on a free port,
seeded through the CLI with a template, two instances, a node, a tag, and a
spare api-key. The population is 11 destructive verbs: `tag rm`, `instance
kill` with and without `--force`, `rm-instance`, `instance delete`, `admin
reset`, `undeploy`, `template rm`, `auth revoke`, `lineage prune`, `asset
delete`.

Every verb runs on a real pty — so `isatty` is true — with `n` already waiting
on stdin, the answer an operator gives when they change their mind. After each
one the probe re-reads the subject through the CLI and reports whether it
survived. `compose down` runs first as the control, because it is known to
prompt; it runs before `auth init` because the compose family sends no
api-key and cannot reach an authenticated deployment. A verb whose subject the
probe cannot construct (`admin reset`, `lineage prune`, `asset delete`) is
reported prompt-only: the prompt question is still answered, since a prompt
would come before the request.

## What was observed

The control prompted: `compose down` printed the three scheduled destructive
operations, asked `Proceed? [y/N]`, read the `n`, and exited 2 without
touching anything.

None of the 11 destructive verbs asked anything. Seven destroyed their subject
on the spot with an operator's `n` sitting unread on stdin: `tag rm`,
`instance kill --force`, `rm-instance`, `instance delete`, `undeploy`,
`template rm`, `auth revoke`. `instance kill` without `--force` is the one
verb that stops, and it stops by refusing — "refusing to terminate without
--force", exit 2 — not by asking.

`--yes` is accepted by every one of them as a common flag and changes nothing
on any of them. The only places it does anything are `instance kill`, where it
stands in for `--force`, and the compose family, where it answers the real
prompt. 11 checks, 0 pass, 11 fail.

Runnables: `src:.ok-planner/experiments/assumption-cli-destructive-verbs-confirm/` at the stamped commit.
