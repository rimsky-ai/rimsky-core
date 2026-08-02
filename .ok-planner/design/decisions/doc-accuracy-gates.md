---
decision: doc-accuracy-gates
---

# Enumerating documentation is gated against the code facts it enumerates

## Choice

Documentation that enumerates code facts is kept honest by build-time gates that mechanically diff the prose against those facts. Two current instances: the after-code-changes rules gate (recognized filesystem-path citations must resolve against the repo tree) and the substitution-doc gate (the documented source-kind list must match the runtime resolver's dispatch set). New documentation surfaces that enumerate code facts follow the pattern.

## Rationale

Enumerating prose rots silently — a rename lands, the doc still reads plausibly, and the reader hits a missing surface. A mechanical diff at build time catches the drift when it is introduced, not when a reader trips on it. This is the repository's own methodology (mechanical checks are authoritative over prose) applied to documentation.

## Alternatives

- Review discipline — rejected: drift lands silently between reviews, and the reviewer has no enumeration to check against.
- Generating the enumerating prose from the code — avoids drift entirely; rejected: couples doc builds to code internals and surrenders hand-authored framing where the gate keeps prose free and still checked.
