---
audit: object-store-watching-model
artifact: decision:object-store-watching-model
determination: unsupported
commit: 3918d24e
audited: 2026-08-02T09:58:10Z
issue: 2026-08-02-095801-sensor-object-store-memory-backend-always-registered
---

# Deposits are watched through one object-store abstraction with only the filesystem backend shipped

Unsupported. The sensor's object-store abstraction and its single shipped filesystem backend are real, but the in-memory backend is not a test fixture as claimed: the sensor's process entry point registers it unconditionally, with no environment gate, so every production build of the object-store sensor advertises and accepts the memory backend alongside filesystem. The claim that only the filesystem backend ships does not hold.
