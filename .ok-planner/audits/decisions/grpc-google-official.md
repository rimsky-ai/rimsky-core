---
audit: grpc-google-official
artifact: decision:grpc-google-official
text: compliant
implementation: supported
commit: d977250c
audited: 2026-08-16T04:47:25Z
---

# Whether the official upstream gRPC and protobuf libraries are the ones in use

Supported. Three of the four module manifests — root, protocols, services — require the official gRPC and protobuf libraries directly at a single shared version each, and a manifest fitness test fails if either pin disappears; the foundation module pulls them transitively. No competing implementation appears anywhere in the four manifests: searches for the gogo fork, the Connect libraries, and Twirp all returned nothing. The codegen is the canonical upstream pair as well — the proto-generation target drives the standard protobuf and gRPC plugins over the ten protocol definitions, and the generated servers and clients are what the 44 files creating or dialing gRPC endpoints use.
