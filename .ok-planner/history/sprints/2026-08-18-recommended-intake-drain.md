---
closed: d6769c97
---

# Sprint: Recommended intake drain

## Intent

Work the accepted batch of the intake to completion: the 34 issues under `.ok-planner/issues/accepted/`, each carrying a recommended ruling from `/verify-issues` that the owner accepted by silence (one, `structural-root-edges-are-derived-on-demand-not-injected-at-registration`, also carries a generated ruling). The sprint has no theme. Twenty-nine corpus amendments and three new decisions bring the corpus to what the owner ruled. Six retirements remove artifacts with no current subject. The work items fix the code where the corpus was right and the code was wrong, and they sweep the test suites clean of wall-clock verdict idioms.

Four decisions the owner made live in the planning session also ride this sprint. `concept:replica` is retired: rimsky addresses a service by its endpoint and coordinates nothing behind it. The `compose:` tag marker and its server-side reservation are removed: compose keeps the manifest's project name as its prefix, and the server reserves nothing. A per-run tag replaces the content-addressed image tag for verification: the ok-workspaces suite change is made, and this tree adopts it. The project's own determinism rules defer to the plumbline testing standard.

Issues promoted into this sprint:

`attribute-second-substitution-round-not-persisted`,
`bundled-default-ports-collide-with-core-listeners`,
`ca-root-is-a-second-unauthenticated-route`,
`ci-services-shard-builds-unresolvable-image-tags`,
`cli-api-key-clause-not-universal`,
`cli-conformance-verbs-outside-capability-surfaces`,
`egress-allowlists-invert-allowlist-default-polarity`,
`experiments-tree-belongs-to-neither-license-track`,
`handler-package-scope-for-services-without-inproc-path`,
`lint-check-activation-cannot-be-staged`,
`log-level-env-var-ignored-by-services-and-host-agent`,
`makefile-go-builds-omit-cgo-disabled`,
`migration-history-was-rebased-against-the-decision`,
`no-shipped-park-resume-recipe`,
`node-run-state-writes-bypass-transition-switch`,
`nominate-cli-and-template-surface-experiments`,
`nominate-deployment-surface-experiments`,
`nominate-http-mcp-and-auth-surface-experiments`,
`nominate-protocol-error-and-event-surface-experiments`,
`per-module-prose-sweep-has-no-subject`,
`polling-division-is-a-backlog-not-a-settled-state`,
`prefix-type-paths-are-field-checked`,
`published-images-are-single-platform-arm64`,
`second-signal-escalation-is-not-universal`,
`sensor-state-stores-use-rejected-sql-adapter`,
`service-address-book-reload-absent`,
`service-dockerfile-expose-lines-disagree-with-listeners`,
`standalone-validator-roles-not-from-capabilities`,
`structural-root-edges-are-derived-on-demand-not-injected-at-registration`,
`surface-intent-go-module-embedding-surface`,
`surface-intent-non-prefixed-env-vars`,
`unpermissioned-reads-have-no-mcp-tool`,
`verbose-flag-inert-and-progress-axes-do-not-compose`,
`wait-set-drain-predicate-at-dispatch-time`.

## Corpus deltas

Every body below is in the sidecar (`2026-08-18-recommended-intake-drain-deltas/<kind>s/<slug>.md`); each heading reads `body: in the sidecar`. Retirements carry only their heading.

### Amend concept: attribute

body: in the sidecar

### Amend concept: control-api

body: in the sidecar

### Amend concept: persistence-database

body: in the sidecar

### Amend concept: publisher

body: in the sidecar

### Amend concept: publisher-subscription

body: in the sidecar

### Amend concept: rimsky

body: in the sidecar

### Amend concept: sensor

body: in the sidecar

### Amend concept: service

body: in the sidecar

### Amend concept: service-address-book

body: in the sidecar

### Amend concept: signal

body: in the sidecar

### Amend concept: tag

body: in the sidecar

### Amend concept: transition-reason

body: in the sidecar

### Amend concept: validation

body: in the sidecar

### Amend concept: wait-set

body: in the sidecar

### Retire concept: replica

### Amend story: compose-lifecycle

body: in the sidecar

### Retire story: bundled-park-resume-recipe

### Retire story: compose-namespace-guard

### Amend decision: allowlist-defaults-open

body: in the sidecar

### New decision: destination-allowlists-default-closed

body: in the sidecar

### Amend decision: bundled-recipes-production-paths

body: in the sidecar

### New decision: default-port-allocation

body: in the sidecar

### Amend decision: graceful-shutdown

body: in the sidecar

### Amend decision: handler-package-in-service-directory

body: in the sidecar

### Amend decision: licensing-dual-apache-agpl

body: in the sidecar

### Amend decision: logging-slog-only

body: in the sidecar

### Amend decision: migrations-append-only-numbered

body: in the sidecar

### Amend decision: operator-env-namespaced-per-service

body: in the sidecar

### Amend decision: polling-audit

body: in the sidecar

### Amend decision: test-wallclock-lint-ratchet

body: in the sidecar

### Amend decision: postgres-pgx-v5

body: in the sidecar

### Amend decision: release-chain

body: in the sidecar

### Amend decision: release-distribution

body: in the sidecar

### New decision: structural-root-edges-derived-on-demand

body: in the sidecar

### Retire decision: structural-root-edge-injection-at-registration

### Amend decision: subscription-edges-only-from-explicit-block

body: in the sidecar

### Retire decision: config-flip

### Retire decision: untagged-prose-by-module

## Work items

- **Send the API key the compose verbs already parse.** The compose up, down, plan, and status verbs attach the resolved key to the control-api client they build. An operator who passes the key flag, sets the environment variable, or holds a context key now authenticates instead of reading a 401 as a server fault. Makes `concept:rimsky`'s api-key invariant true for the compose surface. A test drives each of the four verbs against a stub control API that demands a bearer token and asserts the request carries it.

- **Carry the API key on the publisher conformance suite's control-api poll.** The publisher conformance verb accepts the general key flag and sends the resolved key when it polls the control API for the pushed message. An implementer can then run the check against an authenticated deployment. Makes `concept:rimsky`'s api-key invariant true for the one conformance verb that dials the control-api. The item adds a flag to an already-public verb; the surface intent calls every CLI verb and subcommand public and states no rule about flags, so nothing there changes.

- **Pin the api-key universal with a test over the CLI's verbs.** One test asserts that every verb building a control-api client defines the key flag and sends the resolved key. The test carries the exceptions the concept names — the context-management verbs, the host-agent status and stop verbs, the compose one-shot, the host-agent start verb, the interactive login verb, and the conformance verbs — as its own list, so a new verb that drops the key fails the build. Depends on the two items above. Makes `concept:rimsky`'s api-key invariant mechanically checked.

- **Select the compose one-shot's progress printer as volume × format.** The quiet, verbose, and json flags compose. Quiet with json emits only the final summary as a JSON record, verbose with json adds frame-tick records, and each volume keeps its meaning under both formats. The verbose flag's help text names only output a printer emits, so the claim-events promise leaves it. Makes `decision:progress-flags` true. A test asserts that quiet with json emits the summary record and nothing else.

- **Emit a frame tick from the compose one-shot's poll loop.** The loop that polls each instance to terminal reports each newly observed frame through the printer's frame-tick emitter. Verbose output then differs from default output. Makes `decision:progress-flags` true and keeps `decision:progress-default` true, because the tick stays out of the default volume. Depends on the printer-selection item, which gives the tick its JSON form. A test drives the loop against a stub client that reports a second frame and asserts the verbose printer emits a tick line.

- **Give the three unpermissioned control-api actions their MCP tools.** The liveness probe, the identity echo, and the CA-root fetch each register a tool. The catalog lists and invokes a tool by its action's recorded posture rather than by a permission grant. An agent holding only an MCP client can then ask whether the deployment is up, which key it holds, and for the CA root. Makes `story:mcp-transport` and `concept:control-api`'s same-operation-set claim true. The parity test's list of routed actions deliberately without a tool shrinks to the MCP transport action alone, and that test is the proof. The CA-root tool lists unconditionally, as the enrollment and observability tools already do for their conditionally mounted routes.

- **Declare MCP tools as a public class in the surface intent.** `.ok-planner/surface/surface.md` gains one class rule beside its HTTP-routes rule: every MCP tool the catalog lists is public. The rule covers the three tools above and the forty-four that exist. The owner settled this in the planning session.

- **Remove the `compose:` tag marker and the server-side reservation.** Compose names every tag and instance key it creates `<project>:<name>`, with the manifest's project as the only prefix. The `compose:` marker leaves the CLI's tag and key construction, its state query, its fixtures, and its tests. The control API stops refusing any name by prefix. The reserved-prefix checks on tag create, template registration with a tag, and instance create go. The compose-origin header goes, and so does the `compose:origin` action in the action registry and the bundled role definitions. The tests and the scenario that pin the reservation go with them. Makes `concept:tag`, `concept:control-api`, `concept:rimsky`, and `story:compose-lifecycle` true as amended. A compose scenario asserts `up`, `plan`, `status`, and `down` round-trip a manifest under the project prefix and that a tag another client names under that prefix is listed, diffed, and torn down like compose's own.

- **Route every node-run state write through the next-state function.** Each persistence backend's unrouted state writers — the dispatcher's promotion of a claimed stale run to running, the claim-release writers that return an in-flight run to stale, and the run-tree's parent aggregate write — carry a transition reason and persist the state the switch returns, failing with the illegal-transition sentinel instead of writing when the switch rejects the pair. The switch gains the arms these transitions need, release-back-to-stale among them, so a move no arm models becomes unrepresentable rather than merely unvalidated. The run-tree parent arm derives its target from the aggregation policy inside the switch, so the switch keeps its state-and-reason shape. The unconditional force-release writer, which no shipped code calls, is removed rather than routed. Makes `concept:transition-reason`'s switch invariant true across every state-column writer in both backends. Prove it in the shared persistence conformance suite so postgres and sqlite are pinned by one case: a legal release and a legal promotion succeed, and an illegal state-and-reason pair is refused at the writer.

- **Reject a standalone validator entry that derives no role at deployment load.** The config loader derives a standalone validator's role discriminators from its declared protocols less the validation protocol itself, and refuses to boot when the remaining set is empty, naming the entry in the error. Today an entry declaring only the validation protocol — also what the loader defaults an entry with no protocols list to — dials successfully with an empty role set and is never consulted at any registration. Makes `concept:validation`'s standalone-validator invariant true. No protocol change: the three peer kinds that carry a capabilities handshake keep resolving roles through it. Covered by a loader test asserting the load fails and the message names the entry, beside the existing standalone-validator load tests.

- **Host the one drain-then-escalate helper where both Go modules can reach it.** The shared helper that installs the second-signal hard exit lives in the protocols module's server kit, the only package the root module and the bundled-services module may both import, and it carries the three grace windows as named values. The helper currently sits in the foundation module, which the consumption-side-isolation lint forbids the bundled services to import, so the guarantee cannot be shared without this move. Makes `decision:graceful-shutdown`'s claim that one helper installs the escalation everywhere structurally true. The helper is shared code with consumers in two modules, so it carries its own contract suite that drives a fake signal channel and observes the escalation callback, with no wall-clock value in the verdict.

- **Route all 22 production entry points through that helper.** Every rimsky process that installs a signal handler drains and then escalates on a second signal, so an operator's second Ctrl-C always exits. The 22 sites that call the runtime's signal notify: the role bootstrap shared by scheduler, supervisor and control-api; the container entrypoint; the compose-run and template-run CLI paths; the host agent; the host-agent proxy; the migrate binary; the five long-running CLI verbs (agent, instances, messages, run, watch); the claim-producer shared runner behind both bundled producers; and the nine remaining bundled-service mains. Four install the escalation today and 18 do not. The two bundled claim producers also stop their gRPC servers on a five-second budget where the other bundled services use ten, so this item converges them on the bundled-service window. Depends on the helper item. Makes `decision:graceful-shutdown` true. The host agent's spawned-child grace variable already exists and is public under the surface intent's environment-variables rule, and the decision now names it as the one operator-tunable window.

- **Convert the four sensor state stores to the native pgx interface and deny the adapter repo-wide.** The cron, http, object-store and webhook sensors open their optional Postgres state store through the native driver interface like every other Postgres touchpoint, and the dependency lint denies the driver's standard-SQL adapter package everywhere. Those four files are the only importers of the adapter package in the tree, so the deny rule needs no exemption. The deny names the adapter package specifically and not the standard SQL package itself: the SQLite backend is built on the standard SQL package throughout, and the Postgres queue-park code uses its null and sentinel types. Makes `decision:postgres-pgx-v5`'s repo-wide reading true and mechanical. Covered by the existing sensor state-store tests, which exercise the same store behaviour through the converted interface.

- **Make the migration runner reject a migration whose contents changed after it was applied.** The shared runner records a digest of each applied file alongside its filename and refuses the next boot by name when a recorded file's contents no longer match. Each backend driver's bootstrap step adds the digest column, so the table that records migrations needs no migration of its own; the runner backfills a row already present without a digest on the boot that finds it, so an existing pre-v1 database is not falsely rejected. Makes `decision:migrations-append-only-numbered` and the amended migration invariant of `concept:persistence-database` true. The runner is shared by both backends, so its proof belongs in the persistence conformance suite that already covers migrations, with a case that applies a file, rewrites it, and asserts the next run fails naming that file.

- **Cite the closed destination-allowlist default at the guard and at every service that builds one.** The shared outbound-egress guard and each bundled service that constructs a guard from its operator env var — the http-node executor, the verifier-http executor, and the http sensor — carry a `@decision:` annotation naming the closed default at the point that enforces it, so the choice has an enforcement site instead of auditing as unsupported. The guard's existing unit tests already prove the default blocks loopback, private, link-local, ULA, multicast and metadata destinations, and that a malformed allowlist entry fails construction; no new test is owed. Makes `decision:destination-allowlists-default-closed` true.

- **Re-point the structural-root annotations at the on-demand decision.** Five live files carry the retired slug — the graph module's subscription-edge builder and its tests, the CLI's structural-root helper, the scenario harness, and the empty-message-wake end-to-end scenario test — and each cites `structural-root-edges-derived-on-demand` instead, keeping the citation-resolution lint green and giving the new decision its enforcement sites. Makes `decision:structural-root-edges-derived-on-demand` true. Depends on the corpus delta landing first. No behaviour changes; the existing structural-root injection tests and the empty-message-wake scenario test stay the proof.

- **Disable CGO on every build this tree runs.** The four host targets a developer invokes — the root build, the workspace-wide build, the CLI build, and the containerized build — set CGO off. Every binary produced here is then pure Go and links no C runtime, matching what the image builds and the goreleaser CLI build already do. The build-chain fitness test widens its population from three core Dockerfiles to every build invocation in the tree: the eighteen Dockerfiles that compile Go, the Makefile targets that do, and the goreleaser configuration. Makes `decision:build-cgo-disabled`'s "all builds" literally true.

- **Adopt per-run image tags for verification.** The ok-workspaces suite now materializes a per-run tag script (`run-<12 hex>`, fresh on every call) in place of the content-addressed `tools/image-src-tag.sh`. This tree consumes it. `make test-all`, `make test-in-stack`, and CI's services shard each mint one tag, build the images they verify under it, export it as `RIMSKY_IMAGE_TAG`, and run the suites. The CI shard passes the base-image argument at that tag, because the all-in-one builds `FROM` the rimsky image, and its inline comment names the per-run contract. The services harness resolves every image by `RIMSKY_IMAGE_TAG` alone. It fails loudly, naming the variable and the build command, when the variable is unset or an image under it is missing. The tree-hash fallback goes. `make check-image-freshness` and the `org.rimsky.src-tree` label go. `make reap-images` keys on age alone. The CLAUDE.md gotcha about image resolution is rewritten to the per-run contract. Depends on `/ok` having converged the suite's run-tag payload into this tree, which is the owner's act; until then the builder stages the Makefile and harness changes against the new script path the profile declares. Makes `story:fresh-artifacts-per-run` of the suite true here, and CI a real verification path for the container-backed suites.

- **Make each bundled service image declare exactly the ports its binary opens.** The postgres claim-producer image drops the admin port it declares, because neither producer's admin listener carries a built-in default and only an operator's configuration opens one. The http-node image adds the HTTP bridge port it binds one above its gRPC port. The two verifier images declare the gRPC ports they bind. A fitness check reads each bundled image's declared ports and each binary's built-in defaults, and fails when the two sets differ. Makes `concept:service`'s image-port-declaration invariant true. The check enumerates the same defaults `decision:default-port-allocation` governs, so it lands after the default-port move.

- **Publish every image as a multi-platform index for Intel and ARM.** The push step builds each of the fifteen published images for `linux/amd64` and `linux/arm64` under one tag and keeps the SBOM and provenance attestations. The all-in-one image builds `FROM` the pushed rimsky image, so the multi-platform build resolves that base as an index rather than a single manifest, which constrains the push step's ordering. An operator on either processor then pulls and runs the same tag. Makes `decision:release-distribution`'s image platform matrix true.

- **Fail the release when a pushed tag carries one platform.** A closing step in the release chain reads each pushed tag's platform list back from the registry and fails the release when a tag lacks either platform. Makes `decision:release-chain`'s closing verify step true. Depends on the multi-platform push item.

- **Move the core listeners' default ports into the core block.** The host-agent proxy's agent-facing listener defaults to 8090 instead of 9090, so it no longer takes the claude-agent executor's gRPC default. The supervisor's callback listener, baked at 9100 in the all-in-one image, defaults to 8081, so it no longer takes the filesystem claim producer's gRPC default. The proxy's peer-facing listener moves to 8091 for the same reason, because 9091 is the http-node executor's gRPC default. Every bundled service keeps the default it has. A fitness check enumerates every shipped default across the two populations, fails when one falls outside its block, and fails when two coincide. Makes `decision:default-port-allocation` true.

- **Give every rimsky-authored process the shared log level.** The eleven bundled services, the CLI's two self-host launchers, and the host agent started through the CLI install the JSON handler at the level the shared variable names, replacing the handlers they pin at info. An operator then raises verbosity once and the whole deployment follows. The host agent consumes the log-level field its configuration already loads and defaults, instead of dropping it and logging through the standard library's default text handler. A fitness check enumerates the process entrypoints and fails when one installs a handler whose level the variable cannot reach. Makes `decision:logging-slog-only` and `decision:operator-env-namespaced-per-service` true.

- **Make every test wait declare its class, and fail an unclassified or ordering-dependent poll.** The wall-clock lint reads a per-site class marker on every wait it admits. A wait marked as an outcome-wait passes. A wait marked ordering-dependent fails outright and must block on the event-log tail instead. A wait carrying no marker fails. The lint's recorded baseline empties. The plumbline ratchet test grows the classification arm, so a new ordering-dependent or unclassified poll fails the gate rather than review. Makes `decision:polling-audit` and `decision:test-wallclock-lint-ratchet` true. This is a marker in test source, not user-facing surface.

- **Convert every wall-clock verdict idiom in the suites — all 234 sites across 115 files — to a classified wait.** Each site either becomes an outcome-wait carrying its marker, or blocks on the event-log tail where its pass depended on catching an ordering window: the sub-graph delegation scenario (6), the breakpoint evaluator unit test (15), the CLI watch test (14), the executor callback-receiver conformance test (7), the subscription cascade scenario (7), the four sensor and fan-out scenarios (5–6 each), and every remaining file the baseline names. Depends on the classification item, which supplies the marker vocabulary. The builder stages this by suite group. Makes `decision:polling-audit`, `decision:test-wallclock-lint-ratchet`, and `decision:testing-scenario-based-e2e` true with no backlog.

- **Align the project's determinism rules with the plumbline testing standard.** The "Tests Are Deterministic — There Are No Flakes" section of `.claude/rules/rules.md` keeps only what the plumbline standard leaves to the project: the progress guard and its variable, `-timeout 0`, the `-race` ban, concurrency caps that name their contention, per-test isolation, deterministic seeds, the `Clock` abstraction, and the class marker above. It defers the universal rules — verdict never depends on elapsed time, wait on product events, fix a flake at its cause — to the standard at `.ok-plumbline/docs/testing.md` by reference. Each rule is stated once, in one place.

- **Prove the five dev-loop shortcut verbs are indistinguishable from their grouped forms.** A services scenario test drives both spellings of `ls templates` ~ `template list`, `deploy` ~ `template deploy`, `undeploy` ~ `template undeploy`, `instantiate` ~ `instance create`, and `rm-instance` ~ `instance delete` against one all-in-one stack and compares the flag set each verb reports, the success output in human and JSON form, and the error output and exit code on a missing reference and an undefined flag — normalising hashes, instance ids, and timestamps for the write pairs, which consume their subject. It ports the held probe `assumption-cli-ls-aliases-match-grouped-verbs`. Makes `story:template-lifecycle` and `story:instance-lifecycle` true from the CLI side, and follows `decision:testing-scenario-based-e2e`. The experiment directory stays in the audit's collection; the test is a new scenario, not a move.

- **Prove `compose plan` previews exactly what `compose up` applies and mutates nothing.** A services scenario test drives one compose project through four manifest states — first apply, no-op re-apply, instance entry removed, template entry removed — fingerprinting the live world before and after each `plan`, then comparing the plan's change list against the operations `up` reports applying, operation for operation and object for object. It canonicalises the two known cosmetic differences between the renderings (the truncated template hash, and `tag-delete` against `tag-rm`) so a label difference does not read as a divergence. It ports the held probe `assumption-compose-plan-previews-up`, under the project prefix the compose item above leaves in place. Makes `story:compose-lifecycle`'s plan-before-apply clause true, and follows `decision:testing-scenario-based-e2e`.

- **Prove the `read-only` role grants every action whose verb is `read`, and no write.** A services scenario test mints a key with the `read-only` role against one all-in-one stack, reads the expanded grant back and asserts it is the single `*:read` entry, then drives that key at one route per action for every action ending in `:read` and asserts none is refused by the gate — separating an authorization refusal from the 404 and 400 answers the deliberately absent identifiers produce. It derives the `:read` population from the action registry and asserts its size, so a new read action without a route drive fails loudly. It asserts the same key is refused six writes with 403, and that the two read-shaped actions whose verb is not `read`, `instance:list-frames` and `instance:read-frame`, are also refused, because the wildcard matches the whole verb. It ports the held probe `assumption-read-only-role-covers-every-read-action`. Makes `concept:role-template`'s expansion invariant and `story:grant-scope-enforcement` true, and follows `decision:testing-scenario-based-e2e`.

- **Prove the all-in-one's declared volume carries templates, instances, and event history across a container replacement.** A services scenario test builds a deployment in an all-in-one container over a host directory mounted at the declared state volume — one template, one instance under a fixed key, and a run to quiet — destroys the container, starts a fresh one over the same directory, and reads everything back: the same template id, the same instance id and key, the same event count and kinds, the same messages, and the same per-node run counts. It also asserts a container started with nothing mounted comes up with no templates and no instances, so the mount is what carries the history. It ports the held probe `assumption-all-in-one-state-persists`. Makes `story:single-process-all-in-one` and `story:local-orchestrator-zero-config` true across a restart, and follows `decision:testing-scenario-based-e2e`.

- **Prove every shipped service image name is derivable from its kind and name.** A test checks the image names the build targets produce against the `rimsky-<kind>-<name>` scheme and asserts that the eleven service images match it across all four kinds — claim-producer, executor, sensor, subscriber — and that the four that carry no kind segment are exactly the core images, which name no service kind. It ports the held probe `assumption-image-names-follow-one-scheme`. It needs no running stack, so it belongs beside the other image fitness tests rather than in the scenario suite. Makes `concept:service`'s bundled-image naming true.

- **Prove all four sensors take one connection-string shape against one database and collide on nothing.** A services scenario test gives all four sensor images the same Postgres URL in their own `_STATE_DSN` variables against one database on a private network, reads each once its gRPC port accepts a connection, and asserts each bootstrapped its own state table in that one database with every table belonging to exactly one sensor — including the second table the object-store sensor keeps. A second pass points all four at a host that does not resolve and asserts each exits non-zero naming its state database rather than running stateless. It ports the held probe `assumption-sensor-state-dsn-uniform`. Makes `story:sensor-cron`, `story:sensor-http`, `story:sensor-object-store`, and `story:sensor-webhook` true on their shared state contract, and follows `decision:testing-scenario-based-e2e`. The seventh held probe, `assumption-sensors-are-ha-when-replicated`, is not ported: the owner retired `concept:replica` in the planning session and the probe measures an operator's replication arrangement, not a property rimsky owes.

- **Retire the replica posture tests and reframe the sensor's deterministic-key test.** The sensor-cron replica-posture test is deleted with `concept:replica`. The sensor-cron multi-replica test stays under `concept:sensor`, renamed to what it proves: a sensor derives a deterministic idempotency key per subscription window, so the control API's idempotent send absorbs a second firing of the same window, across a restart or otherwise. Its `@concept: replica` annotation is removed and its cases for distinct subscriptions are kept. Makes `concept:sensor`'s amended invariant the thing the test pins.

- **Name the public embedding modules in the surface intent.** `.ok-planner/surface/surface.md` gains a `## Go modules` section, placed after `## The general rule` and before `## CLI verbs`, so the extractor stops defaulting the two unnamed modules internal. The builder applies this wording verbatim: "## Go modules\n\nThe root module `github.com/rimsky-ai/rimsky-core` and the protocols module `github.com/rimsky-ai/rimsky-core/lib/protocols` are the public embedding surface. A consumer fetches either at a release version and imports it. The foundation module and the services module are internal: they carry no tags, they resolve only through workspace replacements, and the lint denies a consumer the foundation module's internal packages."

- **Name the three non-prefixed public environment variables in the surface intent, and rule the platform variables out.** The intent's `## Environment variables` section gains the exceptions, so the extractor stops defaulting all five non-prefixed variables internal. The builder replaces that section's body verbatim with: "Every `RIMSKY_*` variable the shipped code reads is public. This includes variables rimsky sets for itself when it spawns its own processes, such as `RIMSKY_PROCESS_ROLE`, and variables a spawned third-party binary must read, such as `RIMSKY_AGENT_PORT`. Three variables outside the prefix are public exceptions: `ANTHROPIC_API_KEY` and `CLAUDE_CODE_OAUTH_TOKEN`, the vendor credentials the claude-agent executor reads and passes to its child, and `NO_COLOR`, the convention the CLI honours when it decides whether to colour its output. Platform variables the shipped code reads — `HOME` and `PATH` — are not surface. Variables read only by tests are not surface and are not enumerated."

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
   one line per stage, each marked pending. Seed the closing stages
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
   edits nothing. Every dispatch names its model.
   - **The builder** (`opus`), dispatched once with this sprint's
     path and the report's path, fed one stage per message. It
     writes the code, applies the stage's corpus deltas, tests what
     it built, marks the stage in the report with what it did, and
     stands by. It fixes the reviewer's findings in its own context
     when they arrive.
   - **The standing reviewer** (`opus`), dispatched once under the
     standing-reviewer brief in the certification core
     (`_shared/certification-core.md` under `.claude/skills/`), fed
     each landed stage's paths. It reads the increment under the
     certification gate's code-review brief plus the read-only
     per-stage producers each present family's ceremony contribution
     names under **Standing producers**, keeps a ledger of open
     findings, and replies with the ledger. It reports each claimed
     fork outside the ledger, in every reply until the completion
     report carries it. It edits nothing and runs no suite.
   - **The relay.** The session runs the relay protocol stated with
     that brief in the certification core: the message it sends the
     reviewer as each stage lands, the lines and claimed forks it
     relays back to the builder, the fix-only rounds it runs after the
     final stage, and the bound on those rounds.
   - **Retirement.** Retire a worker only at a stage boundary, once
     its measured context (`subagent_tokens`) passes a threshold held
     below the harness's compaction window (~300k tokens on a
     1M-token window). A replacement builder reads this sprint and
     the report and continues at the next stage; a replacement
     reviewer receives the open ledger and the open claimed forks the
     session holds.
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
report, its delta sidecar, and the issue files it resolved to
`history/`, commits the work, then stamps the archived sprint with
the closing commit — `closed: <sha>` in the frontmatter, one small
follow-on commit. The next planning ceremony reads that stamp to
detect work done out of band. "Finish the sprint" and "follow the
boilerplate" are not a yes; both ask for the presentation.

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
checking. A missing completion report means not done. A run parked
at the review-fix loop's cycle cap awaiting the owner's direction
has not met the goal: a legal in-flight state, not done, not failed,
and never grounds for the run to take either cap step itself.
Nothing else counts either way.
