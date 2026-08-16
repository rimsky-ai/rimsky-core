---
assessment: compose-lifecycle--status
subject: story:compose-lifecycle
way: status
release: d977250c
outcome: held
warrant: experiment:compose-lifecycle
---
# Comparing the manifest against what the deployment actually holds

`catalog:cli-verbs/rimsky compose status` reported every declared resource as missing from the deployment before the manifest was applied, and as present in the manifest afterwards. The verb answers the drift question in both directions — what the manifest declares that the deployment lacks, and what it has — so an operator can check a deployment against its declaration without applying anything.

## Unverified remainder

The run compared a manifest against a deployment that either lacked all of its resources or held all of them; it does not establish how status renders a deployment that has drifted partway.
