---
audit: implementation-language-go-plus-ts
artifact: decision:implementation-language-go-plus-ts
text: compliant
implementation: supported
commit: PENDING
audited: 2026-08-16T04:47:25Z
---

# Whether Go implements everything shipped and TypeScript appears only as a wire-contract type stub

Supported. All 1,686 tracked Go sources carry the implementation, including every bundled service and executor reference — each of the eleven shipped services builds from a Go main package, and no shipped binary is written in anything else. TypeScript appears in exactly one tracked file, the protocols module's declaration file, and it is a pure ambient stub: two declarations and no implementation. The tracked non-Go sources are all outside the shipped surface — the Python is entirely the planner estate's experiment instruments, the shell scripts are build, release and test glue, and the two JavaScript hooks belong to the lint estate. One nuance the choice's text does not mention: the published protocols package also ships a thirteen-line JavaScript module resolving the packaged proto paths, which is what the type stub types; it is neither TypeScript nor a binary, so it sits beside the choice rather than against it.
