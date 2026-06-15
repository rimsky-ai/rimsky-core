---
decision: licensing-dual-apache-agpl
status: as-is
---

# License split

## Choice

Per `concept:module-layout`, dual-track licensing across two surfaces: a permissive open-source license covers the protocols module, the examples module, and the bundled TypeScript executor reference (the surface external implementers copy, modify, or link against); a strong-copyleft license with a commercial alternative covers everything else (the foundation module, graph layer, runtime layer, control layer, the other bundled services, the binaries group, the test group, the tools group).

## Rationale

Permissive surface for everything an external implementer is meant to copy, modify, or link against; copyleft for the orchestrator itself, with a commercial alternative so organizations that prefer not to take on copyleft obligations on modified or derivative work, or on network-delivered services, can use the orchestrator under negotiated terms.
