---
assessment: portable-template-across-modes--same-file-both-modes
subject: story:portable-template-across-modes
way: same-file-both-modes
release: d977250c
outcome: held
warrant: experiment:portable-template-across-modes
---
# One template file run unedited against both deployment modes

The audit ran a single template file against two deployments with nothing edited between them. The first was an all-in-one deployment (`catalog:images/rimsky-all-in-one`) on its baked zero-config defaults; the second was a genuinely split deployment — a database container, a separate bundled-executor container, and three role containers whose commands name `catalog:image-commands/rimsky-control-api`, `catalog:image-commands/rimsky-scheduler` and `catalog:image-commands/rimsky-supervisor` against a shared configuration. On each, the file registered, deployed, instantiated, reached a terminal state, and left every node with one fresh run. Two things rule out a false pass: the file's own hash was taken before and after and was unchanged, so no step rewrote it for the second mode, and both deployments content-addressed it to the same template hash, so each accepted the identical bytes rather than a normalisation of its own. There is therefore no dev-versus-prod template dialect. Nine checks, none failing.

## Unverified remainder

One template, naming one bundled executor with an inline dataset, was promoted between the two modes. The way does not establish portability for templates that reach outside the deployment or that name third-party peers.
