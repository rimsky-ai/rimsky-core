---
assumption: cli-help-on-every-subcommand
commit: d977250c
disposition: trap
synthesized: 2026-08-16T05:48:16Z
---

# `--help` on any node of the verb tree (`rimsky asset --help`, `rimsky conformance executor --help`) prints that node's own usage with its own flags and exits zero.

As new operator, I would take it that `--help` on any node of the verb tree (`rimsky asset --help`, `rimsky conformance executor --help`) prints that node's own usage with its own flags and exits zero.

## Source

craft-convention — a nested subcommand tree with `rimsky help` and `--help` present

## What a run would observe

walk every verb and subverb with `--help`, checking exit code and that the flags printed are the ones that verb accepts.

## Measured

`.ok-planner/experiments/assumption-cli-help-on-every-subcommand` — built for
this run — walked all 89 nodes of the verb tree with `--help`: the root, 14
family nodes, and 74 leaves.

73 of the 89 exit non-zero. The split is clean: the root and the 14 family
nodes (`rimsky template --help`, `rimsky auth --help`, …) exit 0, and every
leaf prints its usage to stderr and exits 2. `rimsky asset --help` and `rimsky
conformance executor --help`, the two the prior names, land on opposite sides
of that line for no reason an operator can see — the first exits 0 because it
is a family node, the second exits 2 because it is a leaf. A script that
treats a non-zero `--help` as "no such verb" is wrong about 73 verbs, and any
`set -e` wrapper around a help call fails.

The first half of the prior mostly holds: each node prints a real usage block
with the flags that verb accepts. Eight do not print their own. Six are the
dev-loop shortcuts naming the grouped verb they dispatch to — `instantiate`
prints "Usage of instance create", `rm-instance` prints "Usage of instance
delete", `logs` prints "Usage of instance events", and the three `ls` forms
print their grouped list verbs — so the operator reading `rimsky rm-instance
--help` is told about a command they did not type. `rimsky ls --help` prints
"Usage of instance list", which silently reveals that bare `ls` means
instances. The eighth is `rimsky version --help`, which ignores `--help` and
prints the version string. 2 checks, 0 pass, 2 fail.
