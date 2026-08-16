---
assessment: rimsky-deployment-bootstrap--role-selection
subject: story:rimsky-deployment-bootstrap
way: role-selection
release: d977250c
outcome: held
warrant: experiment:rimsky-deployment-bootstrap
---
# Choosing whether the deployment runs as one unit or as separate roles

The audit drove all seven launch forms the shipped image accepts — the four legal ones and three illegal ones — and each behaved as promised. The launch with no command served all three roles from one process. Each launch naming a single role (`catalog:image-commands/rimsky-control-api`, `catalog:image-commands/rimsky-scheduler`, `catalog:image-commands/rimsky-supervisor`) ran that role alone. Every illegal launch — an unknown role name, the migration binary, and two roles named at once — exited non-zero naming the three valid roles, and started nothing. The topology is therefore the operator's to choose, and a mistyped choice fails immediately rather than producing a half-formed deployment. Eighteen checks across this way and its sibling, none failing.

## Unverified remainder

The seven launch forms are the whole population the image accepts, and all seven were driven. The way does not establish behaviour when the same role is launched several times over one deployment.
