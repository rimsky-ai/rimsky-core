# Intent Dossier: tag

Distilled 2026-07-13 from session transcripts (2026-06-12..2026-07-13) and ok-planner
history artifacts (2026-05-04..2026-06-11). Transcript tier outranks artifact tier;
later intent supersedes earlier. Part of the drift-remediation intent ledger.

The record covers two tag kinds — **template tags** (movable aliases for template hashes) and **node tags** (operator-facing metadata on nodes) — plus the compose-owned tag namespace.

## Net position

- A template tag is a movable name for a template hash; rebinding atomically redirects subsequent instance creation to the new hash. **Tag movement never migrates live instances** — an instance binds to the resolved hash at creation and stays bound; there is no live re-bind. Deleting a tag makes the name unresolvable for new instances.
- Node tags are free-form template-author strings: no rimsky-defined vocabulary, no effect on scheduling, cascade, or validation — pure operator-facing metadata, filterable but never operator-writable.
- Node-tag substitution admits **only `{{params.<key>}}`**, applied at instance-creation materialization; this restriction was deliberately maintained when the `env` source kind was added to the substitution grammar (2026-06-24, transcript).
- The `compose:<project>:` prefix on tags and instance keys is reserved for the compose machinery and **enforced server-side**: the control-api rejects compose:-prefixed creates from foreign clients with 400 regardless of their grants, while the compose machinery (compose-origin discriminated) succeeds.
- Compose itself is purely application-layer: it reconciles declared templates/tags/instances into an already-running rimsky, project-scoped to its own compose:-tagged resources, never touching manually-created ones and never invoking infra commands.

## Required behaviors (open promises)

- Tag rebind atomicity and no-instance-migration (2026-05-04, modeling-layer-contract + 2026-06-08, corpus-bootstrap, artifact): "instances bind to the resolved hash at creation time and stay bound."
- CLI vocabulary: `template_hash` is the output key everywhere (`template_id` / bare `id` are deprecated); verbs are `template register/rm`, `tag mv`, `tag rm` (2026-05-04, public-docs-architecture, artifact-only).
- Node `tags:` list on template nodes; persisted as `rimsky_nodes.tags` (TEXT[] + GIN index on Postgres; JSON-encoded TEXT on SQLite); returned by node reads; filterable via `GET /instances/{id}/nodes?tag=<value>` single-value exact match (2026-05-19, multi-instance-template-ergonomics, artifact).
- Materialized node tags are a projection of the bound template version: re-registering with edited tags produces a new hash affecting new instances only (2026-05-19, artifact).
- Registration cross-checks `{{params.<key>}}` in tags against ParamsSchema, rejecting undeclared keys; any non-params directive in a tag is rejected (2026-05-19, artifact); a missing param or non-string whole-directive lift fails instance creation with a typed error (2026-05-19, artifact).
- Operators cannot add or remove tags via userdata_overrides or any other route (2026-05-19, artifact).
- Server-side compose: prefix enforcement (2026-06-06, comprehensive-gap-closure + 2026-06-08, corpus-bootstrap, artifact): "the compose-managed namespace stays disjoint from manually-authored artifacts no matter which client made the call."
- `rimsky compose up/plan/status/down` reconciles a rimsky-compose.yml manifest as one declarative unit: plan reports the diff without applying; down removes only the project's resources; no verb shells out to docker/kubectl and none is stubbed (2026-06-06 + 2026-06-08, artifact).

## Intentional absences

- **Live instance re-bind / migration on tag movement** — never existed by design (2026-05-04).
- **Operator mutation of node tags** — excluded by design (2026-05-19).
- **`env` (or any non-params) substitution in tags** — deliberately not added when the grammar gained the env source kind (2026-06-24, 3b1066c7, transcript).
- **Compose bootstrap/infra behavior** — compose never starts rimsky, never invokes docker/terraform/kubectl, materializes no rimsky config; the cut init/dev/embedded-bootstrap halves of the original compose work stay cut (2026-06-06).

## Corrections and restorations (drift-fight record)

- **Compose engine deleted wholesale** (2026-06-06, gap-closure): commit 70a0b98 removed the existing ~3,000-line compose engine (manifest parser, reconcile/plan engine, up/down/plan/status, tests) under an overreaching "bootstrap belongs in docs" rationale. Ruled: recover from git (70a0b98^) and adapt to the current cmd/rimsky/cli layout — not reimplement — keeping the app-layer engine, dropping the bootstrap halves.
- **Client-side-only prefix enforcement closed** (2026-06-06): the 2026-05-04 known-open-question posture (CLI rejects the compose: prefix; raw control-api calls don't) was resolved to server-side enforcement, superseding the courtesy convention.

## Superseded / historical

- compose: prefix as a client-side CLI convention with server enforcement an open v1 question (2026-05-04) → server-enforced 400 with compose-origin discrimination (2026-06-06).
- `template_id` / bare `id` CLI output keys → `template_hash` (2026-05-04).
- The planned migration test pinning the tags column default and GIN index never landed (2026-05-19, divergences, artifact-only) — recorded coverage gap, not a retraction.
