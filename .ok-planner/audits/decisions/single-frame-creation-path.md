---
audit: single-frame-creation-path
artifact: decision:single-frame-creation-path
text: compliant
implementation: supported
commit: d977250c
audited: 2026-08-16T05:33:36Z
---

# Frames open only from a pending message at a frame boundary

Supported. Exactly one production call site inserts a frame row, reached only from the frame engine's open-new-frames pass, which selects the oldest pending message per idle instance and mints the frame's root run scope in the same transaction; every other reference to that insert is test setup. The cascade walker contains no frame-table write of any kind. Messages reach the ledger from exactly two enqueue callers — the operator send route and the cascade send-message node — so a frame's origin is always a ledger row, and the frame row's triggering-message column is declared not-null with a restricting foreign key to the message, making the origin answerable from the observability surface for every frame. Instance creation opens no frame; it materializes the receiver nodes and leaves the instance idle until a message arrives. Frame-engine unit tests cover the oldest-pending pick and the settle path, and end-to-end scenarios confirm one frame per message and a send node opening the next frame.
