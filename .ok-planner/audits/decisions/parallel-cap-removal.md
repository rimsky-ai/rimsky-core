---
audit: parallel-cap-removal
artifact: decision:parallel-cap-removal
text: compliant
implementation: supported
commit: d977250c
audited: 2026-08-16T05:21:13Z
---

# Test-parallelism caps exist only where docker-daemon saturation bounds them

Supported by reading the build orchestration and the CI workflow. The repo's single build-orchestration file carries ten go-test invocations across eight targets; six of them declare a concurrency cap, and every one of those six covers a slice that boots Postgres or full rimsky stacks against the one docker daemon — the root and services module targets, the foundation module target, the plain root target, the smoke target, and the containerised contributor variant. The cap-bearing region of that file opens with a comment naming the docker daemon as the contention and citing this decision, and the services target carries a second comment of its own describing the observed saturation. The two uncapped invocations are the control: the protocols module, which boots no containers, and the single-package in-stack driver target, which holds exactly one test and so has no concurrency to bound. The CI workflow declares no caps of its own — its four-way module matrix invokes the same per-module targets — so the build file is the whole population. Nothing stands in for synchronization: the services test harness exposes a wait that polls per-subscription state until every subscription reports active, which is what the stack suites block on. No test or annotation pins the policy mechanically, so the verdict is a reading of the build configuration as it stands.
