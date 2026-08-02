---
audit: grpc-google-official
artifact: decision:grpc-google-official
determination: supported
commit: 3918d24e
audited: 2026-08-02T09:44:46Z
---

# gRPC + protobuf libraries are the upstream Google implementations

Supported. All five workspace go.mod files that reference an RPC/protobuf runtime (root, `lib/protocols`, `lib/foundation`, `lib/services`) pin `google.golang.org/grpc` and `google.golang.org/protobuf` as the sole RPC/protobuf dependency; a repo-wide search of every `go.mod`/`go.sum` found no `gogo/protobuf` and no connect-style RPC library (`connectrpc.com/connect` or similar) anywhere in the module graph, so neither rejected alternative has crept in.
