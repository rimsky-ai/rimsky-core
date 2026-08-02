---
audit: cascade-signal-blind
artifact: story:cascade-signal-blind
determination: supported
commit: 3918d24e
audited: 2026-08-02T09:28:29Z
---

# Template author wires reactive nodes against any cascade-firing signal

Supported. `concept:cascade`'s firing-gate predicate names exactly 3 signal kinds as cascade-firing — `terminal/success`, `terminal/error/<class>`, `attribute/<key>/changed` — and node-subscription entries target any of them through one uniform `subscribes:` grammar (exact type-path, `terminal/error/*`/`terminal/*` wildcards, plus a payload `when:` predicate). `test/scenarios/cascade_signal_blind_e2e_test.go` exercises all 3 kinds through that one mechanism: terminal/success (exact match), terminal/error/* (wildcard, both give-up and pass error-policy actions), attribute/<key>/changed (both a first-fire and a diff-gated multi-round case distinguishing per-key firing from a same-value re-settle), plus a tag-filter `when:` predicate proven both to fire and to suppress. A companion service-level e2e (`lib/services/test/scenarios/sensor_cascade_e2e_test.go`) confirms the same subscription surface reacts to an externally-driven (sensor/publisher) settling signal indistinguishably from an executor-driven one. No cascade-firing signal kind outside this 3-member population exists per the signal taxonomy's own closed enumeration (`transient/*` is explicitly audit-only and non-subscribable, rejected at registration).
