---
audit: role-template
artifact: concept:role-template
text: compliant
implementation: supported
commit: PENDING
audited: 2026-08-16T05:42:19Z
checked: 6
unaccounted: 0
---

# Six bundled CLI roles, compiled in, expanded client-side, with no server-side role concept

Supported. Enumerating the embedded role resources in the CLI yields exactly six, and their names match the six the concept lists one for one. They are compiled into the binary through the Go embedding directive at build time and read on demand by name, with an unknown name producing an error that lists the available set. The span the concept claims is real: the administrator role is a single full-wildcard grant, and the publisher role is a single concrete action, with the operator, agent-supervisor, debug-operator, and read-only roles in between. A custom role loads from a local file through a dedicated flag and goes through the same parse and validation path as a bundled one. Both grant-patch operators exist on the create command — an add operator that validates each added action string against the same grammar the server uses, and a remove operator that filters by exact action — and the expanded grant is what the CLI submits. The server-side ignorance claim holds: sweeping the control and foundation trees for any role notion turns up only the unrelated process-role vocabulary, the key row stores the raw expanded grant and no role identifier, and there is no registration surface of any kind for a role. The display nicety is exactly as described — the key-listing command compares a key's grant against each bundled role for equality, printing the role name on an exact match and a custom marker otherwise.
