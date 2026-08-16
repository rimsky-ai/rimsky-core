---
audit: artifact-root-discovery
artifact: decision:artifact-root-discovery
text: compliant
implementation: supported
commit: PENDING
audited: 2026-08-16T04:44:00Z
---

# Walk-up-to-first-marker workdir discovery and its override read against the CLI's self-host paths

Supported. The discovery function absolutizes the current working directory, walks parents testing each for the workdir marker directory, returns the first ancestor carrying one, and falls back to the starting directory when the walk reaches the filesystem root — at which point the run-directory step creates the marker there, so a fresh project gets one on first run. An explicit workdir override short-circuits the walk entirely, creating and absolutizing that directory instead. Both self-hosting entry points call it: the compose one-shot passes its workdir flag through, and the ephemeral single-template run passes no override and so always discovers. Tests cover the ancestor hit, the stop-at-root fallback, and the override bypass.
