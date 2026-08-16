---
assessment: rimsky-health-check--persistence-down
subject: story:rimsky-health-check
way: persistence-down
release: d977250c
outcome: held
warrant: experiment:rimsky-health-check
---
# The health answer tracks persistence rather than a happy path

A deployment backed by a database container was driven through all three states of that dependency. While the database was up, `catalog:http-routes/GET /v1/health` answered success. Once the database container was stopped, the probe answered non-success naming the failed transaction, and `catalog:cli-verbs/rimsky health` exited non-zero reporting that status. After the database was started again, the probe answered success once more, so the signal recovers rather than latching. Every answer was taken twice over, through the route and through the CLI verb, whose exit code tracked it — which is what lets an operator gate traffic on a real health signal instead of a silently degraded one.

## Unverified remainder

Persistence was failed by stopping the database container outright. The way does not establish what the probe reports under partial degradation, such as a reachable but slow or read-only database.
