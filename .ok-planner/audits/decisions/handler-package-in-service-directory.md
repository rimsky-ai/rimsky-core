---
audit: handler-package-in-service-directory
artifact: decision:handler-package-in-service-directory
determination: supported
commit: 3918d24e
audited: 2026-08-02T09:28:52Z
---

# Every dual-mode bundled service splits an importable handler package from a thin standalone main

Supported. Checked all 6 service directories `bundled.RegisterAll` participates in — the population the decision's "in-process dispatch pool" scopes to, since sensors and the OpenLineage subscriber have no in-process registry to join and are plain `package main` throughout: 4 executors (`claude-agent`, `http-node`, `verifier-http`, `verifier-shape-checks`) and 2 claim producers (`filesystem`, `postgres`). Each has a non-`main` importable handler package (`claudeagent`, `httpnode`, the verifier packages, and each claim producer's `server` package) plus a separate `cmd/main.go` that is the only `package main` file; the standalone `main` constructs the handler and serves it over gRPC (e.g. `httpnode.NewServer(opts)` in `http-node/cmd/main.go`), and `lib/services/bundled/bundled.go` constructs the identical handler type in-process (`httpnode.NewServer(o)`) for all 6, confirming both surfaces run the same handler code per service without a uniform internal layout being imposed.
