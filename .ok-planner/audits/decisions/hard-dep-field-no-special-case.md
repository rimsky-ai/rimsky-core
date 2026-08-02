---
audit: hard-dep-field-no-special-case
artifact: decision:hard-dep-field-no-special-case
determination: supported
commit: 3918d24e
audited: 2026-08-02T09:28:34Z
---

# Attribute-field hard-dep flag gets no special-case treatment

Supported. The only place `force_upstream_refresh` is read at all is the subscription-entry field (`hardDepSendersOf`, feeding `BuildHardDepEdges`) — a subscription-level flag, not an attribute-field property. Searching every Go source file in the module for any case-insensitive occurrence of "hard_dep" turns up only that subscription edge-building code path and its call sites; the attribute-schema validator (`lib/graph/node/template_validator_attrschema.go`, `lib/graph/attribute/validate.go`) contains no reference to any such property name, and there is no migration-redirect error message anywhere in the codebase naming a retired `hard_dep:` attribute-field flag.
