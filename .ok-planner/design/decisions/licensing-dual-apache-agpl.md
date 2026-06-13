---
decision: licensing-dual-apache-agpl
status: as-is
---

# License split

## Choice

The protocols module + the services module's claude-agent executor (the TS reference executor) + the examples module are Apache-2.0; everything else (the foundation module, graph layer, runtime layer, control layer, the other bundled services, the cmd group, the test group, the tools group) is AGPL-3.0-or-later with a commercial alternative.

## Rationale

Apache surface for everything an external implementer is meant to copy, modify, or link against; AGPL for the orchestrator itself.
