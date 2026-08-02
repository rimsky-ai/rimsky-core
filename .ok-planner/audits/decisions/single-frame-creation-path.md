---
audit: single-frame-creation-path
artifact: decision:single-frame-creation-path
determination: supported
commit: 3918d24e
audited: 2026-08-02T09:28:34Z
---

# Single frame-creation path

Supported. `InsertRunningFrame`, the persistence call that creates a frame row, has exactly one production call site: `openRunningFrameForMessage` in the frame package's producer, which in turn is called from exactly one place, the frame engine's message-pickup tick loop — checked every non-test reference to both symbols across the module. There is no second frame-creation path anywhere in the cascade walker or elsewhere; every frame opens only when a message is picked up off an instance's queue, exactly as the decision states.
