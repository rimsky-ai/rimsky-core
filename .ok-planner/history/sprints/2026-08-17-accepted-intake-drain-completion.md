# Completion report: Accepted intake drain

## Stages

1. **Corpus deltas** — done. Copy the nine sidecar concept bodies and write the eleven decision bodies into `.ok-planner/design/`.
2. **Persistence** — done. Guard the claim-handle expiry renewal by its holding supervisor, order stale-row claims by sequence, rename the two blob in-transaction helpers, close the cross-driver parity gaps.
3. **Runtime settlement** — done. Make strict fan-out cancel siblings, emit the two settling terminal signals through the typed builder, keep a woken parked run through a most-recent cascade round, dispatch builtin-kind nodes inside delegated sub-graphs.
4. **Supervisor callback surface** — done. Move the callback listener's liveness probe under the version prefix, cover the keepalive claim renewal with a test.
5. **Conformance** — done. Give the lifecycle-subscriber suite its own package.
6. **Host agent late-bind** — done. Resolve late-bound claim-producer proxy names through the address book.
7. **Bundled services** — done. Namespace the two claude-agent operator variables, serve the claude-agent HTTP bridge on the versioned execute path, route the verifier-http executor through the egress guard, route the webhook sensor's port through the shared precedence helper.
8. **Control API and observability** — done. Join the frame routes to their triggering message, answer a malformed cursor with 400, map SQLite's trigger-constraint code to the template-in-use error.
9. **CLI** — done. Give the remote one-shot run the four exit-code classes, print every row of a page in the message tail, print validation advisories on a successful registration, recognise the dry-run preview envelope.
10. **Release tooling** — done. Add the parallelism-cap fitness test, drop the root-module fetch line, repoint the SemVer detectors.
11. **Finish the completion report** — done.
12. **Run `/certify-work`** — pending, with this sprint's path as its argument.
13. **Walk the presentation with the owner** — pending.
14. **Offer archive-and-commit** — pending.

## Work done

### Stage 1 — corpus deltas

All twenty deltas landed in `.ok-planner/design/`. Nine concept bodies came from the sidecar. Eleven decision bodies came from the sprint's inline blocks. Each body differs from its live counterpart only by the ruled lines.

### Stage 2 — persistence

- The claim-handle expiry renewal now names the holding supervisor in its predicate, in both backends. Its two callers — the callback server's keepalive and attribute-writeback path, and the acquire-reuse path — pass the acting supervisor. A new claimant-guard conformance case proves two things: a wrong claimant leaves the expiry alone, and the holder's own renewal lands.
- Candidate selection orders by enqueue time, then sequence, then row id, in both backends. The keyset cursor carries the sequence, and `persistence.Candidate` carries it back to the caller. A new conformance case writes three rows for one node in one transaction and asserts they surface in creation order.
- The two package-level blob helpers dropped the `InTx` suffix: `WriteBlob` and `ReadBlob`. The pair-detecting fitness test now walks every top-level persistence declaration, not only receiver-bearing methods.
- The driver-parity suite gained nine cases, one per unexercised method: frame settlement (`EndFrameIfSettled`) first, then the three frame observability reads, the two queue reads, and the three claim-handle reads. A new fitness test enumerates the three interfaces' declared methods and fails naming any the suite does not exercise.

### Stage 3 — runtime settlement

- Strict fan-out aggregation now cancels its siblings. The cancel-action executor handled only the first-policy action, so strict's cancel action was inert. A scenario test drives three async partitions, fails one, and watches the other two reach failed with the sibling-failed settling signal. The unit test that asserted the returned action value came out.
- The sibling-cancellation and instance-kill settlement sites build their signals with the typed terminal-error builder and emit through the validating path, so both payloads carry `tags` and `attributes_delta`. Two error-class constants sit beside the settling-signal constants they compose. The builder-only fitness test now resolves a type path named by a constant, including one built by concatenation, so it sees both sites.
- A woken parked run survives a most-recent cascade round. A new column, `park_resumed_at`, marks a row that reached stale by a park-wake. The coalescing delete skips such a row. The gate keeps it rather than dropping it for the newer round. Migration 042 adds the column to both backends. A scenario test parks a worker, drives a second upstream cascade, and asserts the parked run row still settles.
- Kind sugar, send-message sugar, and the aggregation-policy default now canonicalize every declared node, sub-graph blocks included. Sub-graph child dispatch reads the persisted `graphs:` declarations directly, so a node declared by builtin kind inside a delegated sub-graph reached dispatch with no executor. A scenario test runs such a template to a settled frame.

### Stage 4 — supervisor callback surface

- The callback listener serves its liveness probe at `/v1/health`, so no route on the control-plus-callback surface sits outside the version prefix. The router build moved into an exported `Routes()` method. A new test walks both surfaces — 65 control-API routes and 4 callback routes — and fails naming any bare path.
- A keepalive test builds the callback server against a real claim-handle table, posts a keepalive, and asserts the run's claim expiry moved forward.

### Stage 5 — conformance

The lifecycle-subscriber suite moved out of the executor suite's package into `lib/protocols/conformance/lifecyclesubscriber`, carrying its own dial helpers. The `rimsky conformance lifecycle-subscriber` subcommand points at it, so certifying lifecycle compiles no executor fixtures.

### Stage 6 — host agent late-bind

The claim-producer late-bind path resolves its proxy name through the in-process registry and then the address book, as the executor path already does. It previously read the in-process registry alone, so a template naming a late-bind service never reached a spawned claim-producer binary. A unit test resolves a bound producer to the proxy through the address book and leaves an unbound name unresolved.

### Stage 7 — bundled services

- The claude-agent dispatch spend cap and observability bridge URL carry the service segment: `RIMSKY_CLAUDE_AGENT_DISPATCH_MAX_USD` and `RIMSKY_CLAUDE_AGENT_OBSERVABILITY_HTTP_BRIDGE_URL`. The pin test widened from host-and-port names to every operator variable an executor reads, exempting the generic per-executor set, and derives each service's segment from its directory name. It checks 30 reads.
- The claude-agent HTTP bridge serves `POST /v1/Execute`, the path the http-node bridge serves and the supervisor's HTTP executor client dials. The executor's README names it.
- The verifier-http executor dials its node-attribute URL through the shared egress guard, default-closed, with `RIMSKY_EXECUTOR_VERIFIER_HTTP_EGRESS_ALLOWLIST` as its opt-in. Two tests cover both polarities.
- The webhook sensor resolves its gRPC port through `agentport.Resolve`, so the host agent can late-bind it. A new fitness test enumerates the bundled service binaries, checks the 10 that listen, and names the 1 that serves no port.

### Stage 8 — control API and observability

- The dashboard's frame list and frame get handlers read through the joined store methods and return the triggering message's type, sender and sender kind, matching the instance-scoped routes.
- A cursor that fails to decode reaches the caller as 400. Every decode site in both backends now returns the shared `persistence.ErrInvalidCursor` sentinel instead of an error naming its store operation, and both the control API's `writeError` and the observability handlers' `internalErr` map that sentinel to 400 with the sentinel's own message. Tests cover both surfaces and assert the message names no internal operation or decoder.
- The SQLite store's foreign-key predicate recognises 1811, the code SQLite reports for a restrict-on-delete trigger. Deleting a template referenced only by a terminated instance now reaches the template-in-use error and its 409. A conformance case covers it on both drivers; removing the code makes it fail.

### Stage 9 — CLI

- The remote one-shot run reports the four exit-code classes: 0 on a clean terminal, 1 when the instance's outcome carries a failed node, 2 on the run timeout, 130 on interrupt. The outcome classifier and the four exit-code constants moved down into the `cli` package so the compose coordinator and the remote run share one definition. The test that pinned 0 on interrupt now asserts 130; three new tests cover the other classes.
- The message tail filters each received page against the watermark taken before the poll and advances the watermark only after the page is printed. It previously advanced inside the loop, so a newest-first page printed only its first row. Two unit tests cover the watermark helper and one drives the tail over a three-row page.
- The CLI's template response type carries the validator's advisories, and the register verb prints them on the success path in plain and structured output.
- The client's shared response decode recognises the top-level dry-run marker before unmarshalling into a resource type and returns a `DryRunPreview`. One branch in `reportError` reports it as a preview — the would-have line, exit 0, and the dry-run-marked envelope under structured output — so all thirteen client write methods are covered by the one decode chokepoint.

### Stage 10 — release tooling

- A grouped fitness test reads the Makefile's test recipes and asserts the three docker-backed module suites carry the saturation cap while the protocols suite carries none. The Makefile comment's bare prose reference to the decision is gone; it now points at the test.
- The `go get` line for the root module comes out of the release skill's notes template and out of `RELEASING.md`. Both now say why: the root module depends on the unpublished `lib/*` sub-modules through `replace` directives, the same limit that already blocks `go install`. The protocols-module line stays.
- The release skill's CLI-flag detector reads the CLI library's flag-set declarations under `cmd/rimsky/cli/`; the binary entrypoints declare no flags. The exported-symbol detector now covers the root module alongside protocols and foundation.

## Verification

The project's test suites pass at `src-3c565b64c65f`.

- `make lint` passes across all four modules, license check included.
- `make core-images service-images test-images` rebuilt every image set at the settled tree.
- `make test-root test-foundation test-protocols test-services` passes: 167 packages report `ok`, and no package reports a failure. The two `FAIL Commit:` lines in the log are the conformance runner printing its own report for a synthetic-failure case.
- Two scenario tests written by this sprint failed their first full-suite run. Both defects sat in the tests, not in the code they cover.
  - `TestParkedRunSurvivesMostRecentCascadeRound` posted a second instance message to start the second cascade round. A message posted while a frame runs queues for the next frame, and the parked worker holds its frame open, so the round never arrived. The test now pauses the instance, invalidates the upstream node, and resumes.
  - `TestTemplateSubGraphBuiltinKindNodeDispatches` read the settled-frame count once, immediately after the caller node reached fresh. The frame's `ended_at` lands after that. The test now calls `WaitForSettledFrameCount`.
- The parked-row test detects the defect it covers. Removing `AND park_resumed_at IS NULL` from `DeletePriorCascadeStales` makes it fail, naming the deleted run. Restoring the predicate makes it pass.

## Divergences

- **`make lint` failed before this sprint.** The license check reported 116 unclassified files, all under `.ok-planner/experiments/` — the audit's instruments, which no work item touches. `licensing.yml` now exempts `.ok-planner/` the way it already exempts `.claude/`, for the same reason: the estate holds planning state, not source the project licenses. The lint passes across all four modules.
- **The outcome classifier moved packages.** Giving the remote run its exit-code classes needed the classifier that lived in `cmd/rimsky/cli/compose`, which imports `cmd/rimsky/cli`. It moved down into `cli` and compose delegates to it, so one definition serves both.
- **`test/scenarios/parked_resume_spurious_cascade_test.go` carries a `time.Sleep(2s)` to settle a negative assertion.** It is pre-existing and untouched by this sprint. Proving the absence of a dispatch needs a different design than a sleep, which is more than this sprint's items ask for.

## Calls made where the sprint was silent

- **Corpus deltas applied in one opening stage.** The boilerplate says to apply each delta as part of the work that realizes it. Eleven of the twenty deltas have no work item, and every delta is a verbatim file copy whose result does not depend on code state, so all twenty landed together in stage 1. The end state matches the contract either way.
- **The two claude-agent variables took the `RIMSKY_CLAUDE_AGENT_*` prefix**, matching the service's two existing operator variables rather than the http-node sibling's `RIMSKY_EXECUTOR_HTTP_NODE_*` shape. Both forms carry the service segment and both satisfy the widened pin test; one prefix per service keeps the service's four operator variables in one dialect.
- **The two settling terminal signals now cascade.** The sprint said to emit them through the validating path. That path validates the type-path and walks the subscribers. The previous code wrote an audit row and nothing else. A sibling cancelled by strict aggregation now reaches subscribers of its node, and so does a run force-failed by an instance kill. `concept:signal` promises exactly that: subscribers express interest through the terminal payload. A killed instance is already terminated, so its cascade-driven rows never dispatch.
- **A woken parked run needed a marker.** `ResumeParked` clears the park columns, so the row carried no record of the wake. Migration 042 adds `park_resumed_at`. The coalescing delete and the gate both read it.
- **The gate keeps the woken row as well as the delete skipping it.** The sprint named the coalescing delete. The gate's has-later check would have dropped the woken row on its own dispatch, so the promised outcome — the woken row dispatches first — needs both.
- **The harness gained a settled-frame waiter.** Every other harness wait helper polls until its condition holds. The sub-graph test needed the same shape for settled frames, so `WaitForSettledFrameCount` joins them rather than a one-off loop in the test.
- **A held executor call orders nothing across a frame.** The first repair of the parked-run test held the upstream executor call to order the two rounds. A held dispatch blocks its whole frame. The worker's park therefore waited out the thirty-second sync deadline, and the held run failed. The pause-invalidate-resume construction replaced it.
- **The port-precedence fitness test names its population honestly.** The work item says the invariant holds across all eleven binaries; the invariant's own text scopes it to binaries that serve a port. The openlineage subscriber serves none, so the test checks ten and logs the one it excludes.

---

# Certification — Accepted intake drain

Status: certified clean

## Outcomes delivered

Twenty corpus deltas landed and twenty-six work items now hold. What a user or operator can observe:

**The design corpus says what the code does.** Nine concepts and eleven decisions carry their amended bodies, and the decision catalog's one-line summaries match them.

**Claim handling is safe under a supervisor handoff.** A supervisor renews a claim's expiry only while it holds the claim, on both backends. The claimant-guard conformance suite fails when a future mutator skips the guard.

**A parked run survives an upstream cascade.** A cascade arriving during a park wakes the parked row and queues the new round beside it. Under most-recent mode the coalescing delete now skips the woken row, so the parked unit of work still executes.

**A sub-graph node declared by builtin kind dispatches.** Kind sugar resolves over every declared node, not only the flattened main graph.

**Strict fan-out cancels its siblings.** A strict aggregation force-fails every remaining in-flight clone through the run-tree walk, and the cancelled sibling's terminal signal reaches its subscribers.

**Rows written in one transaction claim in creation order.** Candidate selection orders by enqueue time, then sequence, then row id, on both backends, and the paging cursor carries sequence.

**A late-bound claim producer resolves.** A template naming a late-bind service reaches a spawned claim-producer binary through the address book, as the executor path already did.

**Every bundled binary resolves its port the same way.** The webhook sensor now follows agent-assigned port, then its own variable, then the default.

**The CLI reports outcomes a script can read.** A remote one-shot run reports timeout as 2, interrupt as 130, and the instance's outcome as 0 or 1. The message tail prints every row of a page. Template registration prints the validator's advisories on success. Every write verb reports a dry-run preview as a preview at exit 0.

**Client errors read as client errors.** A malformed pagination cursor answers 400 with a caller-safe message. Deleting a template still referenced answers 409 on SQLite instead of a 500 carrying driver text.

**The verifier-http executor blocks private egress by default**, with its own opt-in CIDR allowlist, matching its two sibling dialers.

**Operator variables carry their service segment.** The two claude-agent variables are `RIMSKY_CLAUDE_AGENT_*`. This is a documented surface change: a deployment setting the old names must repoint them.

**The supervisor's liveness probe moved under `/v1`.** An external probe configured against the bare path needs repointing.

**The claude-agent HTTP bridge serves the versioned execute path**, matching the http-node bridge and the supervisor's HTTP executor client.

**The lifecycle-subscriber conformance suite has its own package**, so certifying lifecycle compiles no executor fixtures.

**Three fitness tests now check claims that had no check:** the parallelism caps, the in-transaction naming convention, and the cross-driver parity coverage.

## Divergences

Every item here is yours to veto after the fact.

**From the build:**

- `make lint` failed before this sprint began. The license check reported 116 unclassified files, all under `.ok-planner/experiments/`. `licensing.yml` now exempts `.ok-planner/` the way it already exempts `.claude/`.
- The outcome classifier moved from `cmd/rimsky/cli/compose` down into `cmd/rimsky/cli`, because the remote run needed it and compose imports cli.
- The two claude-agent variables took the `RIMSKY_CLAUDE_AGENT_*` prefix rather than the http-node sibling's `RIMSKY_EXECUTOR_HTTP_NODE_*` shape, so the service's four operator variables share one dialect.
- The two settling terminal signals now cascade, where the previous code wrote an audit row and nothing else. A sibling cancelled by strict aggregation reaches subscribers of its node, and so does a run force-failed by an instance kill.
- A woken parked run needed a marker. Migration 042 adds `park_resumed_at`; the coalescing delete and the gate both read it.
- The gate keeps the woken row as well as the delete skipping it. The gate's has-later check would have dropped the woken row on its own dispatch.
- The port-precedence fitness test checks ten binaries and logs the one it excludes. The invariant's own text scopes it to binaries that serve a port, and the openlineage subscriber serves none.
- A held executor call orders nothing across a frame. The first repair of the parked-run test held the upstream executor call to sequence the two rounds; a held dispatch blocks its whole frame, so the worker's park waited out the sync deadline. The pause-invalidate-resume construction replaced it.
- The harness gained `WaitForSettledFrameCount`, matching its other poll-until-success waiters.

**From the certification fixer:**

- It deleted `ClaimHandleTable.ListByState` from the interface, both drivers, and a test fake. The stricter parity check showed the method has no caller anywhere. This breaks the `lib/foundation` module's Go API, which the pre-v1 rule allows and the release notes should name.
- It created `lib/protocols/grpcdial` holding `Target` and `TransportCredentials`, and deleted six duplicate copies — one more site than the finding named. The reviewer diffed every copy against the shared implementation and confirmed they did not differ. This adds two exported symbols to `lib/protocols`, which `decision:release-semver-from-diff` reads as a minor bump.
- It formatted the node locator as `nodes[i]` and `graphs["name"].nodes[i]` — the YAML path an author reads — rather than the graph-plus-type phrasing the finding offered.
- It chose receiver analysis over per-case registration for the parity check, so no case author has to remember a registration step.
- It added tests the findings did not require, on the sqlite cursor and the node-path locator.
- `ReportDryRunPreview` returns `(int, bool)` so each call site keeps its own exit path.

**Corpus repairs made during certification:**

- `.ok-planner/design/decisions.md` — seven catalog lines regenerated from their bodies: `acquire-unavailable-carveout`, `event-log-payload-shapes`, `grpc-internal-protocols`, `inproc-handler-interface`, `keepalive-endpoint`, `launch-integration`, `tls-mode-validation`. Each had contradicted its own body since the deltas landed.

## Findings fixed

Thirteen findings, all fixed in one cycle. No kickbacks, no dissolutions, no architect pass.

- **Sprint alignment** — 7 findings, all one root cause: the decision catalog was never regenerated after the eleven decision deltas landed, so seven summaries contradicted their bodies. All twenty deltas were verbatim; all twenty-six work items were realized.
- **Code review** — 6 findings: the send-message error named a nonexistent node index for sub-graph nodes; four auth write verbs and the compose verbs still reported a dry-run preview as a failure at exit 1; the parity coverage check matched bare method names rather than calls on the three interfaces; nothing asserted that a clean strict fan-out cancels nothing; the conformance package split left duplicate dial helpers; the sqlite candidate cursor short-circuited on a zero timestamp where postgres did not.
- **Test suites** — clean on first pass at certification. 168 packages pass across four modules.
- **Plumbline lint** — clean on first pass, 194 changed files.
- **Annotation integrity** — clean on first pass.
- **Catalog table of contents** — clean on first pass.
- **Workspaces discipline sweep** — clean on first pass.
- **Practice citation** — contributes nothing; this project declares no subjects or practices.

## Issues promoted

None. The review-fix loop reached clean in one cycle, so the architect confirmed no fork and the run never reached the cap.

One issue was filed by hand during execution, outside the loop: `.ok-planner/issues/2026-08-17-193000-hang-backstop-counts-retry-output-as-progress.md`. The hang backstop counts a polling test's retry logging as progress, so a wedged test never dies — which cost nine hours during this sprint. Narrowing the guard's progress signal would change ruled text in `.claude/rules/rules.md`, so it is your ruling rather than execution's call. It awaits `/verify-issues` or the next `/plan-sprint` like any other intake issue.
