---
decision: licensing-dual-apache-agpl
---

# License split

## Choice

Per `concept:module-layout`, dual-track licensing across two surfaces. A permissive open-source license covers the protocols module, the surface external implementers link against. A strong-copyleft license with a commercial alternative covers the project's other code groups: the foundation module, the graph layer, the runtime layer, the control layer, the bundled services, the binaries group, the test group, and the tools group. Those nine groups are the whole licensed population; every source file the project authors sits in one of them.

A file outside the nine groups carries no track and no per-file license header. The planner estate is such a tree. It holds the design corpus, the audit's records and instruments, and the project's working records. Ceremonies rewrite those files; the project ships none of them. The licensing map declares the estate outside both tracks, so the license check and this decision cover the same population.

## Rationale

Permissive surface for everything an external implementer links against to build a peer; copyleft for the orchestrator itself, with a commercial alternative so organizations that prefer not to take on copyleft obligations on modified or derivative work, or on network-delivered services, can use the orchestrator under negotiated terms.

Naming the groups rather than saying "everything else" makes the population enumerable. A checker that walks the tree must know which files it governs. A rule that reaches everything not otherwise named sweeps in records the project neither ships nor licenses. A record that graduates into a code group takes that group's track.

## Alternatives

- One permissive license across the whole repo — rejected: forfeits the copyleft lever on the orchestrator core and with it the commercial-licensing alternative.
- Copyleft across the whole repo — rejected: external implementers could not depend on the protocol definitions without taking on copyleft obligations, defeating the integration surface's purpose.
- Put the planner estate on the copyleft track and stamp every file — rejected: header upkeep on trees a ceremony rewrites every run, protecting nothing a consumer receives.
