---
audit: depguard-foundation-internal
artifact: decision:depguard-foundation-internal
text: compliant
implementation: supported
commit: d977250c
audited: 2026-08-16T04:47:25Z
checked: 11
unaccounted: 0
---

# Whether the foundation module's internal package is imported only from inside that module

Supported. The dependency lint carries a rule matching every file in the tree except those under the foundation module and denying the module's internal package by import path, with a message pointing at the public packages — present and shaped as the choice describes, and asserted by a fitness test. Reality matches: all 11 files in the tree that import anything under that internal path sit in four packages, every one of them inside the foundation module, so no external importer exists to be caught. The alternative the decision rejects is accurately described — the Go toolchain's own visibility rule already blocks the path-based edge, and the lint rule is the one that carries the redirection message.
