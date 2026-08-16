---
audit: handler-package-in-service-directory
artifact: decision:handler-package-in-service-directory
text: compliant
implementation: unsupported
commit: d977250c
audited: 2026-08-16T04:43:41Z
checked: 11
unaccounted: 5
---

# Whether every bundled service directory exposes importable handler packages beside a thin main

Unsupported as a universal. Enumerating the bundled service directories under the services module gives eleven — four executors, two claim producers, four sensors, one subscriber, matching the eleven images the build produces. Six meet the claim exactly: each of the four executors keeps its handler in a named importable package at the directory root with a thin main under a command subdirectory, and each of the two claim producers splits into importable server and store packages with the same thin main, and the physical layouts do vary between the two groups as the decision allows. Both surfaces run that same code: the standalone main constructs the handler and serves it over gRPC, and the single bundled registration entrypoint constructs the same handler types and registers them in-process, so the dual-mode property is real for those six. The remaining five keep every source file in the command package itself, so no handler package exists to import; the subscriber has one importable subpackage, but it holds wire types rather than the handler. Those five also have no in-process consumption path at all — nothing registers a sensor or a subscriber in-process — and the decision's own text does not settle whether a service without that second surface is in scope: its first sentence quantifies over every bundled service directory, while its closing invariant and its rejected alternatives all rest on the in-process path being foreclosed. That question is what the verdict turns on.

## Unaccounted

- The cron sensor: handler, state store, and entrypoint all in the command package; nothing importable.
- The HTTP poll sensor: same shape; nothing importable.
- The object-store sensor: same shape, including its lister backends; nothing importable.
- The webhook sensor: same shape; nothing importable.
- The OpenLineage subscriber: handler in the command package, with only a wire-types subpackage importable.
