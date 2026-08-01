# Intent Dossier: discovery-cache

Distilled 2026-07-13 from session transcripts (2026-06-12..2026-07-13) and ok-planner
history artifacts (2026-05-04..2026-06-11). Transcript tier outranks artifact tier;
later intent supersedes earlier. Part of the drift-remediation intent ledger.

## Net position

- The discovery cache is an **in-memory, per-peer Capabilities cache** populated by the startup observability handshake. The handshake is strictly best-effort: unreachable peers are recorded as Unreachable and never abort startup. A refresh loop makes reads eventually consistent and flips dead peers to unreachable; restart resets to a fresh handshake pass (2026-05-11, corroborated 2026-06-02).
- Its job is to decouple template-registration latency from peer availability: it feeds the registration and dispatch validators with each service's advertised capabilities (2026-05-11, 2026-06-02).
- The production refresh interval default is 60s and is **deliberately kept**; the services test harness overrides it to 5s via `RIMSKY_OBSERVABILITY_REFRESH_INTERVAL` (2026-06-17, transcript).
- Registration-time laxness is **opt-in and explicit, never silent**: template-level `late_bind_services: [...]` names bypass existence checks against the cache and expected-attributes schema validation; unlisted names retain strict behavior; the default empty list is strict (2026-05-24).
- In-process registries parallel the gRPC path: bundled executors and claim producers register in-process at all-in-one boot, and **bundled discovery entries are static so the refresh loop cannot wipe them**; config-declared names always win over bundled in-proc handlers (2026-07-03, transcript).
- Calling-side wire code (rimsky's gRPC clients toward services) is rimsky-internal — root module `runtime/peer`, never the SDK (2026-05-24).

## Required behaviors (open promises)

- Best-effort startup handshake: "unreachable peers are recorded as Unreachable and never abort startup"; in-memory only, refresh-loop eventual consistency (2026-05-11, log-convergence, artifact; corroborated 2026-06-02, acceptance-coverage-recovery).
- Startup probes each service's observability gRPC protocol and caches advertised capabilities (declared events, expected_attributes_schema); "the cache feeds registration + dispatch validators"; refresh loop flips a dead peer to unreachable (2026-06-02, acceptance-coverage-recovery, artifact). Note: the declared-events portion is affected by a later transcript retirement — see Superseded below.
- `late_bind_services:` opt-in bypass — "Default is empty list → strict registration matches today's behavior. Opt-in laxness, not silent." (2026-05-24, host-agent-and-proxy, artifact) (artifact-only)
- Claim producers may declare `declared_error_classes` in their capabilities response, stored in the cache at handshake; declaring nothing remains legal. The template validator range-checks `error_types:` keys against the union of executor-declared classes, the acquire/* synthetic family, and the declared classes of every reachable producer — advisory warning on unknown keys, never a hard rejection (2026-06-11, last-mile-stability, artifact) (artifact-only).
- In-process claim-producer registry (lib/runtime/claimproducer) parallels the executor in-proc registry: registration binds name to handler plus explicit mix-in advertisement and capabilities as construction data; handed-out clients satisfy the same consumer-facing interfaces as gRPC peer clients with envelope enforcement to parity; calling an unadvertised mix-in errors naming the producer and verb (2026-07-03, 3f71f90a, transcript).
- Bundled in-proc registration semantics at all-in-one boot: unconfigured services skip with a log line; present-but-invalid config aborts boot naming the handler; config-declared names win over bundled handlers; bundled discovery entries are static — the refresh loop cannot wipe them (2026-07-03, 3f71f90a, transcript).
- Test-harness discovery convergence: `RIMSKY_OBSERVABILITY_REFRESH_INTERVAL=5s` in the harness, 60s production default unchanged (2026-06-17, 9fb55f08, transcript).

## Intentional absences

- **Persistent/durable discovery state** — the cache is in-memory by design; restart means a fresh handshake pass (2026-05-11).
- **Registration hard-failure on unreachable peers** — never; unreachable is a recorded state, not an abort (2026-05-11).
- **Calling-side wire code in the SDK** — rejected; it is tightly coupled to the supervisor, terminal-resolution, and the discovery cache, and stays in the root module, renamed runtime/remote → runtime/peer because "remote" wrongly implied an external-facing surface (2026-05-24, repo-reorganization).

## Corrections and restorations (drift-fight record)

- **Unsanctioned `execSchemaVisible` laxness** (2026-05-21, userdata-collapse-into-attributes-divergences, artifact): `checkAttributesSchema` was implemented with an execSchemaVisible boolean the spec never sanctioned — when the executor's expected schema is not visible at registration, the "source or default or readOnly" rule and the readOnly-authorship rule are skipped, letting a structurally broken template pass registration; the dispatch path is *claimed* to re-apply the rule. Recorded as a divergence, not a ratified decision.

## Superseded / historical

- `on_event` validation against the peer's `Capabilities.declared_events` at registration, with silent no-op for unknown names when the peer was unreachable (2026-05-11, log-convergence, artifact) — **"declared-events" appears on the 2026-07-11 transcript retired-mechanisms list** (see _retired dossier). The transcript tier outranks: doc or code presenting declared-events as current is drift.

## Conflicts needing human ruling

- **RESOLVED 2026-07-14 (user ruling, transcript tier): per-name strictness only — the global ref_validation_mode knob is retired.** A name listed in `late_bind_services` is exempt from presence/capability/schema-visibility checks at registration (that is the list's meaning; its schema arrives at late-bind time); every unlisted name must be present AND schema-visible, full stop. The `templates.ref_validation_mode` (all/available/none) knob and the execSchemaVisible skip it gates are retired erase-completely — the "not provisioned yet" case is what the explicit per-template list is for, and a deployment-wide escape hatch that silently weakens every template contradicts the 05-24 promise. Findings: schema-skip-under-available behavior is a defect; missing strictness for unlisted names is a defect; leniency for listed names is correct.
