---
audit: parallel-cap-removal
artifact: decision:parallel-cap-removal
determination: supported
commit: b767a27d
audited: 2026-08-02T09:34:15Z
---

# No test-parallelism cap stands in for synchronization; the one admitted cap names its docker-daemon contention

Supported. Checked all 5 per-module test targets in the root `Makefile` (`test-root`, `test-foundation`, `test-protocols`, `test-services`, `test-examples`, the population backing `test-all`): three (root, foundation, protocols) invoke `go test` with only `-timeout`/`-race`/`-count`, no `-p`/`-parallel` cap. The remaining two, `test-services` and `test-examples`, are the only ones carrying `-p 2 -parallel 4`, each with an adjacent comment naming the exact contention — concurrent docker-stack boots saturating the docker daemon (observed as wait-strategy stalls and control-API timeouts) — matching the decision's "named contention" requirement. A second comment block at the top of the multi-module section states the docker-stack tests wait on the mounting→active reconciler's observable state rather than a wall-clock budget, which is why no additional cap is needed to guard the old synchronous-Subscribe flake. No other `-p`/`-parallel` cap appears anywhere in the Makefile or in `.github/workflows/`.
