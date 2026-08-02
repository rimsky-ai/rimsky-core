---
audit: implementation-language-go-plus-ts
artifact: decision:implementation-language-go-plus-ts
determination: supported
commit: 3918d24e
audited: 2026-08-02T09:39:17Z
---

# Go is the sole implementation language; TypeScript survives only as a protocols type stub

Supported. Across the whole repository (excluding `.git` and `node_modules`) exactly one `.ts` file exists — `lib/protocols/index.d.ts`, a 6-line ambient declaration file exporting two type signatures for the protocols wire contract, matching the decision's "type-only stub" description exactly. All 11 bundled services under `lib/services/` (4 executors, 2 claim producers, 4 sensors, 1 subscriber) and all 8 `cmd/` binaries are Go; the one service that once shipped a TypeScript implementation (`claude-agent`) was ported to Go and its TypeScript retired, confirmed by the commit history and the absence of any `package.json`/`.ts` source in that directory today.
