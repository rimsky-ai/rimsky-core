---
decision: hard-dep-field-no-special-case
status: as-is
---

# Attribute-field hard-dep flag gets no special-case treatment

## Choice

The cascade walker and edge builder do not read any attribute-field `hard_dep:` flag. The attribute-schema validator carries no special-case rejector for that property name; whatever the existing JSON Schema validation does with unknown properties applies. No migration-redirect error is generated.

## Rationale

Pre-v1, the project does not owe consumers a migration helper. The cleanest interpretation of the rule is "no special-case code naming the flag."

## Alternatives

- Explicit `hard_dep_field_removed` registration error with migration redirect — rejected: a named rejector would commemorate a flag that has no place in the project.
