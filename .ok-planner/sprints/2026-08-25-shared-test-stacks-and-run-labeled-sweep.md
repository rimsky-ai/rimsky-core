# Sprint: Shared test stacks and a run-labeled sweep

## Intent

Cut the docker resources a verification run boots and make every one of them removable by the run that made it. The core suites share one postgres per run instead of one per package process. The services suites share one rimsky stack per package process and backend instead of one per test. Every container, network, and volume a run creates carries the run's tag, and the run sweeps by that tag on every exit path.

Promoted issues: none.

## Corpus deltas

### Amend decision: testcontainers-go

```markdown
---
decision: testcontainers-go
---

# Integration-test container management

## Choice

Integration tests run the persistence backends as real container instances booted through testcontainers-go. The test process owns isolation: it creates its own database per test on the server it holds, from a template it migrated under a name unique to the process. The process boots a server of its own unless the verification run offers one. A run may boot one backend for every package it runs, hand each process the connection through one environment variable, and remove the server when the run ends. A process that finds the variable unset boots its own.

## Rationale

Real database containers in tests, not mocks: the persistence layer is exercised against the engines it ships with. Isolation lives in the per-test database, not in the container, so one container per package process bought nothing the template and clone did not already give, at the cost of one postgres boot per package. A server the run boots is the project's own tooling inside the run, swept with the run, and a test process started outside any run still boots its own, so a local run needs no setup beyond docker.

## Alternatives

- Mocked or faked persistence — rejected: the storage tests' subject is the real engines' behavior.
- A database the developer provisions by hand (a compose file, a CI service) — rejected: setup outside the run, which the run cannot sweep and a local run must repeat.
- One container per package process without exception — rejected: the per-test database already isolates, so the extra containers cost boots and buy nothing.
- An in-memory engine standing in for the server-backed one — rejected: divergent SQL semantics make the tests prove the wrong engine.
```

### New decision: test-stack-per-package

```markdown
---
decision: test-stack-per-package
---

# One rimsky stack per services test-package process

## Choice

The services test suites boot one rimsky stack per test-package process and per persistence backend, on first use, and every test in the package that asks for that backend shares it. A test isolates by identity: it registers its own template under its own name, creates its own instance under its own key, and deletes both when it ends. The package declares the union of the services its tests name, and the stack starts those services once. A test whose posture the shared stack cannot carry — another service-authentication mode, a container environment of its own, host-port access, a restart mid-test, a split-role topology — boots a private stack and names the posture.

## Rationale

The service set is boot-time configuration, so a stack per test was the one way to give each test its own service names; a per-package union gives the same names at one boot. The core suites already isolate by identity on a shared server, so the services suites adopt that pattern rather than a second one. The cost is that a test which leaks an instance affects its neighbors in the same process.

## Alternatives

- One stack per test — rejected: about seventy boots of a postgres, a rimsky, and a stub executor per run, for isolation that names already give.
- Runtime service registration, a route that adds a service to a running stack — rejected: a product feature with its own authorization and consistency questions, not a test-harness concern.
- One stack per module — rejected: the test runner runs each package as its own process, so a stack is owned by a process or provisioned outside every process.
```

### New decision: test-run-labeled-sweep

```markdown
---
decision: test-run-labeled-sweep
---

# Test docker resources carry the run's tag and the run sweeps them

## Choice

Every docker resource a test run creates — container, network, volume — carries a label whose value is the run's own tag, the tag the run mints for its images. The run sweeps by that label on every exit path: pass, fail, the test guard's inconclusive kill, and an interrupt. A reaper target removes labeled resources older than a threshold, for a run whose sweep never ran. The testcontainers reaper stays on. A fitness test fails the build on any creation site outside the labeling helper.

## Rationale

The testcontainers reaper is best-effort and removes a container without its anonymous volume, so a killed run leaves its containers and every run leaves one data volume per postgres. A label unique to the run lets the sweep remove what this run made and nothing a concurrent workspace's run made. The fitness test makes an unlabeled resource unbuildable, which is the mechanical check the coding rules ask for.

## Alternatives

- Rely on the testcontainers reaper alone — rejected: best-effort on a kill, and it leaves the anonymous volume behind.
- A global prune of containers and volumes after each run — rejected: reaches the resources of a concurrent workspace's run.
- A cleanup in each package's test entry point — rejected: never runs on a kill.
```

### New subject: docker-test-resources

```markdown
---
subject: docker-test-resources
---

# Docker test resources

## What it is

Every site in the tree that creates a docker container, network, or volume while a test suite runs. A site is a member whether it creates through the testcontainers library or by invoking the docker command. An image build is not a member: it produces an image, not a container, network, or volume. The product's own runtime, which creates no docker resources, has no members.

## How to find them

Search the Go sources, excluding vendored and generated code, for calls to `testcontainers.Run(`, `testcontainers.GenericContainer(`, `tcnet.New(`, and `pgmodule.Run(`, and for `exec.Command("docker"` whose first argument is `run` or `create`. The tree imports the testcontainers network package as `tcnet` and its postgres module as `pgmodule` at every site. The fitness test in the plumbline test group performs this search; its match count is the population.
```

### New practice: run-labeled-creation

```markdown
---
practice: run-labeled-creation
subject: docker-test-resources
---

# Every docker test resource is created through the labeling helper

## Practice

Every member creates its resource through the shared labeling helper, which stamps the label `org.rimsky.test-run` with the run's tag on the container, network, or volume. The site cites this practice.

## When it governs

Every member of the subject, without condition.

## What it buys

The run's sweep enumerates and removes everything the run created with one label filter, and a concurrent run's resources stay untouched. A reader settles whether it holds by running the fitness test in the plumbline test group, which fails on any member outside the helper.
```

## Work items

- **W1 — One postgres per run for the core suites.** Makes `decision:testcontainers-go` true. The postgres pool honors `RIMSKY_TEST_PG_DSN`: when set, it skips its own container boot, connects to that server as admin, creates its template database under a process-unique name, and clones per test as today; when unset, it boots its own container as today. The `test-root`, `test-foundation`, `test-services`, and `test-all` targets boot one labeled postgres for the run, export the variable, run the packages, and remove the server in the sweep. The pool's boot site carries `@decision: testcontainers-go`. Depends on W5 for the label.
- **W2 — The postgres claim producer's tests use the pool.** Makes `decision:testcontainers-go` true. The two duplicate per-test boot helpers in the postgres claim producer's `server` and `store` test packages (24 call sites) go; those tests acquire a database from the shared pool. No test package boots a postgres outside the pool.
- **W3 — Package stacks in the services harness.** Makes `decision:test-stack-per-package` true. The harness offers a package stack per backend, booted once per process on first use: one postgres-backed, one sqlite-backed. A package declares once the union of the executors, claim producers, publishers, and named locks its tests name, and the stack starts each declared service once. Each test registers a template under a per-test name, creates an instance under a per-test key, and deletes both in cleanup through the existing delete routes. Every test that today boots a per-test stack moves to the package stack, except the private-posture sites: service-auth mTLS, a container environment of its own, host-port access, a mid-test restart, and the split-role topology. Each remaining private stack names its posture at the call site. The package-stack entry point carries `@decision: test-stack-per-package`.
- **W4 — Instance-scoped row assertions.** Makes `decision:test-stack-per-package` true. The openlineage subscriber test's lineage-row wait scopes its count by the test's instance, and every other services test that queries rows directly scopes by template or instance identity, so the tests hold under a shared database.
- **W5 — The labeling helper.** Makes `decision:test-run-labeled-sweep` true and realizes `practice:run-labeled-creation`. One helper, importable from the root, foundation, and services modules within the lint's boundaries, creates every test container, network, and volume with the label `org.rimsky.test-run=<RIMSKY_IMAGE_TAG>`. The pool's boot, the services harness's boot wrapper and shared network, the CLI demo test's container, and the all-in-one smoke test's `docker run` create through it. Each creation site cites `@practice: run-labeled-creation`.
- **W6 — The creation-site fitness test.** Makes `decision:test-run-labeled-sweep` true. A test in the plumbline test group enumerates the subject's population by the search the subject names and fails on any member outside the helper. It carries `@decision: test-run-labeled-sweep`. Depends on W5.
- **W7 — The sweep.** Makes `decision:test-run-labeled-sweep` true. One sweep removes every container (with its anonymous volumes), network, and volume carrying the run's label, with each docker call bounded and its failure printed, so a hung daemon never blocks the exit. The test guard runs it on every exit path — pass, fail, inconclusive kill, and its own interrupt, which it catches, forwards to the process group, waits out, then sweeps — and returns the exit code it would have returned. Every `test-*` and `smoke-*` recipe runs the same sweep as its last line under `trap`, `test-docker` included. A `reap-runs` target removes labeled resources older than `REAP_HOURS`, beside `reap-images`. Depends on W5.
- **W8 — Subject and practice land.** Realizes `subject:docker-test-resources` and `practice:run-labeled-creation`. The two files land under the plumbline estate and the catalog table of contents is regenerated with `python3 .ok-plumbline/bin/catalog-toc`.

## How to execute this sprint

This sprint is self-sufficient. Every executor — an inline session,
an agent handed this file via `/goal`, an orchestrator with its own
planning — runs the same shape: a team of two workers the session
relays, then one cold certification.

1. Read the sprint whole first: intent, deltas, work items,
   completion contract. Do not look for context behind it, in the
   intake (`.ok-planner/issues/`) or in `history/`. Raise a gap with
   the owner; never fill it by inference.

2. Stage the work. Group the items by theme, file surface, or
   dependency, and order the groups so nothing is built on something
   not yet there. Before building, write the staged list as the
   opening section of the completion report (step 9): `## Stages`,
   one line per stage naming the work items it groups, each marked
   pending. Seed the closing stages
   now — finish the completion report, run `/certify-work` with this
   sprint's path as its argument, walk the presentation, offer
   archive-and-commit. The builder marks each build stage done as it
   lands. The session marks the closing stages after the team
   retires. The report is the record of the stages, never a plan
   document. The session keeps one task per stage in the harness task
   tools, where available, mirroring the report's staged list, and
   marks each task done as its stage lands. The task list is display;
   the report remains the record.
   An orchestrator uses its own graph and still records the stages in
   the report.

3. Run the team. The session orchestrates and never joins as a
   worker: it relays messages between the two workers, reads their
   task notifications, and holds the reviewer's ledger. It opens the
   completion report with the staged list before the build and marks
   the closing stages after the team retires; during the build it
   edits no file a worker owns. Every dispatch names its model.
   - **The builder** (`opus`), dispatched once with this sprint's
     path and the report's path, fed one stage per message. It
     writes the code, applies the stage's corpus deltas, tests what
     it built, marks the stage in the report with what it did, and
     stands by. It fixes the reviewer's findings in its own context
     when they arrive.
   - **The standing reviewer** (`opus`), dispatched once under the
     standing-reviewer brief in the certification core
     (`_shared/certification-core.md` under `.claude/skills/`), fed
     each landed stage's paths and the work items it lands. It reads
     the increment under the certification gate's code-review brief
     — findings reach anywhere in the tree the increment breaks —
     and the gate's alignment questions scoped to the stage's own
     items and deltas, plus the read-only per-stage producers each
     present family's ceremony contribution names under **Standing
     producers**, keeps a ledger of open findings, and replies with
     the ledger. It reports each claimed
     fork outside the ledger, in every reply until the completion
     report carries it. It edits nothing and runs no suite.
   - **The relay.** The session runs the relay protocol stated with
     that brief in the certification core: the message it sends the
     reviewer as each stage lands, the lines and claimed forks it
     relays back to the builder, the fix-only rounds it runs after the
     final stage, and the bound on those rounds. On every relay the
     session writes the reviewer's open ledger and the open claimed
     forks to `<sprint-name>-ledger.md` beside the completion report,
     so the state it holds survives it. A replacement session and a
     replacement reviewer read that file from disk.
   - **Retirement.** Retire a worker only at a stage boundary,
     inside the band the worker-pool rule sets: roughly 300k to 500k
     tokens of measured context (`subagent_tokens`) on a 1M-token
     window, scaled on a smaller window. At each boundary the session
     projects what the next stage costs and hands it over only when
     the worker will still retire inside the band. A replacement
     builder reads this sprint and the report and continues at the
     next stage; a replacement reviewer reads the open ledger and the
     open claimed forks from the ledger file.
   - **Without messaging.** Where the harness offers no cross-agent
     messaging, one session runs the same shape in bounded batches.
     The session orchestrates here too. Per batch it dispatches a
     fresh builder (`opus`) with this sprint's path, the report's
     path, one stage, and the open findings, then a fresh reviewer
     (`opus`) under the same brief over that stage's paths. The
     ledger and the open claimed forks travel in the prompt. After
     the last stage's batch, the session runs fix-only batches — a
     builder with the open ledger, then a reviewer over the fixed
     paths — until the reviewer reports an empty ledger, under the
     same bound the protocol sets.

4. Apply each corpus delta as part of the work that realizes it:
   copy the final-form body into `.ok-planner/design/` verbatim (from
   the sidecar where the heading points there), or delete the file
   for a retirement. Apply a delta no work item implements on its
   own.

5. Build stage by stage. Every new or amended story implemented in
   code is exercised end-to-end by a test in the project's ordinary
   suites, carrying the `@story:` annotation. No test checks the
   existence of static text, code, or prose; a commitment realized in
   prose carries no test. Write the tests with the work; the
   builder runs the tests that cover what it built, never the full
   suites — the gate runs the regression. Leave
   `.ok-planner/audits/` and `.ok-planner/experiments/` untouched:
   only a running `/audit` reads or writes them, and they record
   behavior at the time of the audit. An experiment the work breaks
   stays broken until the next run repairs or retires it.

6. Completeness is the floor. Never stub, defer, narrow, no-op, or
   leave a `TODO` in place of a promised outcome. Deliver every
   capability the deltas or work items promise in full, or surface
   the blocker that prevents it.

7. Never destroy uncommitted work. Stage the paths you touched as
   each stage finishes (`git add <paths>`). Never run `git checkout`/
   `restore`/`reset`/`stash`/`clean` on your own initiative. Fix a bad
   edit forward by editing again.

8. Work unsupervised to a defensible done. Do not pause for
   approval, confirmation, or progress checks. Stop only on a
   genuine blocker: a credential or access you cannot obtain, a step
   impossible in the current state, a destructive or irreversible
   action not clearly authorized, or the closing `/certify-work`
   step being unrunnable for you (its subagent dispatches
   unavailable). Surface that and stop; never skip the ceremony and
   call the work done. Ambiguity is not a blocker. The builder never
   files an issue: where the sprint is silent, it makes the most
   plausible call, continues, and records the call in the report as
   a divergence; where the sprint and corpus do not determine the
   fix and reasonable owners diverge, it records the fork with its
   options, builds the reading it judges most plausible, and
   continues. The gate reads both.
   An orchestrator that supervises its own executors folds this into
   its own control.

9. The completion report stays current. It lives beside this sprint
   file, same filename with `-completion` before the extension. The
   session opens it in step 2 with the staged list and marks the
   closing stages after the team retires. The builder marks each build
   stage done as it lands and records what it did. It writes every
   divergence and every claimed fork — its own and the reviewer's —
   into one `## Divergences` section, one entry each. Each entry opens
   with a stable identifier on its first line: `D<n>` for a
   divergence, `F<n>` for a claimed fork, numbered in the order the
   builder wrote them. The identifier lets the gate's architect
   rewrite an entry in place. A fork entry carries the fork's options
   and, where the builder built one, the reading it built. The report is the record the closing ceremony
   finishes and walks with the owner, the artifact a goal checker
   requires, the brief a replacement builder reads, and it is archived
   with this sprint. It is a record of this execution, never a plan.

10. Code complete means the built work works and the reviewer's ledger
    is empty. Close by running `/certify-work` with this sprint's path
    as its argument, immediately after. The argument puts the sprint
    in the gate's scope; the gate never adopts one on its own. The
    gate is cold and is the regression: it runs the project's test
    suites over the touched work, change-scoped corpus checks over the
    touched artifacts and annotations, and one code review over the
    whole diff by a reviewer holding no history and blind to the
    report; its sprint-alignment judge reads the report's divergences
    under the veto test and routes each claimed fork to the architect.
    All producers feed a no-discretion review-fix loop: standing
    agents work in rounds against a finding ledger. The loop ends at
    the first round in which neither the fixer nor the architect
    edited any file (code, corpus, or the report's `## Divergences`).
    A fixer fixes everything a reasonable owner would wave
    through. An architect adversarially checks its kickbacks, its
    refutations, the claimed forks, and any reversal. It makes the fix
    wherever it overturns the claim, and promotes only genuine intent
    forks to the intake.
    Whether the corpus's claims still hold is the periodic `/audit`
    run's question, never this close's. `/certify-work` ends the run:
    it writes its presentation into the completion report, walks the
    presentation with the owner, offers the close-out, and stops.

**After the run stops.** The owner archives this sprint and commits
the work. The run offers both at the end of the presentation and
does neither on its own. Until the owner answers, this file stays at
its `sprints/` path. On yes, the run moves this file, its completion
report, its ledger file, its delta sidecar, and the issue files it
resolved to `history/`, commits the work, then stamps the archived
sprint with the closing commit — `closed: <sha>` in the frontmatter,
one small follow-on commit. The next planning ceremony reads that
stamp to detect work done out of band. "Finish the sprint" and
"follow the boilerplate" are not a yes; both ask for the
presentation.

## Completion contract

The work is done when all of the following hold, each verifiable
from the repository as it stands:

1. The design corpus matches every delta above, applied verbatim
   (from the sidecar where a heading points there).
2. The project's own test suites pass, and every new or touched
   story implemented in code is exercised end-to-end by a test the
   suites run.
3. The completion report beside this sprint (same filename with
   `-completion`) is finished: it records the work done and the
   divergences, and carries `/certify-work`'s presentation — the
   review-fix loop run last and come back clean, every finding
   settled: `fixed <pass>`, `refuted`, `dissolved`, `reversal-ruled`,
   or promoted-and-verified.

**The goal rule, for any checker verifying this contract.** The goal
is met when items 1–3 verify against the repository as it stands.
Decide from the repository, never from the session transcript: an
earlier session may have done the work, and a term the transcript
does not show may hold on disk. That state is the goal met. Walking
the presentation, archiving, committing, and the `closed:` stamp all
follow completion; a pending archive-and-commit offer is evidence
the goal is met. Where this sprint file sits is no term of the rule:
`sprints/` and `.ok-planner/history/sprints/` satisfy it alike, and
a sprint already archived with a `closed:` stamp is terminal — stop
checking. A missing completion report means not done. The ledger file
is no term of the contract: it is the relay's working state, and
whether it exists decides nothing. A run parked
at the review-fix loop's cycle cap awaiting the owner's direction
has not met the goal: a legal in-flight state, not done, not failed,
and never grounds for the run to take either cap step itself.
Nothing else counts either way.
