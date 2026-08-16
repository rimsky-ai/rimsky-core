---
audit: terminal-error-abandoned-as-error-class
artifact: decision:terminal-error-abandoned-as-error-class
text: compliant
implementation: unsupported
commit: d977250c
audited: 2026-08-16T05:49:58Z
---

# The abandoned error class, and the sibling classes the rationale says it slots in beside

Unsupported, on the rationale rather than the choice. The choice itself is fully carried: the abandon outcome is an error class, declared alongside the seven other runtime-synthesized classes and included in that list; both terminal paths that settle a poisoned held run stamp the exact signal path; the canonical emit taxonomy still declares exactly eight patterns with no new top-level root; the error wildcard is a valid subscription whose prefix match covers the class; and a scenario test drives an abandon end to end, subscribes a downstream node on the exact path, and asserts the settling signal type and the audited terminal set. What fails is the rationale's supporting enumeration. It asserts that every other failure mode is expressed as an error class under the error root and names three siblings; re-derived against the tree, only one of the three exists, and it is spelled with an underscore rather than a hyphen. Of the other two, one names a watchdog the project retired by migration — the park-reason taxonomy and park-timeout watchdog are gone, and the state machine has a test asserting that transition reason is rejected — and the other exists nowhere in the tree at all: give-up is an error-policy action, and a give-up settlement emits the error class the executor reported, never a handler-specific one. A live decision whose rationale points readers at a retired mechanism and a signal that never existed is corpus rot, not a stale example.
