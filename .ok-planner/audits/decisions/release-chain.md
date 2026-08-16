---
audit: release-chain
artifact: decision:release-chain
text: compliant
implementation: supported
commit: d977250c
audited: 2026-08-16T04:47:25Z
---

# Whether one release chain runs the seven steps in the declared order, shared by both flows

Supported. The release target's prerequisites are the six named stages in exactly the declared sequence — lint, core images, bundled-service images, the full test suite, the image scan, then push — and a fitness test compares that list against the same order and fails on any change. The licensing check is wired as a prerequisite of the lint stage, so it runs in the same gate (ahead of the linter's own run rather than after it, which is the only place the text and the recipe differ). The images-before-tests rationale is real: the test target itself depends on the three image sets, because the scenario suites resolve locally-built images by a content-addressed tag. Both flows share this one chain — the mechanical dev-release script invokes the same target with two variables overridden, and no second chain exists.
