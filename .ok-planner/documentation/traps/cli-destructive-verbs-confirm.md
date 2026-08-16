---
trap: cli-destructive-verbs-confirm
release: d977250c
demonstration: experiment:assumption-cli-destructive-verbs-confirm
---
## Assumption

As operator deleting something, I would take it that destructive verbs (`rm`, `delete`, `revoke`, `undeploy`, `admin reset`, `lineage prune`) prompt for confirmation interactively, and `--yes` is what suppresses the prompt.

craft-convention — a `--yes` flag existing at all implies a prompt it answers

## Actual behavior

the experiment — built for
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
