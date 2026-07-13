# Intent Dossier: cascade-graph

Distilled 2026-07-13 from session transcripts (2026-06-12..2026-07-13) and ok-planner
history artifacts (2026-05-04..2026-06-11). Transcript tier outranks artifact tier;
later intent supersedes earlier. Part of the drift-remediation intent ledger.

## Net position

The record on this concept is a single artifact-tier source (2026-05-11, log-convergence); weigh accordingly.

- cascade-graph is the **read-only operator-dashboard HTTP backplane** on rimsky-control-api: routes `/observability/*`, `/events`, `/frames`, `/nodes/{instance}/{type}`, `/dispatches` serving JSON views of frames, nodes, dispatches, and events.
- Every handler runs inside a short fresh transaction; **no handler in this surface mutates state**.
- Routes are mounted at bare paths with no `/v1/` prefix, matching control-api versioning discipline.

## Required behaviors (open promises)

- Read-only JSON dashboard surface with the routes above; per-handler short fresh transaction — "All cascade-graph HTTP handlers run inside a short fresh transaction (inTx). Read-only: no handler in this surface mutates state." (2026-05-11, log-convergence, artifact) (artifact-only)
- Bare-path mounting, no `/v1/` prefix (2026-05-11, log-convergence, artifact) (artifact-only).
