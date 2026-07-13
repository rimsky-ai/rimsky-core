# Intent Dossier: rimsky-yml

Distilled 2026-07-13 from session transcripts (2026-06-12..2026-07-13) and ok-planner
history artifacts (2026-05-04..2026-06-11). Transcript tier outranks artifact tier;
later intent supersedes earlier. Part of the drift-remediation intent ledger.

## Net position

- rimsky.yml (RIMSKY_CONFIG) is the single deployment-shape config loaded by all binaries; RIMSKY_SUPERVISOR_CONFIG is supervisor tuning only.
- Peers are declared under top-level role-named blocks (claim_producers:, executors:, publishers:, validators:, data_processors:); each entry may declare a protocols: list (defaulting to the block's protocol); a multi-protocol peer gets duplicate entries across blocks pointing at the same endpoint. There is no generic services: block and no separate lifecycle_subscribers: block.
- Per-service entries carry only connection-level, universal fields (endpoint, TLS, transport, protocols, observability). rimsky.yml has never carried service-schema-specific config bytes; rimsky treats entry names as opaque and no production code branches on a specific service implementation's name. Confirmed intended design, to be preserved (2026-07-03, user).
- Config split: what a node needs (MCP servers, env vars) lives in node config owned by the template author; whether those declarations are trusted is operator policy at deployment level; rimsky.yml stays universal name-to-URL mapping. All-in-one defaults gates open; containerized operators close them.
- rimsky.yml is unchanged for all-in-one: bundled handlers read the same service-owned env vars in both modes; rimsky.yml remains solely for connecting to external services by endpoint (hybrid bundled + external supported).
- Retired keys are hard-rejected: stores: (top-level and per-node), single-value write_semantics:, and write_semantics_envelope: fail validation at startup with a precise error naming the canonical key (claim_producers:, write_semantics_allowed: list form).
- Startup runs the one-shot capability handshake: dial each declared peer, call Capabilities per declared protocol, validate the operator declaration (write_semantics_allowed must be a subset of the producer-declared envelope), fail fast on any unreachable peer or mismatch; capabilities are cached for the process lifetime.
- No auth-related keys exist in rimsky.yml at all: anonymous mode is data-derived from rimsky_api_keys row counts.

## Required behaviors (open promises)

- Unified config loaded by all binaries, with the runtime processes performing the dial + Capabilities + validate handshake and failing the process at startup on any unreachable peer or mismatch; migrate consumes the persistence block only (2026-05-04, modeling-layer-contract, artifact): "any failure (unreachable, mismatch) fails the rimsky process at startup."
- Envelope-subset validation: operator-declared write_semantics_allowed must be a subset of the producer-declared envelope from Capabilities(); every template-referenced producer name and named lock must be declared in config (2026-05-04, modeling-layer-contract, artifact).
- Retired-key hard rejection with precise redirect errors: stores:, single-value write_semantics:, write_semantics_envelope: (2026-05-12, nomenclature-resolution, artifact): "fails YAML validation at startup with a precise error directing the operator to the canonical key." The top-level stores: rejection was reaffirmed during the completion of the rename sweep (2026-06-19, a02fe167, transcript).
- The stores→claim_producers rename is complete across every surface: the per-node template field, scheduler predicates (acquiresClaims), observability routes (/claim-producers), config types (ClaimProducer* variants), proto fields/messages (claim_producers, ClaimProducerHandle), directory and image names, and docs (2026-06-19, a02fe167, transcript, user: "rename them all").
- publishers: is a top-level block using map-keyed-by-name syntax matching the sibling blocks; the old dual-block discovery of sensor-advertising peers under claim_producers:/executors: is retired (2026-05-17, sensor-messaging-unification, artifact).
- Multi-role single binaries register the same logical name in each role's namespace pointing at the same endpoint — the canonical pattern; role separation is achieved by deploying two named instances, not new config surface (2026-05-19, multi-instance-template-ergonomics, artifact).
- The tls key exists on executor, store, and publisher peer entries and is honored at every peer dial site; required verifies against system roots with failures naming the peer and mode; off (default) stays plaintext; HTTP-transport executors under required must declare https:// endpoints (2026-06-11, last-mile-stability, artifact).
- Connection-level-only entries; opaque service names; no production branching on specific service names (2026-07-03, 8a8539a4, transcript, user): "rimsky.yml has never been aware of any specific rimsky service implementation."
- Template portability across modes: a node finds its executor by type matching the executor's name in rimsky.yml; under all-in-one with no rimsky.yml the bundled handler serves the same name; the same template file runs byte-identically in both modes (2026-07-01, 8a8539a4, transcript, user).
- Retention defaults (once wired — see corrections): retention.trace_trailing 30d, recent_frames_kept 100, lineage 30d, claim_handles 30d, message_idempotencies 24h; pointer-field loader semantics — explicit 0 disables a sweep, absent key gets the default, negative durations rejected (2026-06-02, rimsky-core-remediation, artifact; defaults extended 2026-06-03, instance-lifecycle-durable-by-default, artifact).
- rimsky.yml shape changes are a minor-bump trigger in the release skill's SemVer inspection (2026-05-27, release-skill, artifact) `(artifact-only)`.

## Intentional absences

- No generic services: block: operator config disambiguates by protocol even though the umbrella service concept exists (2026-05-12, nomenclature-resolution, artifact; Option III also declined 2026-05-04).
- No separate lifecycle_subscribers: block: a peer declares LifecycleSubscriber via the protocols: list on its primary block (2026-05-04, layer-crystallization, artifact).
- No auth keys in rimsky.yml (no auth mode, no bootstrap key): anonymous mode is data-derived (2026-05-15, control-plane-mcp-and-auth, artifact).
- No sensor-state DSN keys: publisher state persistence is each implementation's concern, configured by env var (RIMSKY_SENSOR_<KIND>_STATE_DSN) (2026-05-17, sensor-messaging-unification, artifact).
- No domain stores in rimsky.yml: prompt-context stores are executor-side configuration (2026-05-08, platform-extensions, artifact).
- No opaque per-service config: path field and no allowlist rewrite of rimsky.yml: both the earlier allowlist direction and the interim opaque-config proposal were superseded — per-node declarations plus env-var convention instead (2026-07-03, 8a8539a4, transcript).
- No single-value write_semantics: shortcut: list form required (2026-05-12, artifact).

## Corrections and restorations (drift-fight record)

- The stale "supervisor reads two YAML files" model was removed from the operator guide: RIMSKY_CONFIG is the deployment-shape config; RIMSKY_SUPERVISOR_CONFIG is tuning only (2026-05-04, layer-crystallization, artifact).
- Published template examples had drifted into a fictional DSL and were realigned to real shapes (no template: wrapper; nodes: a list with type:, not a map; dependencies: not deps:); examples must be complete, copy-pasteable, runnable verbatim (2026-05-04, public-docs-architecture, artifact).
- The quickstart stub config still used write_semantics_envelope and the stub parser silently dropped it, falling back to defaults — silent config drift until the rename to write_semantics_allowed made the operator value reach the loader (2026-05-13, nomenclature-resolution, artifact).
- The retention: block was promised (claim_handles_trailing, lineage_trailing) but never parsed — the sweeps were programmatic-only (2026-05-17, post-data-platform-cleanup, artifact); ruled a defect and fixed 2026-06-02 (rimsky-core-remediation): config plumbed, sweeps wired, retention on by default.
- The stores-to-claim_producers rename had stalled after only the top-level config key; the user ordered the full sweep (2026-06-19, transcript).

## Superseded / historical

- stores: parsed as a tolerated alias of claim_producers: with deprecation note (2026-05-04) → alias retired, hard-rejected with a redirect error (2026-05-12, reaffirmed 2026-06-19).
- Legacy single-value write_semantics: accepted as a one-element-envelope shortcut (2026-05-04) → retired; list form required (2026-05-12).
- write_semantics_envelope key name → write_semantics_allowed (2026-05-12).
- sensors: template block and dual-block publisher discovery → publishers: top-level block (2026-05-17).
- late_bind_service_proxies declaration for host-agent late binding (2026-05-24, artifact) — still the containerized-proxy shape; under self-host, --service spawns directly via the hostagent package with no proxy (2026-07-03, transcript; see host-agent dossier).
- Direction to rewrite rimsky.yml configuration as an allowlist, and the interim opaque config: path proposal → dropped; rimsky.yml unchanged for all-in-one, env-var convention for bundled handlers (2026-07-03, transcript).
