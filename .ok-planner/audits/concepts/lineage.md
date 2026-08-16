---
audit: lineage
artifact: concept:lineage
text: compliant
implementation: unsupported
commit: d977250c
audited: 2026-08-16T05:33:36Z
---

# The lineage projection: two append-only record kinds, forward-only writes, the query surface, and the pass-through exclusion

Unsupported, on the pass-through invariant. The rest holds. The projection persists two record kinds under exactly the two names the concept gives them, through a table interface that offers insert, point reads, a filtered query, a parent-keyed query, and a retention delete — no update anywhere, so append-only is structural. Writes are forward-only from the terminal paths: there is no replay or rebuild entry point on the interface, on the control API, or on the CLI. Data dependency is genuinely captured on every leaf-run record — all eight emission sites in the runtime and scheduler populate the substitution-reference list from the node's declared attribute references, and the held-claim citations carry claim-scope hashes. The query surface matches the description member for member: point lookups by run id, by claim-handle id, by source type and id, and by producer name; ancestor and descendant walks over both runs and claim handles, each taking a depth parameter clamped to a fixed ceiling; and every reverse-lookup and walk response carries a truncation flag set when the internal scan budget runs out before the data does. A manual prune endpoint exists behind its own permission, and the retention sweep applies one configurable trailing window with a thirty-day default, independent of the run and claim-handle windows. What is not carried is the invariant that pass-through nodes emit no leaf-run record. It holds for fan-out parents, which dispatch children and return before any terminal emission. It does not hold for pure-cascade nodes: those settle in the scheduler's own sweep, which transitions the run to fresh and then writes a leaf-run lineage record with a pure-cascade terminal discriminator. The concept calls their absence from the leaf-run surface structural and by design; in fact they are present, and the audit-log-only causality story the invariant offers for them is not the one the code implements.
