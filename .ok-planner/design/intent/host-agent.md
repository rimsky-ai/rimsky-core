# Intent Dossier: host-agent

Distilled 2026-07-13 from session transcripts (2026-06-12..2026-07-13) and ok-planner
history artifacts (2026-05-04..2026-06-11). Transcript tier outranks artifact tier;
later intent supersedes earlier. Part of the drift-remediation intent ledger.

## Net position

- The rimsky-host-agent is a long-running dev-machine daemon bundled into the rimsky CLI binary (`rimsky agent` subcommand). It dials the proxy outbound with the user's api-key (agents never need an inbound route); `rimsky run` auto-starts it when --service is used against a remote deployment.
- The agent is stateless and configuration-free: no capability config, no discovery file, no known-binary list, no persistent state beyond auth. A restart loses in-flight spawn state; pre-restart spawn-ids are dead, detected by the proxy via connection state.
- Spawn lifecycle: lazy birth (first dispatched call for a (run_scope_id, binding_name) pair), per-run-scope lifetime (one spawned process serves all dispatches in that scope; concurrent run-scopes never share process state), push reap on run-scope termination (SIGTERM then SIGKILL after grace, default 30s).
- The RIMSKY_AGENT_PORT child contract is rimsky's standard spawned-binary port handoff: the agent sets it, poll-dials until the child's gRPC server is up, and runs the Capabilities handshake per expected protocol before acking ready; failure yields spawn_failed. Production bundled binaries must honor it (check RIMSKY_AGENT_PORT first, fall back to their own var).
- The proxy exists to bridge a remote long-running stack to a dev box. It is not used in one-shot/local modes: compose run and self-hosted `rimsky run` spawn child binaries directly via the hostagent package (hostagent.SpawnService) — no daemon, no proxy plumbing (2026-07-03, B2', superseding daemon auto-start under self-host).
- Trust posture is permissive by default (anyone with the api-key spawns as the user — SSH-key-equivalent), narrowed optionally by --allow-paths globs.
- The host-agent-to-supervisor heartbeat is a live, intentional surface — distinct from the retired executor-liveness heartbeats.

## Required behaviors (open promises)

- `rimsky run --service <name>=<path>` end-to-end: CLI ensures the agent is running, posts per-instance service_bindings, binaries spawn on the user's machine in the supplied cwd, held for the run-scope lifetime, reaped on close (2026-05-24, host-agent-and-proxy, artifact): "the binary runs on their machine, in the folder they pointed at, with no infrastructure setup, and gets cleaned up automatically."
- Child contract: RIMSKY_AGENT_PORT set in the child env; agent probes until the gRPC server is up (bounded by ready-timeout); Capabilities handshake per expected protocol before SpawnAck ready; any failure → spawn_failed (2026-05-24, artifact).
- Bundled production service binaries honor RIMSKY_AGENT_PORT first with fallback to their own port var — the intended contract is real design; once honored, the concept can be documented honestly (2026-06-21, ecde6dd1, transcript).
- Per-run-scope isolation: one spawn per (run-scope, binding), reaped on that run-scope's termination only; requires run_scope_id on the dispatch wire (ExecuteRequest and OpenRequest) (2026-06-06, comprehensive-gap-closure, artifact — correcting the shipped instance-id keying).
- All five protocols late-bindable through the proxy — executor, claim-producer, publisher, validation, data-processing — no protocol left as a gRPC Unimplemented stub (2026-06-08, corpus-bootstrap, artifact).
- Per-binding overrides: env vars, command args, working directory, ready/spawn-timeout on the Binding proto message, applied at exec; a binding with no overrides inherits env and global cwd/timeout (2026-06-06 + 2026-06-08, artifact).
- Anonymous-mode late-binding: dispatch must not terminate host_agent_not_connected merely because the instance owner is NULL (2026-06-08, corpus-bootstrap, artifact).
- Agent lifecycle verbs: `rimsky agent start/status/stop`; stop reaps all spawned children; status truthfully reports connection state, proxy endpoint, and spawned children (2026-06-08, corpus-bootstrap, artifact).
- cwd is an instance-level parameter under the well-known key `cwd` (--param cwd=. sugar), read at first-spawn for a (run-scope, binding) pair; absent → child inherits the agent's cwd; templates without cwd work (2026-05-24, artifact).
- Loopback-only local HTTP listener by default (OS-assigned ephemeral port, --listen override), reported as local_callback_base_url in the Register frame — a deliberate security choice (2026-05-24, artifact).
- Callback URL rewrite: the proxy rewrites callback_url host:port to the agent's local_callback_base_url before tunneling — the only URL the proxy touches; the agent does not transparently proxy arbitrary localhost traffic (2026-05-24, artifact).
- Crash recovery: spawned-process crash → executor_crashed, fresh spawn on next dispatch; agent disconnect → in-flight dispatches killed with host_agent_disconnected, orphaned children SIGKILLed on reconnect-recovery; proxy restart → agents reconnect with backoff; supervisor crash invisible to the proxy (orphan-reaper reclaims rows) (2026-05-24, artifact).
- `rimsky compose run` supports --service with identical semantics to rimsky run (same alias resolution, same RIMSKY_AGENT_PORT contract, spawn/ready-poll/port/cleanup owned by rimsky-core) (2026-06-13, 65667e33, transcript, user: "since the flag is there, might as well").
- compose run must not leak spawned services: after the run exits, spawned processes are gone — proven by a scenario test asserting kill -0 reports process-not-found for the spawned PID (2026-06-14, f0176bde, transcript).
- Same-key displacement: latest Register wins; older connection gracefully closed; new RegisterAck carries displaced_prior=true (2026-05-24, artifact).
- E2E fidelity: scenario tests exercise the real proxy binary exec()ed as a real gRPC server with a real stub child honoring RIMSKY_AGENT_PORT — the real dispatch path, not an in-process fake (2026-05-24, divergences, artifact).

## Intentional absences

- Host-agent daemon under self-host: superseded — self-hosted `rimsky run` --service spawns directly via hostagent.SpawnService (B2'), no daemon, no proxy (2026-07-03, 8a8539a4, transcript, user: "B2'").
- The proxy in one-shot mode: not used — it bridges a remote stack to a dev box; in one-shot the supervisor is already local (2026-06-13, 65667e33, transcript).
- Deferred out of v1 by design: pool-routed bindings, long-running pinned bindings (attach to an already-running process), a host-agent conformance binary, sandboxing beyond --allow-paths, internal-service auth between rimsky processes, a synthetic error class for unreachable services, explicit multi-agent routing (2026-05-24, artifact). (Per-binding env/args/cwd/timeout overrides were on this deferral list but were un-deferred and promised 2026-06-06.)
- Capability/discovery config on the agent: deliberately stateless (2026-05-24, artifact).
- cwd in the binding shape: it is an instance param, not part of the binding (2026-05-24, artifact).

## Corrections and restorations (drift-fight record)

- The documented RIMSKY_AGENT_PORT contract was honored only by test stubs — zero bundled production binaries read it (sensor-cron read RIMSKY_SENSOR_CRON_PORT; claude-agent read RIMSKY_EXECUTOR_PORT_GRPC), so `rimsky run --service` with any bundled binary silently failed the readiness poll with spawn_failed. Ruled: the contract is real design; fix the binaries (RIMSKY_AGENT_PORT first, fallback to own var), then document honestly. The CLAUDE.md gotcha claiming the contract was honored was drift (2026-06-21, ecde6dd1, transcript).
- The shipped proxy keyed spawns on instance id, contradicting the documented per-run-scope invariant; ruled fix-code — run_scope_id added to the dispatch wire and spawns re-keyed (2026-06-06, comprehensive-gap-closure, artifact).
- Supervisor→executor heartbeat remnants were removed as incomplete-cleanup residue; the host-agent→supervisor heartbeat was explicitly ruled a different surface that stays (2026-06-24, 8a8539a4, transcript).
- Verb-inference wire shortcut acknowledged: the DispatchFrame carries only a protocol name, so the agent infers the claim-producer verb from request shape (OpenRequest first; Commit/Abandon/Release forwarded as Commit — wire-compatible at claim_id). A noted-in-code compat shortcut forced by the frame design, not silent drift (2026-05-24, divergences, artifact).

## Superseded / historical

- B1 (auto-start the host-agent daemon alongside the self-hosted stack) → B2' direct in-process spawning via the hostagent package (2026-07-03, transcript).
- Proxy Publisher/Validation/DataProcessing handlers as registered UNIMPLEMENTED stubs (2026-05-24) → all-five-protocols promise, no stubs (2026-06-08).
- Instance-id spawn keying → (run-scope, binding) keying (2026-06-06).
- License split recorded: hostagent code Apache (client-side, CLI-bundled); proxy binary and its migration SQL AGPL (platform-side) (2026-05-24, divergences, artifact).
