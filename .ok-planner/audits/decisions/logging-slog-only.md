---
audit: logging-slog-only
artifact: decision:logging-slog-only
text: compliant
implementation: supported
commit: PENDING
audited: 2026-08-16T04:47:25Z
---

# Whether logging is the standard library's structured logger and nothing else

Supported. One hundred files across the four modules import the standard structured-logging package, and no source file in the tree imports either rejected alternative or any other third-party logger — the only occurrences of those names anywhere are the string literals in the fitness test that forbids them, which scans all four manifests and fails if any requires one. Nothing imports the older standard logging package in non-test code either, so the structured logger is the single idiom.
