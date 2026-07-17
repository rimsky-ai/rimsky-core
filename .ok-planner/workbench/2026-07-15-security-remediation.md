# Security Remediation Track

A **separate track** from the drift remediation, worked on the
**security-cleared / higher-capability model** (dual-use security work belongs
on that tier). Paired ledger: `review-findings-security-2026-07-06.csv` (40
rows). This doc is self-contained and resumable from a clean context.

Do NOT fold this track into the drift-remediation plan or a general Fable
context — its material is what trips the routing classifier, and keeping it
isolated is the whole point of the split.

## Status

40 rows total. **36 fixed, 4 accepted, 0 open.** The claude-agent executor
cluster is fully closed (9 fixed + 2 accepted); the core validation-bypass
cluster is fully closed (3 fixed); the SSRF/open-egress pair is fixed (2); the
filesystem claim-producer canonicalization pair is fixed (2); the
mechanically-obvious batch (injection/race/escaping/coverage) is fixed (10); the
auth-posture judgment cluster is fully closed (8 fixed + 2 accepted). The whole
ledger is now resolved.

### Auth-posture cluster — CLOSED (10 rows), commit(s) pending user approval

Decided the posture with the user across a design conversation, then implemented
via file-disjoint security-cleared subagents + central verification. The posture:
rimsky has four trust boundaries — control plane (api-key + TLS, unchanged),
internal service↔service (now optionally mutual-TLS), bundled-service web ingress
(webhook/agent), and outbound (service→third-party). "Everything is a key"
(no user entity; service principals are api-keys). New capability: `peer_auth:
none|mtls` (default OFF) — a deployment CA in the control plane (key encrypted at
rest, AES-256-GCM / `RIMSKY_CA_ENCRYPTION_KEY`), services enroll via an api-key
carrying a new `service:enroll` permission (`POST /v1/enroll`), receive a 24h
memory-only leaf cert auto-renewed at ⅔ TTL, and authenticate mutually. The
ad-hoc callback run-token + executor-chosen `async_ack_id`-as-credential were
swept, replaced by mTLS peer identity (`async_ack_id` stays a correlation key).

Row dispositions:
- `1712` FIXED — agent→proxy TLS (pinned-CA verification; api-key stays the
  identity, no client cert; agent is per-user tooling not an enrolled service).
- `1883` FIXED — http-node HTTP dispatch bridge honors mTLS under `peer_auth`;
  bridge port no longer `EXPOSE`d by default. (It's a boundary-2 surface — the
  supervisor's own HTTP executor client dials it, not a public endpoint.)
- `1947` + `2025` FIXED — webhook sensor requires per-subscription auth
  (`hmac`/`secret_header`/`none`), fail-LOUD on omission; e2e flipped to assert
  an unsigned POST is rejected.
- `1967` FIXED — openlineage subscriber presents an optional outbound bearer.
- `1719` FIXED — concept doc's stale "active-context config" auth-source line
  corrected to `RIMSKY_API_KEY` / `--api-key`.
- `2129` FIXED — operator-invalidate test reseeded through the real
  `POST /v1/instances/{id}/debug/override` route; wall-clock verdict removed.
- `2091` FIXED — added the missing negative test (unknown api-key → registration
  refused with `Unauthenticated`).
- `82` ACCEPTED (stale) — proxy self-asserted identity was already closed by the
  earlier `1` fix (whoami verification); the `2091` negative test now covers it.
- `2178` ACCEPTED (refuted) — asset actions declare no `ScopeDimensions`, so they
  authorize via the uniform action-permission gate; the generic out-of-scope
  denial is already tested. No asset-specific hole.

Also built (posture support): shared enroll wire-client `lib/protocols/enroll`;
core-side `lib/runtime/peer` mutual-TLS extensions (client cert, `TLSServerConfig`,
`IdentityHolder`, renewal); services-side `lib/services/internal/peerauth`; the
deployment CA + migration 022 (both backends); full design-corpus update
(`concept:peer-auth` + 8 concepts, 6 decisions, `tension:internal-service-auth-
unspeced` resolved, stories + intent).

Two pre-existing failures found and fixed (overshoot):
- `TestHttpNodeCrossStack` — broken since the SSRF-guard commit `63b26c68`; its
  loopback upstream was blocked by the default-closed egress guard. Fixed by
  allowlisting loopback in the test's http-node env (127.0.0.0/8).
- `TestClaudeAgentPerNodeDivergence` — broken by the `1840` stdio-block; worker-
  beta declared a `stdio` MCP server under a closed allowlist (forbidden). Fixed
  by switching beta to `http` transport (mirrors alpha) — preserves the per-node
  divergence proof.

One judgment call, operator-CONFIRMED: `service:enroll` is `IsWrite: false` (a
compute-and-return that persists nothing, like `template:validate`), so it's
outside the dry-run mutation-preview guarantee.

Host-agent loopback — REDESIGNED after the operator discussion. The `1712`
per-spawn ad-hoc secret (env `RIMSKY_AGENT_SECRET` + `X-Rimsky-Agent-Secret`
header + a planned nonce challenge) was recognized as an ad-hoc reimplementation
of mutual TLS and BACKED OUT entirely. Replaced by mandatory, always-on mutual
TLS on the agent↔child loopback, administered by the daemon as a self-contained
LOCAL enrollment authority: on startup the daemon generates a local CA (reusing
`pkg:lib/foundation/pki`) — a trust domain separate from the deployment CA — and
serves a plaintext bootstrap `route:POST /v1/enroll`; per spawn it mints a
bootstrap token and provisions the child with `env:RIMSKY_PEER_AUTH=mtls` /
`env:RIMSKY_API_KEY=<token>` / `env:RIMSKY_CONTROL_API_URL=<daemon's local enroll
base>`; the child self-enrolls (unchanged executor code — reuses
`pkg:lib/services/internal/peerauth`), and agent↔child dispatch + callback run
mutual mTLS against the local CA. The daemon needs NO rimsky permission and mints
no ledger keys (the loopback trust is entirely local). A plaintext-only binary is
no longer a valid late-bound executor (pre-v1 contract change). This decouples
the loopback from the deployment `peer_auth` (it's always on locally) and closes
the port-squat dispatch-interception, forged-callback, and forged-dispatch cases
uniformly. Two local listeners (plaintext enroll vs mTLS callback); callback base
is now `https://localhost:<port>` for SAN validation. Verified: `-race -count=3`
on hostagent + proxy + enroll, full root + services suites, lint/plumbline clean.

Pre-commit adversarial review (4 security-cleared agents over the risk surface)
found 2 real defects that green tests had hidden, plus hardening — all FIXED:
- HIGH: the core `peer` client trust root was set only by a test hook, so a
  production mtls deployment ran with `RootCAs=nil` → outbound dials verified
  against the system pool (deployment certs rejected AND public PKI trusted, a
  MITM surface). Added a production setter, wired at role startup from the
  deployment CA; fixed the dial-before-identity ordering in `StartSupervisor`.
- MAJOR: the webhook HMAC signed only the body, leaving the replay window
  decorative. Now signs `timestamp + "." + body` and requires the timestamp
  header in hmac mode (fail-loud at bind).
- Loopback: pinned the plaintext enroll listener to loopback (was honoring an
  operator-supplied off-host `ListenAddr`, leaking the bootstrap token over the
  LAN); bounded the bootstrap token's lifetime (child re-enrolls with the same
  token at renewal, so single-use would break it — 24h sliding window instead).
- Callback principal binding (operator-elected hardening): the callback mTLS gate
  discarded the peer principal, so any deployment-cert holder who learned an
  `async_ack_id` could settle that run. Now the dispatched-to principal is
  captured at dispatch (from the executor mTLS handshake), persisted with the
  async-ack (migration 023), and the callback is rejected on principal mismatch.
- Symmetric `ClientCAs != nil` guard on the callback listener; 1-minute cert
  clock-skew backdate. Re-verified: build/lint/plumbline clean; changed paths
  `-race`; migration-023 conformance on both backends; full scenario suite green.

Review scoreboard: 14 findings → **11 fixed, 3 accepted**. Accepts: dead `label`
field (shared wire type, harmless); anonymous-agent displacement (reachable only
in no-auth anonymous mode); flat local loopback trust domain (matches the same-UID
boundary — a finer per-child check is defeated by the same access it would guard).
Finding 12 (webhook secret at rest) split: sensor-side FIXED (the sensor now
persists only its watermark, never the secret — rimsky re-provisions config via
subscription resync); rimsky-side DELEGATED per `decision:secret-at-rest-posture`
(config-blob secrets rely on operator infra encryption + restricted DB access +
encrypted backups, never logged / never returned over API; app-level field
encryption reserved for the CA key). Sensor #12 re-verified via a fresh-image
webhook e2e; #8/#11 fixes verified `-race` on the touched paths.

Verification: `go build` + `make lint` + `plumbline` clean across all modules;
full root + services test suites green (incl. the testcontainers docker harness
against freshly-rebuilt images); race-sensitive packages green under `-race`.

- **2 fixed in Track 0b** of the drift work (id `1` proxy Register auth, `1801`
  unguarded schema drop/rename).
- **8 fixed in the claude-agent executor cluster** (commit `4a9df8f8`): `1834`
  (crit) argv flag-injection guard, `1835` callback-token-off-argv (stdin),
  `1849` prompt ARG_MAX (stdin), `1838` resume carries restrictions+budget,
  `1841` retry-leg rate-limit park, `1856` requests-reset header, `1860`
  module-loopback bearer gate, `2041` faithful rate-limit fake + park assertion.
  All with regression tests; verified `-race` + fresh-image cross-stack green.
- **`1840` FIXED (user ruling A, commit `80c40a19`):** the MCP allowlist pinned
  name only, so a node could run an arbitrary stdio `command` under an
  allowlisted name. `resolveHostServers` now refuses `stdio` transport whenever
  the allowlist is closed (set); `http`/`module` still allowed, open mode
  unchanged. Regression tests + fresh-image cross-stack green.
- **`1843` ACCEPTED (user ruling A, 2026-07-15):** OAuth token via
  `CLAUDE_CODE_OAUTH_TOKEN` env stays. Env is the CLI's OAuth channel; a
  bypassPermissions Bash agent can read any credential the CLI holds (incl. the
  api-key file), so env-vs-file is marginal; `1840` removed the stdio-child
  inheritance vector under a closed allowlist. A file-based move needs a
  real-CLI OAuth test this repo can't run — deferred, not guessed.
- **`1874` ACCEPTED (user ruling A, 2026-07-15):** callback-token registry has
  no TTL. Token is a random UUID on a loopback random port; a TTL is an arbitrary
  wall-clock constant that would false-kill long/quiet runs, and an exploitable
  token is exploitable within any window. Real defense = keep protocol endpoints
  private in operator infra; a per-node liveness bound
  (`cli.silence_timeout_ms`/`cli.tool_use_timeout_ms`) already tears down a
  wedged run. If it ever becomes a genuine surface, derive the token as a hash of
  an executor-held secret rather than adding a TTL.

- **Core validation-bypass cluster FIXED (3 rows):**
  - **`1329`** author-set internal flag: `is_subgraph_entry_absorbed` (and its
    sibling `is_subgraph_exit`) round-trip from author YAML, so a flat-form
    template could set the flag and skip the executor/delegate mutual-exclusion
    check (only the canonicalizer's `absorbEntryIntoCaller` gate covers the
    legit absorbed case, and it runs for graphs-form only). Fix:
    `rejectAuthorSetInternalFlags` runs before `canonicalizeGraphs` and rejects
    either flag on any author-supplied node (flat `nodes:` or `graphs[].nodes:`);
    the canonicalizer still sets them post-check, so the legit path is untouched.
  - **`1504`** async-callback tag-validation bypass: the async terminal path
    never ran `validateTags`, and `CallbackServer`/`runArgs` never carried
    `DeclaredTagsFor` at all. Fix: wired `DeclaredTagsFor` through the callback
    server + `runArgs`, and `driveTerminal` now runs `validateTags` before
    applying the terminal — an undeclared tag becomes `executor_protocol_violation`,
    matching the sync path.
  - **`1631`** commit-validation bypass: `PhaseCommit` validation was gated on
    `t.Changed`, but the delta is merged and persisted regardless, so
    `changed=false` + a non-empty delta committed unvalidated attributes. Fix:
    gate on the delta (`len(t.AttributesDel) > 0`), not on `Changed`, via a
    shared `validateCommitWriteback` helper. **Overshoot:** the error-terminal
    path (`applyTerminalError`) persisted its delta with *no* commit gate at all
    — the same invariant-12 violation — so it now runs the same helper and
    refuses to persist an invalid writeback (emits `attributes_schema_failed`,
    keeps the executor's error class).
  - Regression tests: three template-validator rejections (flat + graphs form,
    plus the legit graphs-form path still validates clean); two Postgres-backed
    async-callback integration tests driving the real HTTP path (undeclared tag
    → run fails; `changed=false` invalid delta → not committed + run fails).
    Verified `-race`, lint, full core+cmd suite, and the subgraph/attribute/
    terminal scenario suites green.

- **SSRF / open-egress pair FIXED (2 rows):** `1882` (http-node) and `1928`
  (sensor-http) both dialed caller/template-supplied URLs with a default-transport
  client — no scheme restriction, no private/link-local (169.254.169.254 metadata)
  block, no allowlist — and fed the response back into rimsky. Fix: one shared
  `lib/services/internal/egress` guard builds an `*http.Client` whose dialer
  `Control` checks the **resolved IP at connect time** (blocks
  loopback/private/link-local/ULA/unspecified/multicast, also defeating DNS
  rebinding and re-checking redirect targets) and whose RoundTripper restricts
  the scheme to http/https. Secure by default (unset env = block all non-public);
  operators opt specific CIDRs back in via `RIMSKY_EXECUTOR_HTTP_NODE_EGRESS_ALLOWLIST`
  / `RIMSKY_SENSOR_HTTP_EGRESS_ALLOWLIST`, parsed fail-closed at startup. In
  sensor-http only the **poll** client is guarded; the publish-to-control-API
  client stays unguarded (trusted, often private). Regression tests: egress unit
  suite (metadata/private blocked, public allowed, allowlist exception, non-http
  scheme rejected) + per-service negative tests (http-node refuses the metadata
  endpoint; sensor-http's guarded poll client refuses a loopback target that
  would otherwise match). The services harness sets the allowlist so its
  host-gateway poll still works; verified against **freshly rebuilt** sensor-http
  + http-node images (sensor/portable/bundled/smoke scenarios green), full lint.

- **Filesystem claim-producer canonicalization pair FIXED (2 rows):** `1745`
  (major, CONFIRMED) and `1774` (minor) both broke the byte-equal conflict
  predicate by emitting non-canonical scope bytes. `1745`: the two SplitScope
  fan-out sites (`splitExpandFolder`, `splitListArray`) marshaled **absolute**
  paths into `claim_scope_data` while `Open` and `BatchPop`/`popOne` marshaled
  **relative** paths, so a fan-out sub-claim on a file and a direct `Open` of the
  same file never collided (two concurrent rw claims undetected), and
  `server_test.go` asserted the absolute shape. `1774`: canonicalization was
  purely lexical, so on a case-insensitive FS (macOS/APFS dev) `Docs/A` and
  `docs/a` emitted different bytes for the same file. Fix: one `Store`
  canonicalization method (`root-relative → clean → fold if case-insensitive →
  marshal`) routes all four emission sites; a probe at `New` sets `caseFold` only
  when the root's FS is case-insensitive (so case-sensitive production is
  byte-for-byte unchanged); addresses stay absolute; `findByScope` was made
  fold-aware so the Commit/Abandon reverse lookup still matches on a
  case-insensitive FS. **Overshoot:** folding forced `findByScope`
  (`pick_policy.go`) to fold its policy-root/on-disk-folder comparison too, else
  a restart-fallback Commit would fail to clear an in_progress entry (queue
  livelock) — fixed in the same change. Regression tests: a fan-out sub-scope
  byte-equals a direct `Open` of the same file (flipping the absolute assertion +
  a root-relative list-shape assertion); a deterministic store-package fold test
  whose verdict is host-FS-independent. Verified: `lib/services` build + vet +
  full `make lint` green; the filesystem claim-producer **docker** scenarios
  (incl. pick-vs-scope + cross-queue concurrency) and the stores smoke test green
  against a **freshly rebuilt** `rimsky-claim-producer-filesystem:latest`.

- **Mechanically-obvious batch FIXED (10 rows):** the injection/race/escaping/
  coverage findings whose correct behavior was not in question — fixed in one
  file-disjoint parallel pass (per-file subagents on the security tier, verified
  and committed centrally). All with deterministic regression tests.
  - **`1949`** (major) unbounded `io.ReadAll` on the unauthenticated webhook
    endpoint → `http.MaxBytesReader` at a named 1 MiB cap, 413 on overflow.
  - **`1956`** instance-id from the unauthenticated Subscribe surface
    interpolated into the message-send URL path unescaped (path traversal to
    other control-api routes) → `url.PathEscape` on the untrusted segment.
  - **`1703`** `CanonicalizeSendMessageSugar` duplicated the `send_message` kind
    literal across the graph/runtime layer boundary and silently no-op'd on an
    unregistered alias (failing at dispatch, not deploy). Relocated the canonical
    string to `lib/foundation/spec` (the common importable ancestor —
    `graph-purity` forbids graph→runtime), so both sides resolve one symbol
    statically; the unregistered-alias case now errors at canonicalize/deploy.
  - **`1713` + `377`** `pathAllowed` glob-matched the un-canonicalized binding
    path, so a symlink under an allowed dir or a relative/unclean pattern escaped
    the allowlist → canonicalize both sides (`Abs`→`Clean`→`EvalSymlinks`, fail
    closed on unresolvable) before matching, and execute the resolved path to
    close the validate-vs-exec gap.
  - **`1724`** free-port TOCTOU (probe listener closed before the child binds,
    spurious `spawn_failed`) → bounded 3-attempt re-pick on a child-didn't-bind
    sentinel, port source injected for a deterministic collision test.
  - **`1494`** breakpoint queue-cap check + insert in separate txns →
    concurrent evaluators could exceed cap. Added `Lock` to the breakpoint table
    (`SELECT … FOR UPDATE` on Postgres, immediate-txn on SQLite); cap-check +
    conditional insert now one serialized txn. Proven with a real-Postgres racer
    test (the load-bearing one — MVCC would slip without the lock).
  - **`1299`** (major) frame-end TOCTOU (`ended_at` stamped without re-checking
    the no-in-flight predicate in-tx) → added `EndFrameIfSettled` (both backends):
    a `FOR UPDATE` re-check + conditional stamp in one txn that conflicts with the
    FK lock every run-insert takes, so a concurrent run can't land in a just-ended
    frame. Raw `MarkFrameEnded` kept for the test scaffolding that force-ends.
  - **`2177`** (major, CONFIRMED) last-active-key revoke TOCTOU was real — two
    concurrent revokes of distinct last keys both passed the `active<=1` guard and
    dropped the deployment to anonymous mode. **Fixed at root:** added
    `RevokeIfNotLast` (count-active `FOR UPDATE` + revoke in one txn, consistent
    lock order); `handleRevokeKey` routes through it. Barrier-synced concurrent
    regression test + a conformance subtest across both backends.
  - **`2179`** audit payloads never asserted to exclude bearer plaintext →
    added the assertion; it **passes against current code** (the audit-write path
    only persists request bodies, never the `Authorization` header), so no leak —
    the test locks the property in.
  - **Flake fix (overshoot, `rules.md` "Fix Every Bug You Find"):** the retry
    loop for `1724` pushed `TestSpawnService_ReadyTimeoutReapsChild`'s
    never-binding path from ~200ms to ~3.5s against a `elapsed > 5s` wall-clock
    verdict — a load-dependent pass/fail rules.md forbids. Removed the timing
    verdict; the deterministic signal assertions stay and the suite `-timeout` is
    the hang backstop.
  - Verified: both modules build; `make lint` (all modules) clean; the full
    changed-package set + persistence conformance + auth scenarios green under
    `-race`; the two sensor images (`rimsky-sensor-webhook`,
    `rimsky-sensor-object-store`) **rebuilt fresh** and their e2e scenarios green.

## Approach

Most of these are **intent-independent defects** — an auth bypass, an injection,
an SSRF, a secret exposure, a TOCTOU race is wrong regardless of design intent,
so it's a direct `fix-code` with a regression test; no dossier consultation
needed. A few are not code fixes:
- `1719 concept-drift-auth-source` — a design-doc drift (`fix-doc`, reconcile
  `concept:host-agent` against the auth dossier).
- Test-side rows (`2025`, `2091`, `2129`, `2177`, `2178`, `2179`, `2041`) —
  missing/blessing-by-omission auth coverage or a test-double divergence
  (`fix-test`, add the negative/coverage assertion; `2091` "auth-bypass blessed
  by test" means a test currently ratifies a bypass — flip it).

Verification per `.claude/rules/rules.md`: `go build ./... && go test ./... &&
make lint`, plus `make test-all` (or the specific rebuilt image) for any
core/service change — the stack suites only prove current source against fresh
images. Add `-race` for the TOCTOU/race rows.

Disclosure: rimsky is **pre-v1 with no deployed consumers** (`rules.md` "Pre-v1
— break freely"), so external disclosure risk is low and fixes can be direct
(no compat shim, no embargo). Keep the security material in this track/ledger;
commit messages describe the fix plainly.

## Work breakdown (by theme — natural batch clusters)

- **claude-agent executor (largest cluster)** — argv/injection + token + rate-limit:
  `1834` argv-flag-injection-allowlist-bypass (major), `1835` callback-token-leak-via-argv,
  `1849` prompt-via-argv-arg-max, `1838` resume-drops-restrictions (major),
  `1840` allowlist-pins-name-only, `1843` oauth-token-env-exposure,
  `1860` auth-posture, `1841` retry-rate-limit-undetected, `1856` rate-limit-header-gap,
  `1874` token-registry-no-expiry, `2041` test-double-divergence-rate-limit.
  All in `lib/services/executors/claude-agent/` (+ its fake CLI). Batch together.
- **SSRF / open egress** — `1882` http-node server, `1928` sensor-http (both major).
  External-dial services with no egress control. Batch together.
- **Filesystem claim-producer canonicalization** — `1745` claim-scope-canonicalization-drift
  (major), `1774` case-insensitive-scope-canonicalization. Scope-confusion class.
- **Host-agent / proxy** — `82` (proxy, minor follow-on to the fixed `1`),
  `377` spawn, `1712` plaintext-api-key-no-tls (major), `1713` allow-paths-not-canonicalized,
  `1724` free-port-toctou, `2091` auth-bypass-blessed-by-test. `lib/runtime/hostagent/`,
  `cmd/rimsky-host-agent-proxy/`, and the scenario harness.
- **Webhook sensor** — `1947` no-webhook-authentication (major), `1949` unbounded-request-body-read
  (major), `2025` missing-auth-coverage (test). `lib/services/sensors/sensor-webhook/`.
- **Core validation bypass** — `1329` validation-bypass-author-set-internal-flag
  (major, template validator), `1504` async-callback-tag-validation-bypass (major),
  `1631` commit-validation-bypass (major). Core runtime/graph.
- **TOCTOU races (core)** — `1299` frame-end-toctou-race (major), `1494` queue-cap-toctou.
  Add `-race`.
- **Other single rows** — `1703` silent-noop-canonicalization (kind_resolver),
  `1719` concept-drift-auth-source (fix-doc), `1883` unauthenticated-execute-bridge
  (http-node), `1956` instance-id-unescaped-in-url (object-store sensor),
  `1967` no-backend-auth (openlineage subscriber), `2129` bypasses-operator-surface (test),
  `2177` missing-negative-test-toctou (test), `2178` asset-authz-bless-by-omission (test),
  `2179` missing-secret-redaction-assertion (test).

## Fleet / model discipline

- Fix in **small file-disjoint batches** (the same batches-of-~4 pacing as the
  drift work). If dispatching subagents, they MUST run on the security-cleared
  model (pass `model: opus` — or the current security tier — on the Agent call);
  a Fable subagent may refuse or mishandle dual-use security material.
- Agents leave changes UNSTAGED; the orchestrator verifies and commits. Agent
  prompts MUST forbid destructive git ops (`git checkout`/`reset`/etc.) and
  require deterministic tests (no wall-clock verdicts, per `rules.md`).
- Each fix ships with a regression test proving the hole is closed (a negative
  test: the attack/misuse now fails). For the "blessed-by-test" rows, the fix
  includes flipping the test that currently ratifies the bypass.

## Verification & commit

Per-batch: `go build ./... && make lint`, package tests, `-race` for race rows,
and `make test-all` (or the specific rebuilt service image) for core/service
changes. Read real exit codes from files, not the background-task wrapper; a
whole-package `panic: test timed out` is a hang, not a failure (see the
drift-remediation plan's Environment discipline). Commit per batch or per
coherent cluster.

## End state

All 40 security rows closed as `fixed` or `refuted`; each fix guard-tested;
the security ledger is the durable record. This doc archives to
`.ok-planner/history/` when the track completes.
