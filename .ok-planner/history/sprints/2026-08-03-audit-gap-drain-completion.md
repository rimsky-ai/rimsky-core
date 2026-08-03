# Completion report: Audit gap drain

Execution record for `.ok-planner/sprints/2026-08-03-audit-gap-drain.md`.
Written as the work landed; divergences and judgment calls are recorded
against the item that produced them.

## Corpus deltas

All five applied verbatim.

| Delta | Target | Applied |
| --- | --- | --- |
| Amend decision | `decisions/inproc-eventstream.md` | yes |
| Amend story | `stories/anonymous-mode-bootstrap.md` | yes |
| Amend decision | `decisions/graceful-shutdown.md` | yes |
| Amend concept | `concepts/conformance.md` | yes |
| Amend decision catalog | two bullets in `decisions.md` | yes |

The catalog entries for `conformance` and `anonymous-mode-bootstrap` were
checked and left alone, as the delta directed.

## Work items

### Delete the executor subcommand's lifecycle-check flag

`--check-lifecycle` and its branch are gone from the executor conformance
subcommand, along with the `--tls` help text that named the lifecycle probe.
The subcommand flag table in the CLI test lost its entry, and a new test
(`TestNoProtocolSuiteHangsOffAnotherProtocolsSubcommand`) walks every
conformance subcommand's real usage output and fails when a flag name
contains another protocol's name — the concept's new leading invariant now
has a mechanical check rather than only the flag-table bookkeeping.

### Gate the object-store sensor's in-memory backend behind an explicit enable

Backend registration moved out of `main()` into `registerBackendsFromEnv`,
which registers the filesystem backend on `RIMSKY_SENSOR_OBJECT_STORE_FS_ROOT`
and the memory backend only on
`RIMSKY_SENSOR_OBJECT_STORE_ENABLE_MEMORY_BACKEND=1`. Because the shipped
capabilities schema derives its `backend` enum from the registered set, a
stock image now advertises only `filesystem` and refuses a subscription
naming `memory`. Three tests cover the stock deployment, its refusal, and
the enabled case.

No consumer needed the new variable: the services harness, the
subscription-mounting demo, and the sensor's Dockerfile already select the
filesystem backend. The sensor's unit tests register the memory lister
directly on the service object, which is the fixture use the decision
already sanctions.

### Unify the bundled executors' host/port environment variable names

`http-node`, `verifier-http`, and `verifier-shape-checks` now read
`RIMSKY_EXECUTOR_HOST` / `RIMSKY_EXECUTOR_PORT_GRPC` /
`RIMSKY_EXECUTOR_PORT_HTTP`, matching `claude-agent`. No compatibility
aliases. Behaviour-specific knobs (`..._MAX_BODY_BYTES`,
`..._HTTP_BRIDGE_URL`, `..._ERROR_CLASS_FIELD`, `..._EGRESS_ALLOWLIST`) keep
their per-service prefixes. The generated env-var registry was regenerated,
and a new fitness test walks `lib/services/executors` and fails on any
host/port variable outside the generic set.

### Apply the amended in-process executor decision

Delta applied; the goroutine-and-channel bridge is unchanged, as the item
directed. No code change.

### Stop the liveness-recovery sweep from double-emitting work-started

The post-acquisition audit transaction now skips the `work_started` append
when the acquisition resumes a dispatch the deadline sweep released — the
row's `prior_dispatch_disposition` is `stale_recovery` and its
`prior_dispatch_id` points at itself, which is exactly the shape
`ReleaseClaimWithDisposition` stamps and nothing else produces. A new
scenario test drives the whole episode (async handoff, sweep at a
far-future clock, re-acquisition to terminal) and asserts the singleton
pair. The test was confirmed to fail against the unfixed code, reporting
two `work_started` rows carrying one dispatch id.

### Apply the amended anonymous-mode story and prove its exception

Delta applied; the enrollment handler is unchanged. A new test stands up a
zero-key deployment with a deployment CA wired (the posture mutual-TLS peer
auth creates), asserts four operator control-plane reads succeed
unauthenticated, and asserts `POST /v1/enroll` is refused with 403 and a
reason. The shared auth fixture gained an enroll-capable variant.

### Make CI run the plumbline lint

The `go` job sets `PLUMBLINE_BIN` to the committed
`.ok-plumbline/bin/plumbline`, so the root module's purpose-built test
executes instead of self-skipping. A new fitness test reads the workflow and
fails if that wiring is dropped or repointed away from the vendored binary.

**Overshoot.** The workflow also carried a `ts-executor` job running
`npm ci` / `npm test` / `npm run build` in
`lib/services/executors/claude-agent` — a directory that holds a Go
executor with no `package.json`. That job could only fail. Per
`decision:implementation-language-go-plus-ts` the only TypeScript left in
the tree is the protocols module's type-declaration stub, which has no
build or test scripts. The dead job was removed and the workflow's header
comment corrected to the two jobs that remain.

### Teach the license check to classify third-party dependencies

`licensing.yml` gained a `third_party` section: a permitted-license set and
a curated module-to-license map covering the Apache surface's dependency
closure, each license read from the dependency's own LICENSE file rather
than recalled. The checker now resolves every non-stdlib import of an
Apache-classified file to its module entry, and separately walks the build
closure of each Apache module. An unclassified module is a violation, as is
a module whose license (including every term of a compound expression) is
outside the permitted set. Confirmed fail-closed by removing a real entry
and watching both Apache modules report the gap.

**Divergence from the drafted method.** An empty `permitted_licenses` list
is not a load-time error — it makes every third-party import a violation
instead. A load-time error would have broken the existing config fixtures
without adding safety, and the fail-closed reading is the stronger one.

### Fold named-lock acquisitions into the claim-acquisition metric family

`rimsky_named_lock_acquisitions_total` is gone. The claim-acquisition family
now carries `{acquirer_kind, acquirer, intent}` — `acquirer_kind` is
`producer` or `named_lock` — and the metrics hook maps both increment call
sites onto it, so the runtime's two call sites stay semantically distinct
while the exported surface is one family. The scrape smoke test asserts the
retired family is absent and the folded series present; the story's scenario
assertions moved to the folded shape, including the one that proves lock
contention does not move the producer series.

### Add second-signal hard-exit to the deployed shutdown paths

A single primitive, `shared.InstallSecondSignalHardExit`, watches the signal
channel while a drain is in flight and runs a caller-supplied hard-exit on
the next signal. All three deployed paths install it — the container
entrypoint's unified drain, its spawned-child shutdown (which also hard-kills
the child), and the standalone role boot — and the CLI's compose-run
escalator was rewritten onto the same primitive so there is one idiom rather
than two. The thirty-second deployed window is unchanged. A subprocess test
drives the entrypoint's child shutdown, feeds it a second signal, and asserts
exit code 130.

### Convert the remaining HTTP bridges to protojson

The claim-producer / lifecycle-subscriber bridge decodes every verb straight
into its generated request message; its ten hand-written body structs are
deleted, as are the three on the conformance kit's client, which now marshals
the same generated messages on the way out. The claude-agent executor's
standalone execute bridge decodes `ExecuteRequest` and answers with a
protojson `Outcome` carrying `await_async` — byte-identical to what the gRPC
path returns, where it used to answer a bespoke `{"async_ack_id": …}` object.
Both bridges decode with `DiscardUnknown`, matching how gRPC treats a field
the proto does not declare. Two now-dead helpers
(`SessionTokenFromScratchBase64`, `unwrapClaimProducersJSON`) were removed.

Request bodies are unaffected in practice: the deleted structs' JSON tags
were already the proto field names, and protojson accepts those alongside
its lowerCamel forms.

### Build the per-run lifecycle state endpoint and close the MCP parity gap

`GET /v1/runs/{run_id}` reads one run's lifecycle state — id, node, frame,
state, an explicit terminal flag, executor, and the claim/progress timestamps
— behind its own `run:read` action, with `run_get` as its MCP tool. Reading
it needed a new persistence method: `Queue.GetByID` filters to in-flight
states, and the endpoint owes "in flight or terminal", so `GetAnyByID` was
added to the interface and both drivers alongside it, each driver's two
getters now sharing one query builder.

The four parity gaps are closed: `instance_frame_list`, `instance_frame_get`,
`observability_get`, and `service_enroll`. The observability route is a
wildcard, so path substitution learned to fill a trailing `*` from a
`path_suffix` argument, rejecting an empty suffix, an empty interior segment,
and any `..` traversal.

Three coverage tests lock it shut: every routed action has an MCP tool (or is
named in the one-entry deliberately-tool-free set, which holds only the MCP
transport itself); every tool resolves back to an action with a route; and
every declared schema belongs to a registered tool. The first found a real
gap beyond the four — `audit_list` had no input schema and was falling back
to a bare `{"type":"object"}`, so an agent could not see its filter
arguments. It now has one.

### Make peer_auth: mtls a single flip

`peer_auth: mtls` now supplies the default for every peer entry's `tls` key,
so a deployment that sets the mode and the CA key gets required TLS on
claim-producer, executor, publisher, validator, and data-processor entries
without naming them; an entry that writes `tls: off` explicitly keeps it. The
control API's own listener wraps itself in TLS under the mode, serving the
leaf the deployment CA already issues for the control-api principal and
verifying any client certificate presented against that CA.

The listener uses `VerifyClientCertIfGiven` rather than
`RequireAndVerifyClientCert`: the control API port carries two boundaries at
once — operator traffic authenticated by api-key bearer, and services'
publish-back calls authenticated as peers. Requiring a client certificate
would lock every operator out of a deployment that flipped the mode. A
service that presents a certificate is verified against the deployment CA; a
certificate from any other CA is refused at the handshake.

**Divergence.** The services integration harness reaches the control API from
the host, and under the mode that port now speaks TLS. The harness has no way
to obtain the container-generated CA root before its first request, so its
host-side client skips server verification when the stack runs under mtls.
That is a test-harness concession on a localhost container, and it is the one
place in this change where a TLS connection is not verified; the mutual
property itself is proved in-process, where the CA is in hand.

**The end-to-end proof, and the four defects it uncovered.** The first cut of
the bundled-service e2e test dispatched to `http-node` in its bundled
in-process form: a direct Go call with no network hop, which proved the
control API's listener and nothing about a forward dispatch leg. Making the
test dispatch through a genuinely networked, enrolling executor peer — a
standalone `rimsky-executor-http-node` container that obtains its own leaf
from the deployment CA and serves gRPC under `RequireAndVerifyClientCert` —
showed that no forward leg could have worked under the mode at all. Four
defects stood between the flip and a completed dispatch:

- **Peer dials verified the endpoint hostname.** Enrollment issues a leaf
  whose SAN is the peer's api-key principal, never the hostname the config
  points at, so every mutual-TLS dial failed with `certificate is valid for
  key-…, not <host>`. Every leaf now additionally carries the fixed peer name
  `peer.rimsky.internal`, and the peer client pins `ServerName` to it when a
  deployment CA pool is installed. Verification stays fully on — the chain
  check, and with it `VerifiedChains`, is preserved, which is what
  `PrincipalFromVerifiedChains` reads the peer's principal off; skipping
  verification and re-checking the chain by hand would have emptied it and
  silently dropped the executor principal from every dispatch.
- **A bundled service could not enroll once the control API served TLS.** The
  enrollment hop carries the api-key, and its client trusted only system
  roots, so the mode made enrollment impossible for every standalone service
  image. Services now read `RIMSKY_CONTROL_API_CA`, the pinned deployment CA
  root the control API's certificate is verified against — required when the
  control API URL is https, refused when it is not, and fail-closed on an
  unreadable or non-PEM file.
- **The supervisor's capability probe dropped the peer's TLS mode.** The
  supervisor built its discovery peer specs without `TLS`, so it probed every
  executor in plaintext. Under the mode that probe could never succeed, on any
  refresh, which left every dispatch failing `executor_schema_unavailable`
  permanently rather than transiently.
- **The supervisor probed before its own identity existed.** Its boot
  handshake runs in `launch` ahead of `StartSupervisor`, which is what
  installs the peer identity, so under the mode the boot probe had no client
  certificate to present. It now re-probes once the identity is installed.

With those fixed, the test proves the claim it makes: the executor entry
declares no `tls` key and the stack reports it as `required`; the executor
refuses both a certificate-less client and one signed by another CA, so it is
authenticating its caller and not merely offering TLS; and the dispatch runs
to a fresh terminal through that leg. The harness's `writePeerBlocks` no
longer hardcodes `tls: off` on every executor entry — it wrote the one value
that would have masked the feature — and instead omits the key unless a test
asks for an explicit override.

## Cross-cutting fixes made along the way

Per the project's fix-every-bug rule, three pre-existing defects in files
this work touched were fixed rather than logged:

- `TestShutdownChild` slept 200ms hoping a shell trap was installed and
  failed on a 10-second wall-clock deadline. It now blocks on a readiness
  file the fixture writes after installing its trap, and on the call
  itself.
- `TestRunMigrateIfOwned_SignalInterrupts` failed on a 15-second wall-clock
  deadline; it now blocks on the subprocess.
- `waitForFile` failed after a 5-second wall-clock deadline; it now polls
  until the file appears, with the suite-level `go test -timeout` as the
  only hang backstop.

Two more in files this change touched:

- `callbackRecorder.waitForCall` in the claude-agent tests failed on a
  ten-second wall-clock deadline; it now polls until the callback arrives.
- `TestHTTPBridgeTraceGet` failed on a five-second deadline; it now polls
  until the trace completes.

And one more, found while running the full suite alongside the image builds:

- `TestTemplateFanOut_HappyPath_AllSuccess` asserted that the three partition
  runs' `work_started` events fell within a 500ms spread — a load-dependent
  verdict with no defensible value ("why 500 and not 499?"), and it failed on
  a loaded machine. The assertion was also redundant: every dispatch in that
  test is held open until the test releases it, so the wait for three
  `work_started` events already proves all three were in flight at once.
  Serialized dispatch stalls on the first hold and never reaches the third.
  The spread query and its bound are gone; the concurrency claim now lives on
  the wait that actually establishes it.

The wall-clock ratchet baseline was regenerated twice to lock in the five
verdict idioms these removals drained.

## Divergences, gathered

- The license lint's empty-`permitted_licenses` case fails closed at check
  time rather than erroring at load time (see that item).
- The services harness skips server verification on its host-side client
  under mtls (see that item).
- Two defects found outside the sprint's items were fixed rather than
  logged: the dead `ts-executor` CI job, and `audit_list`'s missing MCP
  input schema.
- Four defects that stood between `peer_auth: mtls` and a working forward
  dispatch leg were fixed rather than surfaced as blockers, because the
  sprint's own item requires that leg proven end to end (see that item):
  hostname-bound peer verification, the missing enrollment trust root, the
  supervisor's TLS-less capability probe, and its pre-identity boot probe.
  Two carry operator-visible surface: the new `RIMSKY_CONTROL_API_CA`
  environment variable a standalone bundled service must set under the mode,
  and the fixed `peer.rimsky.internal` name every deployment-CA leaf now
  carries alongside its principal.
- The wall-clock spread verdict in `TestTemplateFanOut_HappyPath_AllSuccess`
  was removed as redundant (see above). The `require.Eventually` deadline
  idiom it sits beside is repo-wide (50 call sites across 25 files) and was
  left alone: sweeping an idiom is a single change of its own, not a
  side-effect of this one.

---

# Certification — Audit gap drain

Status: certified clean

## Outcomes delivered

Thirteen promoted issues and one out-of-band reconciliation, as
user-observable outcomes:

- A protocol's conformance suite is reachable only through its own CLI
  subcommand; the lifecycle-check alias flag is gone and a test forbids any
  flag naming another protocol.
- A stock object-store sensor advertises only the filesystem backend and
  refuses a subscription naming the in-memory one.
- All four bundled executors read the same unprefixed listen host and port
  variables.
- A dispatch swept back by the liveness sweep and re-acquired emits exactly
  one work-started event across the episode, matching its one
  work-completed.
- On a fresh deployment every operator control-plane action succeeds
  unauthenticated, and machine service enrollment — the one exception — is
  refused.
- A comment-hygiene or citation-resolution violation now fails CI.
- An Apache-surface package importing an unclassified or copyleft-
  incompatible module fails the build.
- Setting `peer_auth: mtls` and the CA key is one flip: every internal peer
  entry defaults to required TLS, the control API's own port serves TLS
  from the deployment CA, and a bundled executor authenticates its caller
  on the forward dispatch leg.
- An operator holding a run id can read that run's lifecycle state, in
  flight or terminal, over HTTP or MCP.
- Named-lock acquisitions are graphable alongside producer claims in one
  metric family, discriminated by label.
- A second interrupt forces immediate hard exit on every deployed shutdown
  path.
- Both remaining HTTP bridges speak the proto contract's own JSON in both
  directions.
- Every control-API action has an MCP counterpart, and a test forbids
  shipping one without.

## Divergences

- **Overshoot — the peer-auth flip was larger than the item described.**
  Proving the forward dispatch leg end to end uncovered four defects that
  made the mode unusable in any deployment: the test harness forced
  `tls: off` on every executor entry; every mutual-TLS dial failed hostname
  verification because enrollment leaves carry the principal, not the
  endpoint host; standalone services could not enroll once the control API
  served TLS; and the supervisor probed executors in plaintext, so
  dispatches failed permanently under the mode. All four are fixed, with
  two new operator-visible surfaces: `RIMSKY_CONTROL_API_CA` and the fixed
  peer server name.
- **The license lint's empty-`permitted_licenses` case** fails closed at
  check time rather than erroring at load.
- **The services harness's host-side client** skips server verification
  under mtls; it cannot obtain the container-generated CA root before its
  first request. The mutual property is proved in-process, where the CA is
  in hand.
- **Two defects outside the sprint's items were fixed rather than logged**:
  the dead `ts-executor` CI job, and `audit_list`'s missing MCP schema.
- **Corpus edits by the fix loop: none.** All five deltas remain byte-for-
  byte as the sprint approved them.

## Findings fixed

- **Sprint alignment** — 1 finding: the peer-auth end-to-end test proved
  only the control-API port, not the forward dispatch leg (blocking
  undershoot). Fixed; re-review clean.
- **Test suites** — 1 finding: `cmd/rimsky-entrypoint` failed
  intermittently under the full-module run. Root cause was not a shutdown
  race but an orphaned shell grandchild holding the test binary's stdout
  pipe, so `go test`'s WaitDelay expired 30s after the tests had passed.
  Three fixtures rewritten; reproduced against the old code, then 40/40
  clean under load.
- **Mechanical floor** — clean on first pass; every annotation resolves.
- **Code review** — 1 finding, the same one alignment raised. Re-review
  after the fixes came back clean.

## Pre-existing defects surfaced, not fixed

Both reproduce identically at HEAD in a clean worktree, so neither is
caused by this change, and the gate does not widen scope to them:

- `lib/foundation/persistence/sqlite` hangs under `-race` (still hung at a
  300s ceiling), so `make test-foundation`'s race step cannot pass.
- `lib/runtime/hostagent` takes ~105s under `-race`, blowing
  `make test-root`'s 180s parallel ceiling when the other `lib/runtime`
  packages run alongside it.

Every non-race suite passes across all five modules, and `make lint`
(including `license-lint`) is clean.

## Issues promoted

None. No finding survived the fix loop as a genuine intent fork.
