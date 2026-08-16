---
audit: cron-robfig-v3
artifact: decision:cron-robfig-v3
text: compliant
implementation: supported
commit: d977250c
audited: 2026-08-16T04:47:25Z
---

# Whether cron expressions are parsed by the pinned robfig v3 parser and nothing else

Supported. Exactly one module manifest requires the library, pinned on the v3 major line, and a manifest fitness test fails if that pin disappears. Exactly one package in the tree parses cron expressions — the bundled cron sensor — and it does so through the library's standard-grammar parser at both of its two entry points, keeping the parsed schedule to compute next and missed fire windows; a repo-wide search for cron-parsing code found no second parser and no hand-rolled expression handling. The sensor's behavior is exercised by its own unit tests and by a restart-recovery scenario in the services suite.
