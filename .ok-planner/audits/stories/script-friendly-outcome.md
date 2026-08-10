---
audit: script-friendly-outcome
artifact: story:script-friendly-outcome
determination: supported
compliance: compliant
commit: PENDING
audited: 2026-08-10T05:25:00Z
---

# A script branches on a one-shot run's outcome class without reading its output

Supported. Three one-shot runs driven from a shell `case` that reads only the
exit status and discards the transcript produced all three classes the story
names and produced them distinctly: an all-pass manifest exited 0, a manifest
with one failing instance exited 1, and a manifest whose instance waits on a
20-second upstream exited 2 under `--timeout 3s`. All 3 of the 3 classes the
story enumerates were branched on with no output parsing.
