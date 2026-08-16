---
kind: ports
release: d977250c
population: 20
---

- `8080 (control API)` — public under: general rule; not driven by any measured story way
- `9081 (sensor-cron gRPC)` — public under: general rule; not driven by any measured story way
- `9082 (sensor-http gRPC)` — public under: general rule; not driven by any measured story way
- `9083 (sensor-object-store gRPC)` — public under: general rule; not driven by any measured story way
- `9084 (sensor-webhook gRPC)` — public under: general rule; not driven by any measured story way
- `9090 (claude-agent gRPC)` — public under: general rule; not driven by any measured story way
- `9090 (host-agent-proxy agent-facing gRPC)` — public under: general rule; not driven by any measured story way
- `9091 (host-agent-proxy peer-facing mTLS gRPC)` — public under: general rule; not driven by any measured story way
- `9091 (http-node gRPC)` — public under: general rule; not driven by any measured story way
- `9092 (http-node HTTP)` — public under: general rule; not driven by any measured story way
- `9095 (verifier-shape-checks gRPC)` — public under: general rule; not driven by any measured story way
- `9096 (verifier-http gRPC)` — public under: general rule; not driven by any measured story way
- `9100 (claim-producer-filesystem gRPC)` — public under: general rule; not driven by any measured story way
- `9100 (supervisor async-callback listener, rimsky-all-in-one baked callback.port)` — public under: general rule; not driven by any measured story way
- `9101 (claim-producer-postgres gRPC)` — public under: general rule; not driven by any measured story way
- `9110 (claim-producer-filesystem HTTP bridge)` — public under: general rule; not driven by any measured story way
- `9111 (claim-producer-postgres HTTP bridge)` — public under: general rule; not driven by any measured story way
- `9121 (claim-producer-postgres admin)` — public under: general rule; not driven by any measured story way
- `9184 (sensor-webhook HTTP ingress)` — public under: general rule; not driven by any measured story way
- `9190 (claude-agent HTTP)` — public under: general rule; not driven by any measured story way
