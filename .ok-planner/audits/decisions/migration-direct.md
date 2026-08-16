---
audit: migration-direct
artifact: decision:migration-direct
text: compliant
implementation: supported
commit: PENDING
audited: 2026-08-16T04:44:00Z
---

# In-process migration before the role stack starts, and the absence of a migrate subprocess

Supported. Starting the role stack begins by loading the run's synthetic config, opening the persistence driver it names — the run directory's freshly created SQLite database — and calling the driver's migrate operation directly, closing the driver and failing the start if it errors; only after that does the unified stack launch, so migration completes before any role runner exists. The rejected alternative is absent: the CLI never names or executes the migrate binary anywhere, so there is no subprocess to fork and no second failure path.
