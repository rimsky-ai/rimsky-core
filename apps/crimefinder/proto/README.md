# crimefinder proto

Wire protocol for the crimefinder-producer's typed-data gRPC service
(`crimefinder.v1.CrimefinderState`). Loaded at runtime via
`@grpc/proto-loader` by both the producer (server) and the executor
(client). The `rimsky.v1.ClaimProducer` protocol is also implemented
by the producer; that `.proto` lives at the rimsky repo root under
`protocols/proto/v1/claim_producer.proto` and is loaded directly
from there.
