---
audit: release-dev-mechanical
artifact: decision:release-dev-mechanical
text: compliant
implementation: supported
commit: d977250c
audited: 2026-08-16T04:47:25Z
---

# Whether the dev-release flow derives its version mechanically with no judgment and no drafted notes

Supported. The dev-release script reads the last stable tag, increments its minor to form the next-minor base, and appends a pre-release segment carrying a UTC build date and the short commit hash — no prompt, no judgment step, and no notes-drafting stage anywhere in it; the GitHub prerelease it creates uses the host's auto-generated commit notes. The derived version drives everything downstream: it tags the repo and the protocols module, bumps the published package version, and is passed into the shared release chain alongside the dev channel override, so every artifact of a dev build carries the same traceable version. A fitness test pins the next-minor derivation and the dot-joined date-and-hash form, and fails if the hash is ever appended as build metadata instead, which the tag grammar would reject.
