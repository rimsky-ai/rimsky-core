---
decision: run-name
status: adopted
---

# run-name

## Choice

Default the run name from the compose manifest's `project` field. `--name <name>` overrides the default, passed through the same regex (`^[a-z][a-z0-9-]{0,62}$`) the project field is already validated against.

## Rationale

The project field is required and already filesystem-safe by construction. Reusing the same regex for `--name` keeps the directory-name shape predictable.
