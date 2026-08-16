---
document: protocols
target: docs/protocols/
---
# Protocol implementation guides

## Purpose
A service author implementing a rimsky protocol in their own process opens the guide for that protocol to learn the call order, the outcome shapes, the deadline and callback contract, and the enrollment posture their service must satisfy. They close it able to write a service rimsky's conformance run accepts. One guide per protocol a third party implements, plus one shared-conventions page (the HTTP+JSON bridge and mTLS enrollment).

## Covers
- every public gRPC RPC, grouped by the protocol a third party implements
- every public proto file
- every public published package a service implementer imports
- the public environment variables a peer service reads to enroll
