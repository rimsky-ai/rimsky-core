---
decision: run-name
status: adopted
---

# run-name

## Choice

Default the run name from the compose manifest's project field. A per-invocation run-name override is available; it is written into the run-directory path as given, with none of the project field's filesystem-safe character-class validation (a lowercase-alphanumeric-and-hyphen identifier capped in length) applied to it.

## Rationale

The project field is required and already filesystem-safe by construction, so it is safe to use directly in a directory name. The override has no equivalent check.
