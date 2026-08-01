---
decision: config-format-yaml
status: as-is
---

# Configuration format is YAML

## Choice

YAML is the single configuration format: the unified config file (see `concept:rimsky-yml`) and the per-service configs are all YAML.

## Rationale

Human-readable and hand-editable, supports comments, and is the incumbent format across deployment tooling, so operators bring existing fluency and configs sit naturally beside the compose and orchestration files they accompany.

## Alternatives

- JSON — rejected: no comments and noisy to hand-edit.
- TOML — rejected: awkward for the deeply nested structures the config carries, and far less established in deployment tooling.
