---
audit: depguard-protocols-purity
artifact: decision:depguard-protocols-purity
determination: supported
commit: 3918d24e
audited: 2026-08-02T09:38:50Z
---

# Protocols module's dependency budget is stdlib + grpc + protobuf + uuid + yaml

Supported. `lib/protocols/go.mod` requires exactly `github.com/google/uuid`, `google.golang.org/grpc`, `google.golang.org/protobuf`, and `gopkg.in/yaml.v3` as direct dependencies (the rest are transitive/indirect); the `.golangci.yml` `protocols-purity` rule applies to every file under `**/protocols/**` (including tests, no per-file exemption) and denies `lib/foundation`, `lib/graph`, `lib/runtime`, `lib/control`, `cmd`, and `testcontainers-go`. A repo-wide grep of all `.go` files under `lib/protocols` for those rimsky-internal import paths plus `testcontainers`/`testify` returned zero hits, confirming no rimsky-internal code or test infrastructure has crept into the module.
