---
experiment: assumption-cli-ls-aliases-match-grouped-verbs
commit: d977250c
---

# The dev-loop shortcut verbs against their grouped forms

## What it ran against

The CLI built from this tree, driving one `rimsky-all-in-one` container from
this tree's image set on a free port. The population is the five pairs the
assumption names: `ls templates` ~ `template list`, `deploy` ~ `template
deploy`, `undeploy` ~ `template undeploy`, `instantiate` ~ `instance create`,
`rm-instance` ~ `instance delete`. `ls instances` ~ `instance list` and `ls
tags` ~ `tag list` are measured alongside as context.

Each pair is compared three ways: the flag set each verb reports for itself
under `-h`, the output of a success path, and the output of an error path
(unknown reference, unknown flag). Read pairs are run twice over the same
world and diffed byte for byte in both output formats. Write pairs cannot be —
the first spelling consumes its subject — so the run seeds two interchangeable
subjects (two templates differing only in name, two instances of one template)
and diffs the two spellings with hashes, instance ids, and timestamps
normalized away. Exit codes are compared unnormalized.

## What was observed

All five named pairs are indistinguishable, and so are the two context pairs.
Each pair reports the same flag set, and each shortcut's usage text names the
grouped verb it dispatches to (`rimsky ls templates -h` prints "Usage of
template list"). Success output matched byte for byte in human and `-o json`
form; error output and exit code matched on a missing template hash (exit 1),
an unknown instance id (exit 1), and an undefined flag (exit 2). 19 checks, 19
pass.
