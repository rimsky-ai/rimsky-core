---
issue: object-store-sensor-cloud-backends-absent
kind: human
category: bundled-services
artifacts:
  - story:sensor-object-store
  - concept:sensor
  - decision:object-store-watching-model
status: answered
opened: 2026-07-22T10:19:01Z
github: https://github.com/rimsky-ai/rimsky-core/issues/37
---

# Is the object-store sensor's cloud-backend absence a gap awaiting work, or the sensor's intended boundary?

Question: the filed Problem noted the bundled object-store sensor rejects `s3` / `gcs` / `azure` at `Subscribe`, and that no artifact said whether that absence is permanent.

Answer: it is the sensor's intended boundary, now stated by `decision:object-store-watching-model`, applied in commit `2ef58038` ("Corpus deltas applied verbatim: decision:object-store-watching-model (closes gh#37)"). The decision's Choice: "the current build ships the local filesystem as its only registered backend ... alongside an in-memory backend that is a test fixture, not a shipped store," and its Rationale states the generalization is kept deliberately — "the backend seam is a single listing operation, so a real object-store backend is a drop-in lister" — without committing the bundled sensor to ship one now. Current code matches: `lib/services/sensors/sensor-object-store/main.go` registers only the filesystem backend by default, gates the in-memory fixture behind `RIMSKY_SENSOR_OBJECT_STORE_ENABLE_MEMORY_BACKEND`, and has no `s3`/`gcs`/`azure` registration path at all. Nothing here is left for a sprint to carry.
