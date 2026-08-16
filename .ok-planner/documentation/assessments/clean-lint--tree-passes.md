---
assessment: clean-lint--tree-passes
subject: story:clean-lint
way: tree-passes
release: d977250c
outcome: held
warrant: experiment:clean-lint
---
# The whole tree passes the project's lint with every check active

The audit read the lint the project ships with and the configuration a maintainer runs it under: the configuration switches off no check and declares five citation tags. Run over the repository root under that same configuration, the lint exited clean with no output. The maintainer's question — is the tree clean right now, under full enforcement rather than a reduced set — is therefore answered by one command whose configuration is the same one every contributor's change is held to. Seven checks ran across three legs and none failed.

## Unverified remainder

None: the passing run demonstrates the way as promised.
