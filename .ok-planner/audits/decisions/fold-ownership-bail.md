---
audit: fold-ownership-bail
artifact: decision:fold-ownership-bail
text: compliant
implementation: supported
commit: PENDING
audited: 2026-08-16T05:15:46Z
---

# The verify-before-run ownership bail resolves through the unified claim-handle engine

Supported. When the pre-dispatch ownership check finds the dispatch row stolen, the bail path unwinds each acquired producer-backed claim by calling the unified claim-handle resolution engine with its own dedicated terminal-source kind and an abandon outcome; the engine branches on that source to delete the row rather than promote its state, which is the whole reason the kind exists. No abandon call is made outside the engine on this path — the one direct deletion in the bail helper covers producerless named locks, which have no producer verb to abandon and which the engine refuses by construction, so nothing there duplicates the engine's sequence. A dedicated scenario drives the exact race: a second supervisor steals the dispatch between acquire and verify, and the test then asserts the producer saw an abandon targeting the claim its own open minted and never a commit, that the bail's claim-handle row is gone while a decoy row owned by another supervisor survives untouched, that the node stays in its pre-dispatch state, that no terminal signal or claim-resolution forensics were emitted because the bail is an administrative path, that exactly one orphaned-claim event names the stolen dispatch, and that the executor was never invoked.
