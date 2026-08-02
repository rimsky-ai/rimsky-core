---
audit: release-distribution
artifact: decision:release-distribution
determination: unsupported
commit: 3918d24e
audited: 2026-08-02T09:58:10Z
issue: 2026-08-02-095816-cli-archive-release-channel-untested
---

# Distribution channels

Unsupported for one of the four named channels. Checked all four: container images, the protocols npm package, and Go modules consumed from a full checkout are each real, structurally load-bearing, and covered by the project's release-chain regression tests. The fourth, prebuilt CLI archives via a release-archive builder, matches the decision's specifics on inspection — correct target platforms, per-archive software bills of materials — but carries zero automated verification anywhere: no test in the project's suites exercises it, and neither continuous-integration workflow invokes it. One member of the claimed four-channel enumeration is entirely unguarded.
