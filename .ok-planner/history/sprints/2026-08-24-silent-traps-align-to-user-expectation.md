# Sprint: Align twenty silent traps to what a user expects

## Intent

This sprint has no single theme. It remediates twenty traps the release documentation recorded at `d977250c`. A trap is a belief a user forms from the public surface. An experiment contradicted each of these twenty, and no concept, story, or decision speaks to any of them. For each trap the belief is the target behavior. Nine traps concern the CLI's flags and verbs, five the HTTP and MCP control surface, one the audit feed, two the deployment and template configuration, and three the bundled services, conformance, and compose. Five new decisions record the choices the work makes where a real alternative exists: the short-flag grammar, the MCP base-method boundary, the meaning of `undeployed` in a compose manifest, the deployment-wide retry defaults, and the http-poll sensor's outbound credentials. One amendment extends `concept:api-key` to cover the key's expiry.

Issues promoted into this sprint:

- `silent-traps-align-to-user-expectation`

## Corpus deltas

### Amend concept: api-key

```markdown
---
concept: api-key
aliases:
  - bearer token
---

# API key

## What it is

An api-key is a credential rimsky issues and a control-api client presents as a bearer token. The plaintext is a high-entropy string carrying a recognizable prefix. The server keeps only a one-way hash of it in a persisted api-key ledger, and surfaces the plaintext exactly once at mint and once again at each rotation.

## Purpose

An api-key is rimsky's authentication floor: every control-api endpoint can tell who is calling, and an operator mints, rotates, and revokes a key without redeploying. A deployment that needs richer identity terminates that identity at its own edge and injects an api-key downstream. The ledger is rimsky's entire principal registry: rimsky holds no user entity, so a person is the holder of a key's plaintext and a service principal is an api-key (see `concept:service-auth`).

## Boundaries

An api-key owns the plaintext's shape, the one-way hash the server stores, the persisted ledger, and the key's whole life: creation, rotation under a grace period, revocation, expiry at the key's declared end, and the periodic retirement of a key whose grace has passed. It does not own integration with an external identity system, rate limiting, or the definition of a role. It does not own the certificate machinery that derives a service's short-lived identity from a key carrying the enrollment grant: the api-key is the standing secret, the certificate is the derived identity, and revoking the key stops the certificate's renewal (see `concept:service-auth`). Each key carries a grant, which belongs to `concept:permission`. The deployment state in which no active key exists belongs to `concept:anonymous-mode`.

see also: `permission`, `role-template`, `anonymous-mode`, `event-log`, `service-auth`

## Aliases

- bearer token
```

### New decision: http-poll-sensor-auth-outbound

```markdown
---
decision: http-poll-sensor-auth-outbound
---

# The http-poll sensor takes the webhook sensor's `auth` block outbound

## Choice

The bundled http-poll sensor takes the same `auth` block as the bundled webhook sensor (see `decision:webhook-auth-required`) and applies it to its own outbound poll. The block takes two modes: `secret_header` (send a configured header on every poll) and `none`. It does not take `hmac`. A subscription whose `auth` block names any other mode is refused at bind time. A poll subscription with no `auth` block sends no credentials.

## Rationale

The http-poll sensor shares the block so that an operator who has learned one `auth` shape writes the same shape for both sensors. `hmac` is absent outbound because a poll is a GET with no body. No upstream defines a signature over a request rimsky originates. Omission is legal outbound because the poll is rimsky's own request to an upstream the operator chose. An unauthenticated poll exposes nothing of rimsky's, so the fail-loud polarity that protects the webhook sensor's inbound port has nothing to protect here. The sensor refuses an unknown mode because it cannot apply the block. A block the sensor drops is a credential the operator believes it sends.

## Alternatives

- A poll-side `hmac` over method, URL, and timestamp — rejected: no upstream verifies such a signature, so the mode would describe a request nobody checks.
- Fail-loud omission on the poll side, as the webhook sensor has — rejected: the poll is outbound to an upstream the operator chose, and forcing `none` there protects nothing.
- A poll-specific credential key outside the `auth` block — rejected: a second shape for one job.
```

### New decision: dispatch-defaults-cover-every-node-timing-key

```markdown
---
decision: dispatch-defaults-cover-every-node-timing-key
---

# Every per-node timing key takes a deployment-wide default

## Choice

`max_retries` and `retry_backoff` take their deployment-wide default from the dispatch defaults, beside the three deadlines that `decision:three-dispatch-deadlines` governs. `retry_backoff` defaults as one object: a node that sets `retry_backoff` replaces the whole default object, and a node that omits it takes the whole default object.

## Rationale

An operator sets deployment-wide policy once. A timing key with a deployment default and a timing key without one are two idioms for one job, so every per-node timing key takes a default from the same place. `retry_backoff` defaults whole because a backoff is one policy: kind, base delay, cap, jitter. Merging a node's partial object into the default would let a node change the kind and inherit a base delay chosen for a different kind.

## Alternatives

- Deployment defaults for the three deadlines only, with `max_retries` and `retry_backoff` set per node — rejected: an operator who wants one retry policy repeats it on every node.
- Per-subkey merging of `retry_backoff` — rejected: a node's partial object would inherit values chosen for a different backoff kind.
```

### New decision: short-flags-single-letter

```markdown
---
decision: short-flags-single-letter
---

# Short flags are single letters, one per token

## Choice

The `rimsky` CLI parses flags with the Go standard library's parser. A short flag is a one-letter alias registered beside its long form: `-y` for `--yes`, `-f` for `--follow`, `-o` for `--output`, `-v` for `--version`, `-h` for `--help`. Each short flag stands alone in its own token, with its value in the next token. Short flags do not cluster, and a value never attaches to its flag. `compose` keeps `-f` as the manifest path, so `--force` has no short form anywhere.

## Rationale

The standard library's parser cannot cluster short flags or attach values, and the project's rules resist a heavier command-line dependency. Registering the aliases operators type most gives them the habit that works. Stating the grammar keeps the parser's limit from reading as a bug. `-f` for `--follow` and `-f` for the manifest path cannot coexist under one letter, so `--follow` takes the letter on the verbs that stream and `--force` stays long. A long-only flag on a destructive verb costs one habit; a wrong short flag on one costs data.

## Alternatives

- A POSIX flag parser as a dependency, for clustering and attached values — rejected: the project resists heavier command-line libraries, and clustering is the only thing the dependency buys.
- No short flags beyond `-h` and `-v` — rejected: `-y` and `-f` are the two an operator types by habit, and their absence is the surprise.
- `-f` for `--force` and a different letter for `--follow` — rejected: `--force` on a destructive verb is the flag a mistyped letter should not reach.
```

### New decision: mcp-base-methods-scope

```markdown
---
decision: mcp-base-methods-scope
---

# The control API's MCP server implements six base methods

## Choice

The control API's MCP server answers `initialize`, `ping`, `tools/list`, `tools/call`, `resources/list`, and `resources/read`. It does not implement `prompts/*`, `resources/subscribe`, `resources/templates/list`, `roots/*`, `sampling/*`, or `logging/setLevel`. The capabilities the server returns from `initialize` name exactly what it serves, and a request for an unimplemented method receives the protocol's method-not-found error.

## Rationale

The MCP surface exists to project the HTTP control surface onto agents (see `decision:mcp-http-parity`). Tools and read-only resources are that projection. `ping` costs nothing and every client library sends it. Prompts, subscriptions, roots, sampling, and log-level control are client-side or agent-side features that project no part of the control surface; implementing them would add a second semantics with nothing behind it. Declaring the served set in the capabilities and answering the rest with method-not-found lets a client discover the boundary instead of guessing at it.

## Alternatives

- The whole base method set, with empty results for the parts rimsky has nothing behind — rejected: an empty `prompts/list` is a lie about what the server offers, and a no-op `logging/setLevel` is a control that controls nothing.
- Tools only, no `ping` — rejected: clients send `ping` as a liveness check, and a method-not-found on it reads as a broken server.
```

### New decision: compose-undeployed-is-registered

```markdown
---
decision: compose-undeployed-is-registered
---

# `undeployed` in a compose manifest means `registered`

## Choice

A compose manifest's `templates[].state` accepts `registered`, `deployed`, and `undeployed`. `undeployed` is a synonym for `registered`: the template stays registered under its tag and holds no deployment. The state is declarative in both directions: a template the deployment holds at `deployed` and the manifest declares `registered` or `undeployed` plans an undeploy step on the next apply, and a template the manifest declares `deployed` plans a deploy.

## Rationale

An operator who wants a template out of service writes the word that says so. `undeployed` names the outcome the operator wants; `registered` names the state the deployment ends in. Both words describe one state, so accepting both costs one map entry and spares the operator a lookup. A state key that moved a template forward and never back would make the manifest a script, not a declaration.

## Alternatives

- A third state that also drops the tag — rejected: dropping a tag is removal, and a manifest already expresses removal by omitting the entry.
- Reject `undeployed` as unknown — rejected: the word is the one an operator reaches for, and the error teaches nothing the synonym does not.
- One-way state, deploying only — rejected: a manifest that cannot undeploy is not declarative.
```

## Work items

Each item names the trap it remediates by the trap's slug. An item names a decision or concept where the item makes that artifact true.

- **Confirmation on every destructive verb** (`cli-destructive-verbs-confirm`). Lift `confirmDestructive` and `isTerminal` from `cmd/rimsky/cli/compose/apply.go` into the shared `cli` package. `tag rm`, `instance delete`, `instance kill`, `template undeploy`, `template rm`, `auth revoke`, `admin reset`, `lineage prune`, and `asset delete` print the target, prompt `Proceed? [y/N]` on a terminal, refuse with exit 2 without a terminal unless `--yes`, and proceed under `--yes`. `auth revoke` gains `--yes`. `instance kill` keeps `--force` with its current meaning; `--yes` answers the prompt only. This item also remediates `admin-reset-is-scoped`: `admin reset` parses `--yes` today and never reads it.
- **One duration grammar** (`cli-duration-flags-share-syntax`). Every duration flag parses with `time.ParseDuration`. `auth create --expires` drops its day suffix. `conformance --retention-test-seconds` becomes `--retention-test`, a duration flag. The rotation-grace parser in the auth handlers and every YAML duration field already use the grammar and stay.
- **`--help` exits zero on every verb** (`cli-help-on-every-subcommand`). `-h` and `--help` on any node of the verb tree print that node's own usage on stdout and exit 0: the leaf verbs that go through `runWithCommon` and `parseInterspersed`, the verbs with their own flag sets (`auth revoke`, `auth create`, `compose down`, `compose run`), the dev-loop shortcuts in `cmd/rimsky/main.go`, and `version`.
- **`-o json` is the one JSON spelling** (`cli-json-flag-universal`). `auth list` moves onto the common flags and drops `--json`. The four read verbs with no JSON output gain `-o`. `compose run --json` stays, under `decision:progress-flags`. Every read verb emits its JSON on stdout with human output on stderr.
- **`-o` names a format or fails** (`cli-output-flag-is-json-superset`). `-o yaml` serializes the same structs `-o json` does. `-o table` names the list verbs' table rendering and errors on a verb with no table. Any other value is an error. No value falls back to human output.
- **Short flags** (`cli-short-flags-single-dash`, makes `decision:short-flags-single-letter` true). Register `-y` for `--yes` on the common flags and `-f` for `--follow` on `instance events` and `messages tail`. `compose` keeps `-f` as the manifest path. `--force` has no short form.
- **`--since`/`--until` on every time-ordered read** (`cli-time-window-flags-uniform`). `messages tail` gains `--since` and `--until` in RFC 3339, wired to the server's delivered-after and delivered-before parameters. A new `rimsky audit` verb reads `GET /v1/audit` with the same flags. `watch --until` becomes `--until-state`. `lineage prune --before` becomes `--until`. `asset lineage` and `instance nodes` take no window flags.
- **Key expiry is an audit event** (`key-expiry-emits-an-event`, makes the amended `concept:api-key` true). A sweep beside the rotation-grace sweep in `lib/runtime/auth_sweep.go` appends one `auth.key_expired` event for each key whose expiry has newly passed, with a proto-declared payload and a new operational kind. A persisted marker on the key row, added by a Postgres migration and a SQLite migration, makes the event fire once per key.
- **The http-poll sensor takes the `auth` block** (`sensor-auth-block-uniform`, makes `decision:http-poll-sensor-auth-outbound` true). The http-poll sensor's subscription decode accepts `auth` with the webhook sensor's shape, applies `secret_header` on every poll, accepts `none`, refuses `hmac` and any unknown mode at bind time, and sends no credentials when the block is absent. The two sensors share one `AuthConfig` type.
- **Every instance route accepts an id or a key** (`http-idorkey-accepted-uniformly`). The frames, messages, assets, and debug-override handlers resolve their instance through the `resolveInstance` helper the nodes handler already uses, answer 404 on an unknown identifier, and spell the path segment `{idOrKey}`.
- **One pagination contract on every collection** (`http-list-routes-paginate`). Every collection route under `/v1` accepts `limit` and `cursor` and answers `{items, next_cursor}`: the observability executors and claim-producers collections, the assets list, the breakpoints list, and the claim holders gain it. `next_cursor` is present on every page. An empty page serializes `items` as `[]`. The tags cursor uses the shared opaque encoding. A malformed `limit` answers 400 on every route.
- **Unknown parent ids answer 404** (`http-status-codes-conventional`). Listing messages under an unknown instance and listing holders under an unknown claim handle answer 404, through an instance resolve and a claim-handle lookup with a not-found sentinel.
- **Tag creation is idempotent** (`http-tag-create-idempotent`). `POST /v1/tags` answers 200 with the existing mapping when the tag exists and names the same template hash, and 409 when it names a different one.
- **`ping`** (`mcp-standard-methods-present`, makes `decision:mcp-base-methods-scope` true). The MCP server answers `ping` with an empty result, its `initialize` capabilities name the served set, and every unimplemented base method receives method-not-found.
- **Deployment-wide retry defaults** (`dispatch-defaults-cover-every-node-timing-key`, makes `decision:dispatch-defaults-cover-every-node-timing-key` true). `dispatch_defaults.max_retries` and `dispatch_defaults.retry_backoff` join the deployment configuration, thread through the runner and supervisor configuration the way the sync-RPC deadline default does, and apply where the runner's error policy reads a node's retry settings. A node's `retry_backoff` replaces the default object whole.
- **A host-daemon conformance suite** (`conformance-covers-every-protocol`). A conformance suite for the host-daemon gRPC protocol lands under `lib/protocols/conformance/`, and `rimsky conformance host-daemon` runs it.
- **Declarative compose state** (`compose-state-key-is-declarative`, makes `decision:compose-undeployed-is-registered` true). The manifest accepts `undeployed` as a synonym for `registered`. The planner emits an undeploy step for a template the deployment holds at `deployed` and the manifest declares `registered` or `undeployed`; today the kept-hash check drops that step because the tag is still present.
- **Node tags on the CLI** (`node-tags-are-selectors`). The client's node type carries the tags the server already returns. `rimsky instance nodes` shows a `TAGS` column, filters by `--tag` through the route's existing `tag` parameter, and filters by `--tag-prefix` on the client. `rimsky node get` is unchanged.
- **Stub mode on every bundled executor** (`stub-mode-on-every-bundled-executor`). The `stub_response` override moves from the http-node executor into the shared stub-probe package beside park and cancel, and the verifier-http and verifier-shape-checks executors honor it under stub mode in place of their hardcoded stub successes. The claude-agent executor drops its extra `stub_probe` precondition.
- **Decision catalog** (no trap). Add the five new decisions' bullets to the decision catalog's table of contents, in the catalog's one-line form.

No item depends on another. Three pairs share a file surface: the `--help` item and the short-flags item both touch the common flag registration; the JSON-spelling item and the format item both touch the output package; the pagination item and the unknown-parent item both touch the messages handler. An executor lands each pair together or in sequence.

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
