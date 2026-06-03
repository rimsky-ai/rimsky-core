# Acceptance Coverage Recovery — Divergence Record

Audit of the working tree against `2026-06-02-acceptance-coverage-recovery.md`
(plan) and its spec. This is a record of where the implementation differs from
what the plan literally said, including consequential choices the plan left
unspecified. It proposes no fixes.

The plan's framing line — *"One new harness helper (`StartSensorHTTP`) and one
test-only seam in the runner. No production behavior changes except bug-fixes a
gate forces."* — sets the bar. Three production-behavior changes landed; all are
bug-fixes the Gate 1/8 acceptance run forced, which the plan generically
sanctioned ("If the real loop surfaces a bug … fix it forward; the green gate
enforces it") but did not enumerate as anticipated tasks. They are the most
consequential entries below.

---

## 1. Production behavior change: `POST /instances/{id}/messages` now seeds a delivery frame

- **What the plan said:** Pass 8 (Task 19) drives a real sensor through the
  message-emit path and asserts the cascade fires; the plan anticipated only "if
  the real loop surfaces a bug … fix it forward." No task names a change to the
  message-emit handler. The architecture line promised "No production behavior
  changes except bug-fixes a gate forces."
- **What was implemented:** `lib/control/controlapi/messages.go:241-273` — after
  `runtime.EnqueueMessage`, the handler now resolves a frame source
  (`resolveMessageFrameSource`, `messages.go:316-346`) and calls
  `frame.EnqueueOrCoalesce` in the **same tx** as the message insert, so a
  message POSTed to a quiescent instance (no running frame) gets a frame to be
  delivered into instead of staying pending forever. New import of
  `lib/graph/frame`. This is a behavior change to a core control-plane route, not
  a test.
- **Inferred reason:** The Gate 1/8 acceptance run (a real sensor emitting into a
  quiescent instance) flushed the bug — the sensor's message persisted but never
  woke the subscriber because nothing seeded a delivery frame. Fixed forward per
  `.claude/rules/rules.md`; documented in `VERIFICATION.md`'s "Bugs flushed"
  section (bug #2).

## 2. Production behavior change: terminal predicate now spares instances with an active publisher-subscription

- **What the plan said:** No task touches `MarkInstanceTerminatedIfDone` or the
  persistence frame drivers. Same "no production behavior changes except
  gate-forced fixes" bar.
- **What was implemented:** `lib/foundation/persistence/postgres/frames.go:136-139`
  and `lib/foundation/persistence/sqlite/frames.go:173-176` — both drivers'
  `MarkInstanceTerminatedIfDone` gained an `AND NOT EXISTS (… rimsky_publisher_subscriptions
  ps WHERE ps.instance_id = … AND ps.state = 'active')` clause, so an instance
  with an active publisher-subscription is never auto-terminated on first settle.
- **Inferred reason:** Gate 1/8 flushed it — a sensor-watched instance died on
  its first settle and rejected the next sensor emit with 409, breaking the
  reactive use case `concept:cascade` claims. Fixed forward; `VERIFICATION.md`
  bug #3. Touches a `@blessed-invariant`-adjacent predicate (frame-end /
  instance-terminated parity) the plan never anticipated editing.

## 3. Production bug fix: `rimsky-control-api` binary now wires the parsed `publishers:` block

- **What the plan said:** Nothing. No task touches any `cmd/` entrypoint.
- **What was implemented:** `cmd/rimsky-control-api/main.go:113-120` — the
  standalone control-api binary's `AppDeps` literal now sets
  `Publishers: rimskyCfg.Publishers`. Previously unset, so the publisher registry
  was empty and every publisher-subscription failed at instance-create with
  `unknown_publisher` in any multi-process (three-container split) deployment.
- **Inferred reason:** Gate 1/8 flushed it; `VERIFICATION.md` bug #1. Note this
  was a drift between two parallel `AppDeps` construction paths: the config-driven
  builder `lib/control/config/controlapi.go:320` already wired
  `Publishers: publisherReg` correctly (so the all-in-one image — which routes
  through that builder — was never broken, which is why the pre-existing
  `TestAllInOneSQLite` never caught it). The fix closes the standalone binary's
  copy of the same construction.

## 4. Stub executor advertises a `DeclaredErrorClass` beyond the plan's Task-15 scope

- **What the plan said:** Task 15 said: add an `EXECUTOR_STUB_FORCE_ERROR` env
  read and, when set, emit a `StreamClose` with an **error** outcome
  (`error_class: "stub/forced_error"`). Nothing about the executor's advertised
  capabilities.
- **What was implemented:** `lib/services/test/stubexecutor/main.go:78-101` — in
  forced-error mode the `observability.Capabilities` RPC now *also* advertises
  `DeclaredErrorClasses: ["stub/forced_error"]`, not just the error StreamClose.
- **Inferred reason:** Task 16 step 5 calls for an `error_types: {stub/forced_error:
  give_up}` block on the abandon-case verifier, and the registration validator
  range-checks each `error_types` key against the executor's *advertised* error
  vocabulary — so a template routing `stub/forced_error` would be rejected at
  registration unless the stub declares it. The implementer recognized the
  dependency Task 15 did not spell out and extended the stub's advertised caps to
  match. A correct, necessary consequence of Task 16's design; the divergence is
  only that Task 15's literal text did not anticipate it.

## 5. Erroring-stub bring-up implemented as a dedicated harness function (not env-through-a-helper)

- **What the plan said:** Task 16 step (Aggregate outcome) offered two options:
  "add a `StartErroringExecutorStub` variant in Task 15's file **or** pass the env
  through a small helper."
- **What was implemented:** `lib/services/test/harness/executor_stub.go:36-57` —
  a new exported `StartErroringExecutorStubOnNetwork`, with both it and the
  original `StartExecutorStubOnNetwork` delegating to a shared private
  `startExecutorStub(…, forceError bool)`. The `EXECUTOR_STUB_FORCE_ERROR=1` env
  is set inside the harness when `forceError` is true, rather than passed by the
  test.
- **Inferred reason:** The plan sanctioned this option explicitly; the
  implementer chose the named-variant path and refactored the shared body out.
  Not a deviation from intent — recorded only because the chosen branch differs
  from the helper-location the prose leads with.

## 6. `VERIFICATION.md` retains a qualified "PASS" verdict rather than dropping the PASS framing

- **What the plan said:** Task 21 step 2: "replace any 'every feature / PASS / 0
  shape-only' absolute with the honest post-gate state … Do not assert coverage a
  gate did not establish."
- **What was implemented:** `VERIFICATION.md` keeps a verdict of **"PASS, with
  three implementation fixes landed this pass,"** still states "70 of 71 concepts
  … behavioral," and drops the prior "0 concepts shape-only" line and the "no
  implementation fixes" claim. It adds a "Bugs flushed" section and re-points the
  weak citations to the new gates per Task 21 step 1.
- **Inferred reason:** A judgment call on "honest post-gate state." The implementer
  read the instruction as *correct the false absolutes* (the "no fixes" /
  "0 shape-only" claims, now removed) rather than *delete the word PASS* — and
  qualified PASS with the three landed fixes. Within the spirit of the
  instruction; recorded because a literal reading of "replace any … PASS …
  absolute" could have meant removing the PASS verdict entirely.

---

## Notes (not divergences, recorded for the reader)

- **Coupling-proof out-and-back tasks (Tasks 2, 4, 7, 9, 11, 14, 17) left no
  residue.** `detectDelegateCycles` is still called
  (`template_validator_graphs.go:106`), `verifyBeforeRun` still does its real
  `GetClaimedBy` re-read (`runner_acquire_postcommit.go`), `operator.json` and
  `pick_policy.go` are byte-restored, and the catalog re-entry is intact — the
  neuter steps were executed and reverted exactly as the plan required.
- **Tests strengthened the assertions beyond the plan's minimum, in good faith.**
  The post-commit test (`test/scenarios/verify_before_run_post_commit_test.go`)
  added a `stolen` flag asserting the hook actually fired (guards against the seam
  silently going inert); the sensor-cascade test added quiescence-then-baseline
  machinery (`waitForDispatchQuiescent`) so the negative-control bystander
  assertion is unambiguous. These are sound additions consistent with the plan's
  intent, not deviations from it.
- **The runner seam (Task 12) and `StartSensorHTTP` helper (Task 18) match the
  plan precisely** — nil-default `PostCommitHook` on `RunArgs`, invoked
  immediately before `verifyBeforeRun` at `runner_acquire.go:341`; and the
  sensor helper is pure-env with `WithHostPortAccess`, returning a bare
  `<alias>:9082` endpoint, exactly as specified.
- **`FilesystemStoreSpec` / `FilesystemPickPolicy` were already present in the
  harness at HEAD** (not added this pass), so Gate 10's test consumed the existing
  harness API; no harness-type change was needed there.
