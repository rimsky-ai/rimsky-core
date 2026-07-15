# Security Remediation Track

A **separate track** from the drift remediation, worked on the
**security-cleared / higher-capability model** (dual-use security work belongs
on that tier). Paired ledger: `review-findings-security-2026-07-06.csv` (40
rows). This doc is self-contained and resumable from a clean context.

Do NOT fold this track into the drift-remediation plan or a general Fable
context — its material is what trips the routing classifier, and keeping it
isolated is the whole point of the split.

## Status

40 rows total. **10 fixed, 3 awaiting a design ruling, 27 open.**

- **2 fixed in Track 0b** of the drift work (id `1` proxy Register auth, `1801`
  unguarded schema drop/rename).
- **8 fixed in the claude-agent executor cluster** (commit `4a9df8f8`): `1834`
  (crit) argv flag-injection guard, `1835` callback-token-off-argv (stdin),
  `1849` prompt ARG_MAX (stdin), `1838` resume carries restrictions+budget,
  `1841` retry-leg rate-limit park, `1856` requests-reset header, `1860`
  module-loopback bearer gate, `2041` faithful rate-limit fake + park assertion.
  All with regression tests; verified `-race` + fresh-image cross-stack green.
- **3 in the same cluster are design-calls** (intent latitude a wrong guess
  would break — not yet fixed, awaiting user ruling):
  - `1840` MCP allowlist pins name only, so a node can run arbitrary stdio
    `command` under an allowlisted name. Fix shape = the operator boundary's
    intent: block node-supplied stdio when the allowlist is closed / add
    operator-side command pinning / accept name-only. Recommend: block stdio in
    closed mode.
  - `1843` OAuth token handed to the child via `CLAUDE_CODE_OAUTH_TOKEN` env
    (readable by the agent's Bash + inherited by stdio MCP children); the
    API-key path avoids env via an apiKeyHelper. A file-based fix needs the
    claude CLI's OAuth credential-file format, which can't be verified here;
    env is the documented OAuth mechanism.
  - `1874` callback token registry has no TTL. A fixed TTL is an arbitrary
    wall-clock constant that would reject a legitimate long-running dispatch;
    "max dispatch lifetime" is a design question.

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
