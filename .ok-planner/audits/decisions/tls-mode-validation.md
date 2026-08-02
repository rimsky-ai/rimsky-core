---
audit: tls-mode-validation
artifact: decision:tls-mode-validation
determination: supported
commit: b767a27d
audited: 2026-08-02T09:38:12Z
---

# The `tls` field is a parse-time validated off/required enum

Supported. `lib/control/config/claim_producers.go::parseTLSMode` is the sole parser for the field across all 5 peer-entry kinds (claim_producers, executors, publishers, validators, data_processors): empty string and `"off"` both map to `peer.TLSModeOff`, `"required"` maps to `peer.TLSModeRequired`, and any other value — including `"opportunistic"` — returns a config-load error naming the block, entry, and the offending value, rejecting the whole config load rather than defaulting silently. `peer.TLSModeOff`/`peer.TLSModeRequired` (`lib/runtime/peer/credentials.go`) are the only two mode constants defined anywhere in the codebase, confirming no third value exists to accept elsewhere.
