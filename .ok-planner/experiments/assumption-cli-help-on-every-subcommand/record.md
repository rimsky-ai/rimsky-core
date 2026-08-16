---
experiment: assumption-cli-help-on-every-subcommand
commit: d977250c
---

# `--help` on every node of the verb tree

## What it ran against

The CLI built from this tree, with no server — help never dials. The
population is the 89 nodes reachable from `rimsky --help`: the root, 14 family
nodes, and 74 leaves across the dev-loop, literal-API, auth, ctx, compose,
agent, and conformance families. Two claims are checked at each node: it
prints that node's own usage, and it exits zero.

## What was observed

73 of the 89 nodes exit non-zero on `--help`. The pattern is clean: the root
and the 14 family nodes exit 0, and every leaf exits 2 while printing its
usage to stderr. So `rimsky instance delete --help` and `rimsky conformance
executor --help` both print exactly what the operator wanted and then report
failure.

8 nodes print another node's usage. Six are the dev-loop shortcuts naming the
grouped verb they dispatch to — `instantiate` prints "Usage of instance
create", `rm-instance` prints "Usage of instance delete", `logs` prints "Usage
of instance events", and the three `ls` forms print their grouped list verbs.
`rimsky ls --help` is the sharpest: with no sub-argument it prints "Usage of
instance list", which is also what plain `rimsky ls` does. The eighth is
`rimsky version --help`, which ignores `--help` and prints the version.
2 checks, 0 pass, 2 fail.
