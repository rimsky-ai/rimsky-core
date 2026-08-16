---
audit: exposure-no-config
artifact: decision:exposure-no-config
text: compliant
implementation: supported
commit: d977250c
audited: 2026-08-16T04:44:00Z
---

# One-shot mode's absence from every config surface, read against the unified config schema and the CLI's mode selection

Supported. The unified config's YAML schema is a single closed struct decoded strictly, and enumerating its fields shows fourteen top-level keys — persistence, the four service families, named locks, retention, dispatch defaults, late-bind proxies, peer auth, and the unreachable-validator policy — none of which names an embedded, one-shot, or self-host mode; the persistence block itself carries only a driver, a per-driver connection block, and the blob sub-block, so the rejected knob exists nowhere. Mode selection lives entirely in the command layer: the compose one-shot sub-verb is embedded unconditionally, and the ephemeral-run verb chooses embedded only when no endpoint resolves or when its explicit self-host flag is passed. A deployed stack's config therefore cannot be edited into the embedded shape.
