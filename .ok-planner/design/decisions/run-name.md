---
decision: run-name
status: adopted
---

# Run name defaults from the manifest's project field

## Choice

Default the run name from the compose manifest's project field. A per-invocation run-name override is available; it is written into the run-directory path as given, with none of the project field's filesystem-safe validation applied to it.

## Rationale

The project field is required and already filesystem-safe by construction, so it is safe to use directly in a directory name. The override has no equivalent check.

## Alternatives

- Requiring an explicit run name on every invocation — rejected: ceremony for the common case the manifest already names.
- Applying the project field's validation to the override — rejected: the override is deliberate operator input naming a path the operator owns; re-imposing the manifest's identifier grammar on it rejects names the filesystem accepts.
