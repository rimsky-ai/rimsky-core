---
audit: timestamp-format
artifact: decision:timestamp-format
text: compliant
implementation: supported
commit: d977250c
audited: 2026-08-16T04:44:00Z
---

# The filesystem-safe timestamp layout wherever a timestamp is embedded in a path

Supported. Exactly one function in the tree formats a timestamp for a file or directory name — the run-directory namer — and it converts to UTC and emits the hyphenated layout the choice specifies. Sweeping the whole codebase for date layouts turns up three others, all of them row serialization for the SQL driver rather than path construction, so the population of path-embedded timestamps has one member and it conforms. A test pins both properties the rationale rests on: the output contains no colon, and it matches the exact year-month-day, hyphen-separated time, trailing-Z pattern, which also gives the lexicographic ordering directory listings need. Neither rejected form appears: no unmodified colon-bearing timestamp and no epoch-seconds name.
