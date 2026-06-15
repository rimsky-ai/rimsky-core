---
decision: run-name
status: adopted
---

# run-name

## Choice

Default the run name from the compose manifest's project field. A per-invocation run-name override is available; it is constrained to the same filesystem-safe character class the project field is already validated against (a lowercase-alphanumeric-and-hyphen identifier capped in length).

## Rationale

The project field is required and already filesystem-safe by construction. Reusing the same character class for the override keeps the directory-name shape predictable.
