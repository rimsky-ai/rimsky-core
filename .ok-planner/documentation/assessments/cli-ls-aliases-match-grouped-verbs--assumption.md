---
assessment: cli-ls-aliases-match-grouped-verbs--assumption
subject: assumption:cli-ls-aliases-match-grouped-verbs
way: assumption
release: d977250c
outcome: held
warrant: experiment:assumption-cli-ls-aliases-match-grouped-verbs
---
# `rimsky ls templates` returns exactly what `rimsky template list` returns, and `rimsky deploy` / `rimsky undeploy` / `rimsky instantiate` / `rimsky rm-instance` are pure aliases of their grouped forms with identical flags and output.

As operator using the CLI, I would take it that `rimsky ls templates` returns exactly what `rimsky template list` returns, and `rimsky deploy` / `rimsky undeploy` / `rimsky instantiate` / `rimsky rm-instance` are pure aliases of their grouped forms with identical flags and output.

sibling-symmetry — top-level shortcut verbs coexisting with `rimsky template list` / `rimsky instance create` in `cli-verbs`

## What the audit ran and observed

The experiment — built
for this run — drove both spellings of all five named pairs against one
`rimsky-all-in-one` from this tree's image set and diffed what came back: the
flag set each verb reports under `-h`, the success output in human and
`-o json` form, and the error output and exit code on a missing reference and
on an undefined flag. Write pairs, whose first spelling consumes its subject,
were run over two interchangeable subjects with hashes, instance ids, and
timestamps normalized away. All five pairs are indistinguishable — same flags,
same bytes, same exit codes — and each shortcut's own usage text names the
grouped verb it dispatches to (`rimsky ls templates -h` prints "Usage of
template list"). `ls instances` ~ `instance list` and `ls tags` ~ `tag list`
match too. 19 checks, 19 pass.

## Unverified remainder

None: the passing run demonstrates the prior as stated.
