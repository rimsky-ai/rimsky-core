---
audit: memory-gate-premise-corrected
artifact: decision:memory-gate-premise-corrected
text: compliant
implementation: supported
commit: d977250c
audited: 2026-08-16T05:52:00Z
---

# The memory blob backend rejected at startup outside single-process mode

Supported. One validator rejects the memory backend whenever the process topology is not unified, and it runs on all four startup paths — the single-process stack launcher and each of the three per-role launchers — with the error aborting the boot rather than degrading, so the rejection is genuinely a startup gate and not a lazy failure. The error text does what the decision says it should: it names the single-process mode as the reason, spells out that all roles must share one in-process map, names the environment marker and the three invocations that set it, and states why a per-role process cannot qualify. Unit coverage pins all three topology cases — split, unified, and the zero value, which is treated as split so an unset marker fails closed. Both rejected alternatives are absent: the backend is neither ungated nor removed — it remains one of the four selectable backends and works in the mode it is gated to, where its delete method lets the orphan sweep reap it in the same process that wrote it. The stated asymmetry also holds: the embedded persistence driver carries no equivalent gate, only a warning.
