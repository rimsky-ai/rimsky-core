# Design Corpus Bootstrap — Specification

**Spec slug:** `2026-06-08-design-corpus-bootstrap`
**Status:** draft, awaiting user approval

## Overview

This spec bootstraps two durable artifact catalogs that the rimsky design model has not yet had — `design/stories/` (user-outcome stories describing what the product does) and `design/decisions/` (technical decisions describing what the project has decided) — and populates them comprehensively against the project's current shape.

It does three things in one unit of work:

1. **Backfill** every user-outcome story that shipped via the 2026-06-06 comprehensive gap-closure work (commit `ee6eb53`) into the new durable shape (Acceptance / Falsifier / Proof), with a committed proof artifact per story that exhibits the capability through the assembled product.
2. **Bootstrap** `design/stories/` and `design/decisions/` as durable artifact catalogs with the same source-of-truth weight as `design/concepts/`.
3. **Expand** the corpus with stories for currently-implicit user-facing capabilities (capabilities the product supports but never had a story) and decisions for currently-implicit architectural choices (choices baked into the code but never enumerated).

The systematic sweep that informed this spec walked every user-reachable surface (CLI verbs, control-API routes, MCP tools, gRPC service protocols, bundled-service implementations) and every decision surface (workspace + module layout, depguard rules, library choices, conventions, build / image scheme, persistence, protocol design, auth / permission, release flow, pre-v1 policy, licensing). Each story landed in one cluster organized by persona; each decision landed in one surface.

Proof artifacts follow choice A: an existing test file that drives the assembled product through the real surface, triggers the story's acceptance condition, and exhibits the observable outcome with the value-delivering component real, qualifies as that story's proof. Where no qualifying artifact exists, a fresh proof is authored. Existing-artifact citations in this spec name real file paths; they are spec scaffolding consumed by `write-plan`, not part of the durable story bodies that land in `design/stories/`.

**Proof line convention.** Each story's `Proof:` line names the artifact form (demo / example / executable proof / all-of-the-above). The artifact's content is the story's Acceptance exhibited through the assembled product — the artifact drives the real surface, triggers the trigger, and exhibits the outcome. The Falsifier line names what to look for if the exhibition fails (a stubbed component, an unwired entry point, a missing emitted effect, a canned response). Where a story's Proof line gives only the form ("executable proof."), the artifact is the Acceptance literally exhibited end-to-end against the assembled product; where the Proof line names something specific ("`examples/X/` extended with a worked walkthrough"), the artifact's content is constrained further.

## User outcomes

### Cluster 1 — Operator entity lifecycle

**STORY-template-lifecycle** — As an operator, I can register a workflow definition with rimsky, mark it ready to run, create live instances of it, retire it when I don't want new instances, and remove it once nothing's using it, so that I curate the catalog of workflows my stack offers.
- **Acceptance:** Through the control-api or the `rimsky template …` CLI, an operator submits a template definition; afterward, the same operator can retrieve it by name or content hash, can mark it deployed and from that point create instances of it that proceed to run, can mark it undeployed and from that point have new instance-creation refused, and can delete it once no instance references it. The operator can also pre-flight a definition through a validation surface and get back findings without the template being persisted.
- **Falsifier:** Deployed-vs-undeployed state is recorded but not gated on at instance creation (an undeployed template still produces a running instance), OR pre-flight validation persists, OR delete succeeds while live instances reference the template.
- **Proof:** executable proof exercising the full lifecycle against the assembled all-in-one stack.
- **Existing artifact:** `fresh-proof-needed`.

**STORY-instance-lifecycle** — As an operator, I can create a live instance of a deployed template, watch its progress, pause and resume it, force-terminate it when it's wedged, and remove its record once it's done, so that I drive an instance's runtime existence and intervene when something goes wrong.
- **Acceptance:** Through the control-api or `rimsky instance …` CLI, an operator creates an instance of a deployed template; afterward, the supervisor begins dispatching its nodes. The operator can pause the instance — the supervisor stops claiming new dispatches against it — and resume — the supervisor picks it up again. The operator can force-terminate an instance whose node is wedged awaiting an executor callback that never arrives; the wedged node-run transitions to a terminal state through the real lifecycle path (not by a direct write to the row), the main run-scope closes, and the operator can then delete the instance record. Deleting a non-terminal instance is refused.
- **Falsifier:** Pause is recorded but the supervisor keeps dispatching against the instance, OR force-terminate writes a row but doesn't propagate to the in-flight node-run, OR delete succeeds non-terminal.
- **Proof:** executable proof.
- **Existing artifact:** `test/scenarios/lifecycle_force_terminate_fullstack_test.go` covers the force-terminate leg end-to-end; the rest of the lifecycle (pause/resume/delete/create/get/list) needs fresh proof or the existing file extended.

**STORY-tag-management** — As an operator, I can create a movable name for a template hash, list and resolve current tag bindings, re-point a tag to a different template hash, and remove a tag I no longer need, so that I version a deployable name and roll forward or back without disrupting in-flight instances.
- **Acceptance:** Through the control-api or `rimsky tag …` CLI, an operator binds a tag to a template hash; afterward, instance creation against the tag uses that hash. Re-binding the tag to a different hash atomically redirects subsequent instance creation to the new hash without affecting instances already created under the old binding. Deleting a tag makes the name no longer resolvable for new instances.
- **Falsifier:** Tag rebind isn't picked up by subsequent instance creation (resolves to the prior hash), OR tag deletion leaves the name still resolving.
- **Proof:** executable proof.
- **Existing artifact:** `fresh-proof-needed`.

**STORY-node-admin** — As an operator, I can inspect a node's full state on a running instance, force it stale to re-fire it, target the invalidate at either a freshly-enqueued frame or the cascade frame currently running, and reset a failed node's error counter, so that I drive a stalled or errored node back into the cascade without re-instantiating.
- **Acceptance:** Through the control-api or `rimsky admin …` CLI, an operator retrieves a node and sees its current state and settling signal type; force-invalidating a node causes the supervisor to re-fire it on a real dispatch; invalidating with the in-cascade option joins the running cascade frame and the node settles inside that frame rather than the next one; resetting a failed node clears its error count and the next acquisition attempt is not skipped due to error budget exhaustion.
- **Falsifier:** Invalidate flips state but the supervisor never picks the node up, OR the in-cascade option produces a separate frame rather than joining the running one, OR reset clears the visible counter but the supervisor still treats the node as exhausted.
- **Proof:** executable proof.
- **Existing artifact:** `test/scenarios/cascade_operator_frame_in_e2e_test.go` covers the in-cascade invalidate leg; get and reset legs need fresh extension.

**STORY-message-bus** — As an operator or publisher, I can emit messages into a live instance's bus with a mandatory dedup key, see those messages in the instance's message history, retrieve a specific one by ID, and have a replay return the original message without producing a duplicate, so that downstream nodes consume the bus reliably and no replay slips through.
- **Acceptance:** A sender (operator or publisher, via the control-api `POST /instances/{id}/messages` or its MCP equivalent) emits a message carrying a dedup key; the message is persisted and visible in the instance's message history. A second emission with the same key returns the original message identifier and produces no second envelope. A request with no dedup key is refused. Senders with structurally distinct identities (operator vs. publisher; one operator key vs. another) do not replay each other when they happen to choose the same dedup key.
- **Falsifier:** A second emission with the same key produces a second envelope, OR the no-key request is silently accepted, OR a publisher named the same as an operator-sender replays the operator's emit.
- **Proof:** executable proof.
- **Existing artifact:** `lib/control/controlapi/idempotency_matrix_test.go` covers the per-status matrix; `lib/control/controlapi/idempotency_sender_subject_test.go` covers cross-sender isolation; together qualify.

**STORY-event-log-read** — As an operator, I can read the unified event log of an instance and see node lifecycle transitions, breakpoint hits, message activity, and supervisor decisions in true chronological order across kinds, with filtering by kind and time, so that I reconstruct what happened on an instance from one feed.
- **Acceptance:** Through the control-api or `rimsky watch`/`rimsky logs` CLI, an operator reads the events for an instance and observes a single timestamp-ordered feed where (for example) a breakpoint hit that occurred between two node-state transitions appears between them — not grouped by source — and where filtering by kind narrows to that kind while preserving the order across what remains.
- **Falsifier:** Events are returned source-grouped rather than timestamp-ordered, OR a breakpoint hit that actually occurred between two events appears outside the window.
- **Proof:** executable proof.
- **Existing artifact:** `lib/services/test/scenarios/cli_watch_chronological_e2e_test.go` covers chronological ordering through `rimsky watch`; `test/scenarios/breakpoints/hit_emits_event_test.go` covers the breakpoint-on-feed acceptance.

**STORY-audit-log-read** — As an operator, I can read the audit log of every auth-relevant action against the deployment — key creates, revokes, rotates, dry-run-mode access attempts, denied attempts — with filtering, so that I see who did what to the rimsky stack and when.
- **Acceptance:** Through the control-api's `GET /audit` route (gated by `audit:read`), after an admin mints / revokes / rotates keys and a non-admin caller triggers an access denied, the audit log returns each event in timestamp order carrying actor identity, action name, outcome, and resource target.
- **Falsifier:** A real access denied doesn't appear in the audit, OR dry-run-mode attempts are absent, OR actor identity is dropped from the record.
- **Proof:** executable proof.
- **Existing artifact:** `test/scenarios/auth/audit_read_test.go` covers the audit read surface.

**STORY-breakpoint-debugger** — As an operator, I can install a breakpoint on a running instance's checkpoint, see hits appear both on the unified event log and on the breakpoint-hits ledger, resume a paused hit with an attribute overlay that the supervisor applies on re-fire, and delete a breakpoint to cascade-clear its hits, so that I debug a live instance.
- **Acceptance:** Through the control-api or MCP, an operator installs a breakpoint on a node's checkpoint; when the supervisor evaluates the checkpoint, the hit appears co-transactionally in both the breakpoint-hits ledger and the unified event feed (a debugger tailing the event stream sees the hit); resuming a paused hit with an attribute overlay causes the next dispatch to actually carry the overlaid attributes; deleting the breakpoint removes both it and its hits.
- **Falsifier:** Hit appears on one surface but not the other (not co-transactional), OR resume's overlay isn't applied at the next dispatch, OR breakpoint deletion leaves orphaned hits.
- **Proof:** executable proof.
- **Existing artifact:** `test/scenarios/breakpoints/hit_emits_event_test.go` covers the unified-feed leg; install / list / delete / resume legs need fresh extension.

**STORY-asset-management** — As an operator, I can list the data assets a running instance has produced, see the current version of each, materialize a new version on demand, walk the version history and materialization audit, retire an asset, and trace its lineage to consumers, so that I manage the data outputs nodes produce.
- **Acceptance:** Against an instance running a template whose nodes declare durable claims against a data-processing-capable producer (the asset construction per `concept:asset`), the operator queries the instance's assets through the control-api and sees each asset alias with its current version; triggering a re-materialization causes the supervisor to dispatch the producing node again and a new version row appears as a result of that real dispatch; the materialization-history surface lists each materialization with its outcome; deleting an asset removes the alias.
- **Falsifier:** Materialize trigger doesn't actually cause a producing dispatch, OR the version-history surface returns rows that don't match what really materialized.
- **Proof:** executable proof.
- **Existing artifact:** `fresh-proof-needed`.

**STORY-backfill-ops** — As an operator, I can start a backfill on an instance's asset, override which partitions get re-processed, watch per-partition progress, and cancel a running backfill mid-flight, so that I re-process historical data through the live pipeline without bouncing the template.
- **Acceptance:** Through the control-api or `rimsky backfill …` CLI, an operator starts a backfill on an asset with a partition-selector override; the supervisor materializes runs against the overridden selector (not the template default) and drives them to terminal through the real dispatch path; the per-partition progress surface reflects what actually happened; cancelling a running backfill aborts the in-flight partitions through the real supervisor cancel path.
- **Falsifier:** Override silently dropped (supervisor uses template default), OR cancel is recorded but in-flight partitions keep running, OR the per-partition progress lies about what dispatched.
- **Proof:** executable proof.
- **Existing artifact:** `test/scenarios/lifecycle_force_terminate_fullstack_test.go` covers the partition-selector-override leg through real dispatch; cancel and per-partition-progress legs need fresh extension.

**STORY-lineage-exploration** — As an operator, I can walk the lineage of a run forward and backward, query lineage by claim handle, and pivot through source or named producer, so that I trace how data flowed through the rimsky stack.
- **Acceptance:** After running an instance whose template produces lineage records, an operator queries the lineage for a run through the control-api and walks upstream to the producers that fed it and downstream to consumers that depended on it; query by claim handle returns the lineage record for that claim; the source-pivot and producer-pivot return the records they should — a producer the run actually used appears in upstream, a consumer that actually consumed appears in downstream.
- **Falsifier:** A real upstream producer is missing from the ancestor walk, OR a real downstream consumer is missing from the descendant walk.
- **Proof:** executable proof.
- **Existing artifact:** `fresh-proof-needed`.

**STORY-lineage-admin** — As an operator, I can prune lineage records older than a cutoff timestamp, so that the lineage table doesn't grow unbounded in a long-lived deployment.
- **Acceptance:** With lineage records of varied ages persisted, an operator submits a prune request through the control-api or `rimsky lineage prune` carrying a cutoff; only records strictly older than the cutoff are removed, records at or after the cutoff are untouched (verifiable through a follow-up lineage query).
- **Falsifier:** Prune removes records at the cutoff boundary, OR removes records newer than cutoff, OR silently drops the cutoff and returns a no-op count.
- **Proof:** executable proof.
- **Existing artifact:** `fresh-proof-needed`.

**STORY-api-key-management** — As an operator, I can bootstrap the first admin key on a fresh deployment, mint scoped keys with roles, list and inspect existing keys without seeing plaintext, revoke a key so it stops being accepted, rotate one (new plaintext now, old key kept usable through a grace window), and check the current auth status, so that I administer credentials end-to-end.
- **Acceptance:** Against a fresh deployment with no keys, the operator bootstraps an admin key through `rimsky auth init` and receives plaintext exactly once. With the admin key, the operator mints scoped keys; subsequent metadata reads never expose plaintext. Revoking a key causes subsequent requests bearing that key to be refused. Rotating a key produces new plaintext and the old key keeps working through its grace window, then stops. The auth-status surface reports the current mode and active key count.
- **Falsifier:** Revoke leaves the old plaintext still accepted, OR the rotated key's grace window collapses to zero (old key dies immediately) or never expires, OR auth-init succeeds when the keys table is non-empty.
- **Proof:** executable proof.
- **Existing artifact:** `test/scenarios/auth/lifecycle_test.go` covers the key-management lifecycle.

**STORY-runtime-diagnostics** — As an operator, I can inspect the parked nodes, the live wait-set edges, the frames a holding-subgraph is gripping, and the current holders of a claim, so that I see why the runtime is wedged when an instance isn't progressing.
- **Acceptance:** With an instance whose nodes are parked, gated on senders in the wait-set, and holding a claim, the operator queries the parked-node, wait-set, held-frames, and claim-holders surfaces through the control-api or MCP and sees the parked nodes with resume reason, the receiver-waiting-for-sender edges the supervisor is actually consulting, the held frames, and the current holders.
- **Falsifier:** A parked node that's really parked isn't on the parked surface, OR a wait-set edge the supervisor is consulting is missing from the wait-set surface.
- **Proof:** executable proof.
- **Existing artifact:** `test/scenarios/parked_lifecycle_test.go` covers the parked-node leg; others `fresh-proof-needed`.

**STORY-client-context** — As an operator on a dev machine, I can register multiple control-api endpoints in the `rimsky` CLI, switch between them, and inspect or remove them, so that I run commands against several deployments without flag plumbing.
- **Acceptance:** The operator registers a context naming a control-api endpoint, switches the active context to it, and from that point CLI commands hit that endpoint; switching to a different registered context redirects subsequent commands. Inspecting the active context names the current endpoint; removing a context makes it no longer switchable to.
- **Falsifier:** Switched context isn't picked up by the next command, OR removed context still resolves.
- **Proof:** demo — a runnable script walking through register / switch / use / remove, with two real local control-api endpoints to make the switch observable.
- **Existing artifact:** `fresh-proof-needed`.

### Cluster 2 — Operator workflows, permissions, debugging surfaces

**STORY-operator-onboarding** — As a new operator with no prior rimsky experience, I can copy an example workflow from the shipped `examples/` module, run a single CLI verb against my local stack, and watch the resulting instance run to completion, so that I learn the dev loop end-to-end without writing a template from scratch.
- **Acceptance:** An operator without prior template-writing experience copies a templatespec from the `examples/` module, runs `rimsky run <file>` against a running all-in-one stack, observes the command print an instance ID and exit cleanly, can look the instance up through the standard list/get surfaces, and watches it progress to a terminal state through the real supervisor. A second assertion confirms the README's documented `rimsky run` invocation succeeds as written.
- **Falsifier:** The shipped example isn't a real runnable templatespec (would need modification to run), OR `rimsky run` is a stub that prints a fake ID without driving register + deploy + instantiate.
- **Proof:** demo — a runnable shell sequence the README points to as the first-steps walkthrough.
- **Existing artifact:** `fresh-proof-needed`.

**STORY-compose-lifecycle** — As an operator declaring a workflow's templates, tags, and instances together as a manifest, I can apply that manifest to a running rimsky and have rimsky reconcile state to match, namespace the resources under a compose tag, plan and inspect status before applying, and tear it all down with one command, so that I drive multi-resource changes as one declarative unit.
- **Acceptance:** An operator writes a `rimsky-compose.yml` declaring multiple templates, tags, and instances; `rimsky compose up <manifest>` reconciles them into a running rimsky (each resource visible via the standard list surfaces, each carrying a `compose:<project>:`-prefixed tag bound to the manifest's project); `rimsky compose plan` reports the diff without applying; `rimsky compose status` reports current state vs. manifest; `rimsky compose down` removes the project's resources cleanly without touching unrelated ones. No member operation invokes infrastructure (docker, kubectl) and no member operation is stubbed.
- **Falsifier:** Any compose verb returns without performing its reconcile, OR `compose down` touches resources outside the project namespace, OR a compose verb shells out to docker/kubectl.
- **Proof:** executable proof.
- **Existing artifact:** `lib/services/test/scenarios/cli_compose_up_down_e2e_test.go` covers the compose lifecycle end-to-end.

**STORY-compose-namespace-guard** — As an operator running rimsky behind multiple tools, I can trust that the `compose:` prefix on tag and instance-key namespace is reserved for the compose machinery alone — any other client attempting to create a `compose:`-prefixed resource is refused at the server regardless of which client (CLI, raw HTTP, MCP) it comes from — so that compose-managed namespace stays disjoint from manually-authored artifacts.
- **Acceptance:** A non-compose-CLI caller (raw HTTP, MCP) attempting to create a tag or instance key prefixed with `compose:` is refused with a clear diagnostic and no persisted row results; the compose machinery itself, holding the appropriate capability, succeeds at creating such resources.
- **Falsifier:** A non-compose caller holding `tag:create` or `instance:create` succeeds at creating a `compose:`-prefixed resource (the guard is client-side only, not server-enforced), OR the compose machinery is also refused.
- **Proof:** executable proof.
- **Existing artifact:** `lib/services/test/scenarios/control_api_compose_prefix_guard_e2e_test.go` covers the server-enforced guard including the permission-gated leg.

**STORY-mcp-transport** — As an operator or AI agent using rimsky through an MCP client (e.g., Claude Code), I can perform every read and mutation through the MCP tool surface that the HTTP surface offers, with the same auth and permission semantics, so that an agent can drive rimsky deployments without a custom client.
- **Acceptance:** An MCP client connecting to a running rimsky's MCP endpoint discovers a tool catalog covering the templates / tags / instances / nodes / messages / events / audit / breakpoints / assets / backfills / lineage / diagnostics / auth surfaces; invoking a tool mirrors the equivalent HTTP route — the same auth gate fires, the same observable state results, and the same response is returned through the MCP wire.
- **Falsifier:** An MCP tool gate is weaker than the equivalent HTTP route's gate (bypasses auth), OR an MCP tool returns a canned response without invoking the real handler.
- **Proof:** executable proof.
- **Existing artifact:** `fresh-proof-needed` for the catalog-discoverability + parity proof across the tool surface.

**STORY-anonymous-mode-bootstrap** — As an operator bringing up a fresh rimsky deployment on a dev machine, I can use it without minting credentials first — anonymous mode is open, every action succeeds — and the moment I mint the first admin key, anonymous mode closes and subsequent unauthenticated requests are refused, so that I can experiment freely on first run and lock down the moment I'm ready.
- **Acceptance:** Against a fresh deployment with no api-keys, requests through the control-api and CLI succeed without bearer tokens; `rimsky auth init` mints the bootstrap admin key (plaintext returned exactly once) and from that point unauthenticated requests are refused; the status surface accurately reports the deployment's auth mode throughout.
- **Falsifier:** Anonymous mode stays open after a key is minted, OR `rimsky auth init` succeeds on a deployment that already has keys, OR the status surface lies about which mode is active.
- **Proof:** executable proof.
- **Existing artifact:** `fresh-proof-needed`.

**STORY-dry-run-request-flag** — As an operator about to make a potentially destructive change, I can submit any write request with a per-request dry-run flag and get back a synthetic envelope showing what would have happened — same validation as a live write, no persistence — so that I preview the effect before committing.
- **Acceptance:** An operator sends a write request (instance create, tag bind, template register, etc.) with the dry-run flag set; the response carries the dry-run marker and the synthetic envelope describing the would-have-been outcome; no row is persisted (verified via a subsequent list/get); a read request with the same flag genuinely executes (reads are flag-no-ops).
- **Falsifier:** A dry-run write persists state, OR returns a canned envelope unrelated to the inputs (validation didn't actually run), OR a read returns the dry-run marker.
- **Proof:** executable proof.
- **Existing artifact:** `test/scenarios/auth/dry_run_test.go` covers the request-flag path.

**STORY-dry-run-mode-floor** — As an operator delegating control-plane access to an autonomous agent, I can mint an api-key whose grant pins write actions to dry-run mode — the key can preview every write but never commit one — so that I have attempt-only credentials that are safe to hand out.
- **Acceptance:** An operator mints an api-key whose grant carries `mode: dry_run` on a write action; using that key, an operator or agent issues a write request without the per-request dry-run flag and receives the synthetic envelope back; no row is persisted; the audit log records the attempt with executed-false. A second ordinary write-capable key issued by the same operator performs the same request and creates a real row — proving the floor is carried by key identity, not by the request flag.
- **Falsifier:** A dry-run-pinned key's write actually persists state, OR the audit misses the attempt, OR no comparison shows the floor is identity-bound.
- **Proof:** executable proof.
- **Existing artifact:** `test/scenarios/auth/dry_run_identity_bound_test.go` covers the identity-bound floor.

**STORY-grant-scope-enforcement** — As an operator delegating control-plane access to a per-tenant agent, I can scope an api-key's grant to a specific resource (e.g., a template-tag), with the permission matcher refusing requests against any other resource of the same action across the resource's full lifecycle, so that least-privilege delegation is enforced rather than just believed.
- **Acceptance:** An operator mints an api-key whose grant scopes a write action to a single resource (e.g., `template_tag: "analytics"`); an in-scope request succeeds; an out-of-scope request of the same action is refused with the auth-denied audit row attributing the refusal to scope, not action; the out-of-scope resource is not created. Scope enforcement covers the full lifecycle of the resource (register, deploy, undeploy, deregister, tag set, tag delete, instance create), not just register.
- **Falsifier:** An out-of-scope request succeeds, OR a same-action operation later in the lifecycle silently bypasses the scope check.
- **Proof:** executable proof.
- **Existing artifact:** `test/scenarios/auth/grant_scope_lifecycle_test.go` covers full-lifecycle scope enforcement.

**STORY-forensic-last-attribute** — As an operator debugging a node that has run at least once, I can read the node's most recent resolved attribute bag from the read surfaces directly, instead of hand-reconstructing it from the event log, so that I see what values the node actually computed without forensic effort.
- **Acceptance:** After a node executes at least once, the operator queries the node through the control-api and observability surface and sees the latest resolved attribute bag — the values that were dispatched to the executor, read from real persistence — in the response. When the node has executed across multiple runs, the surface returns the most recent run's bag.
- **Falsifier:** The latest-attribute surface returns an earlier run's bag (stale), OR returns synthesized values, OR is absent on a node that has executed.
- **Proof:** executable proof.
- **Existing artifact:** `test/scenarios/observability_latest_attribute_fullstack_test.go` covers the full-stack read.

**STORY-rules-doc-accuracy** — As a contributor following the project's after-code-changes verification rules, I can trust that every path and command the rules document instructs me to run actually exists — no missing directories, no stale make target, no non-existent test path — so that acting on the documented verification steps doesn't hit a missing surface.
- **Acceptance:** An automated accuracy check over `.claude/rules/rules.md` parses every filesystem path and make-target the rules cite, resolves each, and fails if any doesn't exist. Mutating `rules.md` to cite a non-existent path makes the check fail; the check passes only when every citation resolves to a real artifact.
- **Falsifier:** The check accepts a non-existent path (text-search only, no resolve), OR the check is informational and doesn't fail CI.
- **Proof:** executable proof.
- **Existing artifact:** `tools/rulesdoc/rulesdoc_test.go`.

### Cluster 3 — Template author

**STORY-claim-scope-substitution** — As a template author, I can use the canonical `{{claim.<alias>.claim_scope}}` substitution in a node's attributes and have it resolve at runtime to the live claim's claim-scope bytes, with the legacy `scope` spelling cleanly refused at registration, so that I have one canonical spelling end-to-end.
- **Acceptance:** A template using `{{claim.<alias>.claim_scope}}` registers without complaint, instances of it dispatch with the executor receiving the resolved claim-scope bytes for that attribute. A template using the legacy `{{claim.<alias>.scope}}` is refused at registration with a clear validation message naming the rename.
- **Falsifier:** Legacy `scope` spelling is silently accepted, OR canonical `claim_scope` resolves to empty / wrong bytes at dispatch.
- **Proof:** executable proof.
- **Existing artifact:** `test/scenarios/stores/claim_scope_directive_e2e_test.go`.

**STORY-substitution-doc-accuracy** — As a template author reading the substitution module documentation, I can trust that the listed source kinds match exactly what the resolver actually recognizes, so that I don't silently miss a supported source.
- **Acceptance:** An automated accuracy check parses the substitution module's header enumeration of source kinds and asserts it matches the set of source kinds the resolver actually dispatches on (the live `case` arms). The check fails when the header undercounts, omits a real kind (trigger, child), or lists a kind the resolver doesn't handle.
- **Falsifier:** The check is informational only (doesn't fail CI), OR text-matches the doc without ASTs over the resolver code.
- **Proof:** executable proof.
- **Existing artifact:** `lib/graph/attribute/substitution_test.go` — the accuracy gate (header-enumeration vs. resolver case-arm cross-check via `headerBulletPattern`) lives in this file.

**STORY-ref-validation-mode** — As an operator setting up a staged bring-up where templates register before all referenced services exist, I can choose a registration-time reference-validation strictness mode — `all` (refuse anything missing), `available` (validate only what's provisioned), `none` (skip altogether) — with whatever a relaxed mode lets through caught at the mandatory instantiation gate, so that infra-as-code bring-up is an explicit operator choice rather than implicit heuristic.
- **Acceptance:** With the `all` mode, registering a template whose node references a not-yet-provisioned executor / store / lock is refused with a clear missing-reference error. With the `available` mode, the same registration succeeds while still validating refs to provisioned services. With the `none` mode, registration succeeds with no registration-time ref validation. In every mode, whatever the relaxed strictness let through is caught by the mandatory instantiation gate before the instance runs.
- **Falsifier:** Any mode's stated behavior isn't realized (strict accepts missing refs, or available rejects a real-reference-to-provisioned-service), OR the implicit always-on soft-fail heuristic is still present alongside the explicit modes.
- **Proof:** executable proof.
- **Existing artifact:** `test/scenarios/attributes/ref_validation_mode_e2e_test.go`.

**STORY-mandatory-instantiation-gate** — As an operator creating an instance from a deployed template, I can trust that rimsky validates statically-knowable attribute config against every referenced service's schema — including value constraints, not just shape — and refuses the create with a clear error if anything is statically misconfigured, so that bad config is caught at create time rather than as a mid-run dispatch failure.
- **Acceptance:** With a template referencing an executor whose schema declares value constraints (e.g., a `minimum: 0` on a numeric attribute), creating an instance whose attributes violate the constraint (e.g., a negative value) is refused with a clear validation error naming the offending attribute and the violated constraint; the instance is not persisted and nothing runs. A well-formed instance of the same template succeeds.
- **Falsifier:** Value-constraint violation slips through, OR the rejection cites only a shape error rather than the value-constraint violation.
- **Proof:** executable proof.
- **Existing artifact:** `test/scenarios/attributes/instantiation_static_config_gate_e2e_test.go`.

**STORY-lenient-marker** — As a template author, I can mark a substitution directive lenient with `?` so a missing source resolves to empty at runtime instead of failing dispatch, so that I can write templates that gracefully handle optional upstream inputs.
- **Acceptance:** A template node setting an attribute via a `?`-marked directive whose source is absent at dispatch dispatches successfully (the executor receives the resolved-empty attribute) and the node-run reaches terminal. A companion template using the same directive without `?` against the same absent source fails dispatch with a clear missing-source error.
- **Falsifier:** The `?` marker is silently treated like no-marker (lenient dispatch fails when source absent), OR no-marker is silently treated like `?`.
- **Proof:** executable proof.
- **Existing artifact:** `test/scenarios/attributes/lenient_marker_recovery_test.go`.

**STORY-verifier-severity-partition** — As a template author declaring data-quality checks, I can label a check `warning` or `error` and have the verifier honor the partition — failing-warning is non-blocking (the run still succeeds), failing-error blocks the commit — so that I distinguish observed-but-tolerated quality issues from blocking ones.
- **Acceptance:** With a template whose verifier node carries one `severity: warning` failing check and one `severity: error` passing check against an in-bounds dataset, the dispatch reaches terminal success and the observability surface records the failed check as warning. A second dispatch against an out-of-bounds dataset that flips the `severity: error` check to failing reaches a terminal error and the commit is blocked.
- **Falsifier:** Warning blocks commit, OR error doesn't block commit, OR the severity field is declared but unused.
- **Proof:** executable proof.
- **Notes:** Severity is a free-form string today; the runtime partitions on exact-string `warning` (non-blocking) and treats every other value, including the documented `error` and any typo, as blocking. The two-string convention `warning`/`error` is the contract this story exercises; the open `tension:quality-rule-severity-string-footgun` tracks the typo footgun for separate resolution.
- **Existing artifact:** `lib/services/executors/verifier-shape-checks/server_test.go` and `validation_test.go` cover in-process behavior; cross-stack severity exhibition needs verification.

**STORY-template-fan-out** — As a template author writing a data-pipeline template, I can declare a fan-out node whose claim partitions into sub-claims and have rimsky dispatch one work unit per sub-claim concurrently, with the parent settling once all sub-claims resolve, so that I express parallel partition processing as a single template declaration.
- **Acceptance:** A template with a fan-out node whose claim-producer supports `SplitScope` and returns N sub-scopes; when the instance runs, rimsky materializes N sub-claims and dispatches one node-run per sub-claim concurrently against the producer's executor; once all N sub-runs reach terminal, the parent fan-out node settles with an aggregate outcome reflecting the sub-claims' resolutions.
- **Falsifier:** Sub-claims are materialized but not dispatched concurrently, OR the parent settles before all sub-claims resolve, OR aggregate outcome doesn't reflect the sub-claim resolutions.
- **Proof:** executable proof.
- **Existing artifact:** `fresh-proof-needed`.

**STORY-template-sub-graph-delegation** — As a template author composing larger workflows, I can declare a node that delegates to a named sub-graph and have it dispatch the sub-graph as its execution unit, with the calling node settling once the sub-graph settles, so that I compose workflows from reusable units.
- **Acceptance:** A template declaring a node with `delegate: <graph-name>` and a separate template providing the named sub-graph; when the parent instance runs, rimsky dispatches the sub-graph (with its own entry/exit nodes) as the delegating node's execution; once the sub-graph settles, the delegating node settles with the sub-graph's terminal outcome propagated back.
- **Falsifier:** The delegate node settles before the sub-graph does, OR the sub-graph's terminal outcome doesn't propagate to the parent.
- **Proof:** executable proof.
- **Existing artifact:** `fresh-proof-needed`.

**STORY-template-error-policy** — As a template author writing fault-tolerant workflows, I can declare per-error-class routing actions (`pass`, `give_up`, `retry`, `discard_claims_then_retry`) and have the runtime honor each action at the appropriate error site, so that I express graceful failure handling without writing handlers.
- **Acceptance:** A template declaring `error_types:` entries mapping specific error classes to each of the four actions; when a node errors with a class mapped to `pass`, the cascade continues as if the node had succeeded; mapped to `give_up`, the node-run terminates and downstream nodes are not dispatched; mapped to `retry`, the runtime re-dispatches the node; mapped to `discard_claims_then_retry`, held claims are released before re-dispatch.
- **Falsifier:** Any of the four actions has no observable effect (the runtime acts the same regardless of the declared action), OR an action's effect doesn't match the declaration.
- **Proof:** executable proof.
- **Existing artifact:** `fresh-proof-needed`.

**STORY-template-subscriptions** — As a template author wiring upstream-event-driven nodes, I can declare a `subscribes:` entry with a canonical signal type-path (exact or trailing-`*` prefix) plus an optional CEL predicate over the signal payload and have the runtime fire the node only when a matching signal arrives whose payload satisfies the predicate, so that I write reactive nodes that filter precisely on what triggers them.
- **Acceptance:** A template with a node declaring a subscription to a signal type-path with a CEL predicate (e.g. `payload.tenant == "alpha"`); when the runtime produces a signal of that type whose payload matches the predicate, the subscribed node fires; when payload doesn't match, the node doesn't fire. Trailing-`*` prefix matches every type-path with that prefix.
- **Falsifier:** Subscription fires the node on a non-matching payload (predicate ignored), OR doesn't fire on a matching one, OR trailing-`*` doesn't match its prefix.
- **Proof:** executable proof.
- **Existing artifact:** `fresh-proof-needed`.

### Cluster 4 — Executor author + bundled executors

**STORY-executor-protocol** — As a service author writing a custom executor, I can implement the gRPC `Execute` server-streaming RPC plus the executor-observability handshake (capabilities, declared error classes, attribute-schema advertising), and have rimsky discover my executor at startup, validate template attributes against my advertised schema, dispatch nodes to my server, accept my emitted events and terminal outcomes, and route errors I raise according to my advertised error classes, so that a custom executor plugs into a rimsky stack without rimsky-internal knowledge.
- **Acceptance:** A custom executor implementing the public protocol, registered with rimsky's executor catalog, can be referenced from a template; on instance dispatch, the executor receives a real `Execute` stream with resolved attributes, can emit heartbeats and named events that show up on the rimsky event log, and can resolve to success / error / park / async-callback through the real supervisor terminal-resolution path. Errors with advertised classes route per the template's error-policy.
- **Falsifier:** A registered executor advertising a declared error class emits it but the policy router treats it as generic, OR an event the executor emits doesn't appear on the event log, OR attributes resolved against the executor's schema bypass the schema validation.
- **Proof:** example — `examples/executor/` extended with a worked walkthrough that boots a running rimsky and exhibits each protocol surface end-to-end.
- **Existing artifact:** `examples/executor/executor_test.go` covers the `Execute` happy path + capabilities advertising in-process; cross-stack via running rimsky needs fresh authoring.

**STORY-executor-trace-observability** — As an operator running a dashboard against a rimsky deployment, I can fetch the structured trace records for a completed dispatch and stream live trace events while a dispatch is in flight, so that I see what an executor is doing in real time and after the fact.
- **Acceptance:** With an executor advertising trace support and a node in flight, the operator's dashboard subscribes to the executor's trace stream through the observability surface and sees structured trace events as the executor emits them; after the dispatch terminates, the operator queries the trace history and receives the full record.
- **Falsifier:** Trace stream silently drops events under load, OR trace history returns rows that don't correspond to what the executor actually emitted, OR the trace surface is absent for an executor that advertised trace support.
- **Proof:** executable proof.
- **Existing artifact:** `fresh-proof-needed` for the end-to-end via running rimsky.

**STORY-http-node** — As a template author wiring a node against an upstream HTTP API, I can use the bundled `http-node` executor to issue requests, route the response into the node's output attributes, opt into rate-limit parking on 429 (auto-resume from `Retry-After`), and configure which JSON field on an upstream error body carries the error class with a stable fallback when absent, so that I integrate with HTTP upstreams without writing a custom executor.
- **Acceptance:** A template using `http-node` against a real upstream: a 200 response populates the node's output attributes from the response body; a 429 response with `Retry-After` causes the node-run to enter `parked` with the corresponding `resume_at`, and the supervisor wakes the node at that time and re-dispatches it (succeeding when the upstream returns 200 on retry); a 4xx response carrying the configured error-class JSON field surfaces a typed `http/<class>` terminal error; a 4xx with no such field surfaces the stable `_unspecified` leaf.
- **Falsifier:** 429 errors a node-run instead of parking, OR the `resume_at` isn't honored by the supervisor, OR the configured error-class JSON field is ignored.
- **Proof:** executable proof.
- **Existing artifact:** `lib/services/executors/http-node/server_test.go` covers in-process behavior; the supervisor-side resume from `parked` needs verification — likely partially covered by `test/scenarios/parked_lifecycle_test.go`.

**STORY-claude-agent** — As an operator wiring an agentic node, I can use the bundled `claude-agent` executor to dispatch work to the Claude CLI with async-handoff callbacks, configure available MCP servers through a startup catalog (with `{ref:<name>}` references in node config across http / stdio / module / http-loopback transports plus an `allow_inline` policy), resolve `${env:VAR}` references in validator MCP server headers at spawn time without leaking secrets into persisted attributes, gate the run with a cryptographic sign-off over the real bound output, and observe four declared error classes (rate-limited, context-exceeded, refused, tool-use-failed) routed via policy or subscribed via wildcard, so that I run controllable, secure, observable agentic dispatches.
- **Acceptance:** With a deployed template referencing `claude-agent`, an operator drives a real dispatch end-to-end (CLI spawned, agent does real work, async-callback returns); the gate accepts a signature over the run's actual accumulated bound output and rejects an empty-output signature; the MCP catalog resolves `{ref:}` references to declared transports and refuses inline servers when `allow_inline=false`; validator MCP headers carry resolved env-var values on the wire without the plaintext appearing in persisted attributes; each of the four declared error classes routes through error-policy / subscriber matching when the agent surfaces the corresponding condition.
- **Falsifier:** The sign-off accepts a signature over stale output, OR `allow_inline=false` is silently accepted alongside an inline server definition, OR a declared error class fires but the policy router treats it as generic, OR an env-var-referenced credential persists in plaintext attributes.
- **Proof:** executable proof.
- **Existing artifact:** `lib/services/executors/claude-agent/src/signoff-gate.e2e.test.ts` covers the signoff binding; `mcp-servers-wiring.test.ts` covers MCP catalog + ref + transports + inline policy; `rate-limit.test.ts` covers the rate-limited error class; `observability.test.ts` covers the error-class advertising; `agent-run.test.ts` and `lifecycle.e2e.test.ts` cover the base dispatch and async-callback. Collectively qualify for executor-side behavior; whether they spawn a real Claude CLI vs. a stubbed runner needs verification.

**STORY-verifier-http** — As a template author wiring a verifier node against an external check service, I can use the bundled `verifier-http` executor to POST the claim payload to a configured URL and route the node terminal on HTTP status (2xx → success, 4xx/5xx → error with the upstream's class), so that I validate claim outputs against an external service without writing a custom verifier.
- **Acceptance:** A template using `verifier-http` against a real verification service: a payload the service accepts (2xx) reaches a terminal success on the verifier node; a payload it rejects (4xx with a class field) reaches a terminal error with the typed class surfaced; the upstream actually receives the claim payload (echo-back or response-mirror exhibits this).
- **Falsifier:** The verifier resolves to success when the upstream returned 5xx, OR the upstream's class field is dropped, OR the payload posted is canned.
- **Proof:** executable proof.
- **Existing artifact:** `lib/services/executors/verifier-http/executor_test.go` covers in-process behavior; end-to-end via running rimsky needs fresh authoring.

### Cluster 5 — Publisher author + bundled sensors

**STORY-publisher-protocol** — As a service author writing a custom publisher (or sensor), I can implement the gRPC `Publisher` server (`Capabilities`, `Subscribe`, `Unsubscribe`, `ListSubscriptions`), advertise the message kinds I emit with their per-kind config schemas, accept rimsky's `Subscribe` carrying resolved per-instance config, and emit messages into rimsky through `POST /instances/{id}/messages` with the mandatory dedup header, so that a custom publisher plugs into a rimsky stack and rimsky reconciles my subscriptions on restart through `ListSubscriptions`.
- **Acceptance:** A custom publisher implementing the protocol, registered with rimsky's publisher catalog, is referenced from a template's publisher binding; rimsky issues a `Subscribe` with resolved config; the publisher acknowledges and begins emitting messages to the rimsky message endpoint; the messages reach the targeted instance and downstream nodes consume them. After a simulated rimsky restart, rimsky calls `ListSubscriptions` on the publisher and reconciles back to the steady state without re-subscribing what's already there.
- **Falsifier:** Subscribe is acknowledged but messages never reach the message endpoint, OR the post-restart reconcile re-subscribes already-active subscriptions, OR the publisher emits without the dedup header and is silently accepted.
- **Proof:** example — `examples/publisher/` extended with a worked walkthrough that drives a real subscribe / publish / reconcile sequence against a running rimsky.
- **Existing artifact:** `examples/publisher/publisher_test.go` covers in-process; `test/scenarios/sensor/message_routing_test.go` and `lib/services/test/scenarios/sensor_cascade_e2e_test.go` cover routing. The `ListSubscriptions` reconcile leg needs verification.

**STORY-sensor-cron** — As an operator wiring a cron-driven message into a workflow, I can use the bundled `sensor-cron` to fire at declared cron expressions, persist watermarks to a configured durable state DB so a process restart doesn't lose firing position, with the documented replica posture (one replica fires per window once; N independent replicas fire N times — no cross-replica advisory coordination) matching what the binary actually does, so that I have a cron sensor whose behavior under restart and under multi-replica deployment is what the docs claim.
- **Acceptance:** A `sensor-cron` instance, configured with a state DSN pointing at a real durable store, holds a publisher-subscription whose `next_fire_at` is future; restarting the binary preserves the subscription and the binary fires at the originally-scheduled window without external re-subscribe. With an empty DSN the in-memory default takes over. One running sensor instance with a due subscription POSTs exactly one message per window; two independently-running instances sharing the same subscription POST exactly two per window — no advisory-lock coordination silently leaders-elect.
- **Falsifier:** State persists but the binary refuses to honor it on restart, OR two replicas fire only once per window (silent leader election), OR cron advancement uses wall clock instead of the row's prior `next_fire_at`.
- **Proof:** executable proof.
- **Existing artifact:** `lib/services/sensors/sensor-cron/state_db_test.go`, `replica_posture_test.go`, `multi_replica_test.go`, `sensor_test.go` collectively qualify for sensor-internal behavior; `lib/services/test/scenarios/sensor_cascade_e2e_test.go` covers the cross-stack cascade direction.

**STORY-sensor-http** — As an operator wiring a poll-driven message into a workflow, I can use the bundled `sensor-http` to poll a URL at a fixed interval, emit a message when the upstream returns success (optionally filtered by response body), and persist polling state so a restart preserves the schedule, so that I poll an external HTTP source without writing a custom publisher.
- **Acceptance:** A `sensor-http` instance polling a real upstream at a configured interval emits exactly one message per interval-tick when the upstream returns 200; downstream nodes consume the message; with a body-filter declared, only responses matching the filter produce messages. State persists across restart.
- **Falsifier:** Polling skips a window, OR the body filter is declared but unused, OR a process restart drops the polling watermark.
- **Proof:** executable proof.
- **Existing artifact:** `lib/services/sensors/sensor-http/sensor_test.go` and `state_db_test.go` cover sensor-internal behavior; cross-stack leg needs verification.

**STORY-sensor-webhook** — As an operator wiring an inbound-webhook-driven message into a workflow, I can use the bundled `sensor-webhook` to expose configured HTTP routes; inbound POSTs translate to messages routed into rimsky against the subscription's target instance, so that external systems trigger rimsky nodes via webhooks without polling overhead.
- **Acceptance:** A `sensor-webhook` instance subscribed with a configured path-prefix exposes HTTP routes under that prefix; a real inbound POST to a route reaches a message in the targeted rimsky instance with the request body translated into the message payload; the inbound request is acknowledged with success once rimsky has persisted the message.
- **Falsifier:** Inbound POST acknowledged before the message is persisted in rimsky, OR the path-prefix filter is declared but unused, OR the request body translation is canned.
- **Proof:** executable proof.
- **Existing artifact:** `lib/services/sensors/sensor-webhook/sensor_test.go` and `state_db_test.go` cover sensor-internal behavior; cross-stack leg needs verification.

**STORY-sensor-object-store** — As an operator wiring an object-store-driven message into a workflow, I can use the bundled `sensor-object-store` to poll a bucket-and-prefix at a fixed interval, emit a message per newly-discovered object (with the object's metadata surfaced into the message payload), and persist discovery state so restarts don't re-emit previously-discovered objects, so that I react to new objects landing in an external store without writing a custom publisher.
- **Acceptance:** A `sensor-object-store` instance polling a real bucket+prefix discovers a new object dropped after the last poll and emits exactly one message carrying that object's metadata; downstream nodes consume the message; a process restart preserves discovery state and doesn't re-emit objects already discovered. Backend kinds (S3, GCS, etc.) are pluggable.
- **Falsifier:** Restart re-emits already-discovered objects, OR the configured backend is ignored, OR metadata in the emitted message is canned.
- **Proof:** executable proof.
- **Existing artifact:** `lib/services/sensors/sensor-object-store/sensor_test.go` and `state_db_test.go` cover sensor-internal behavior; cross-stack leg needs verification.

### Cluster 6 — Claim-producer author + bundled stores

**STORY-claim-producer-protocol** — As a service author writing a custom claim-producer, I can implement the gRPC `ClaimProducer` server (`Capabilities`, `Open`, `Commit`, `Abandon`, `Release`) with my chosen write-semantics (sync / staged_async / blocking_async / read_only), advertise my capabilities at startup, accept `Open` requests with resolved scope data, return claim handles that drive the executor dispatch, and accept terminal verbs (Commit / Abandon / Release) that close the claim lifecycle correctly, so that my producer plugs into a rimsky stack and rimsky orchestrates claims against it.
- **Acceptance:** A custom claim-producer implementing the public protocol, registered with rimsky's catalog, is referenced from a template; on instance dispatch, the producer receives a real `Open` with resolved scope bytes, returns Acquired or Unavailable per its policy; on success, rimsky drives Commit at auto-terminal; on failure, Abandon; on lifecycle close, Release. The producer's capabilities are honored — a template referencing a write-semantics the producer doesn't advertise is refused at registration.
- **Falsifier:** A registered producer's `Open` is bypassed, OR Commit/Abandon/Release are called but the producer's effect is canned, OR a write-semantics the producer didn't advertise is silently accepted at registration.
- **Proof:** example.
- **Existing artifact:** `examples/claimproducer/claimproducer_test.go` covers protocol surface against the example in-process; `lib/protocols/conformance/claimproducer/runner_terminals_test.go` covers terminal lifecycle. Cross-stack via running rimsky needs verification.

**STORY-claim-producer-scopes-conflict** — As an operator running templates whose claims overlap non-trivially (e.g., prefix-containment), I can use a claim-producer that advertises `SupportsScopesConflict` and define its overlap rule there, with rimsky consulting that rule during claim acquisition (including the fan-out sub-claim path) — two writers whose scopes byte-equally don't overlap but semantically do cannot both hold claims, so that invariant 4b is enforced for the producer's own overlap definition.
- **Acceptance:** A producer advertising `SupportsScopesConflict` whose `ScopesConflict` returns true for prefix-overlapping scopes; two nodes acquiring claims on overlapping scopes — only one acquires, the second is routed to unavailable; a fan-out parent whose `SplitScope` returns overlapping sub-scopes has its conflicting sub-claim rejected.
- **Falsifier:** Both writers acquire, OR the fan-out path skips the consult, OR producers without the capability are still asked.
- **Proof:** executable proof.
- **Existing artifact:** `lib/services/test/scenarios/scopes_conflict/scopes_conflict_test.go`.

**STORY-claim-producer-conformance** — As a claim-producer author shipping a custom producer, I can run `rimsky conformance claim-producer --endpoint <my-producer>` and have the suite drive my producer through the four terminal verbs (Open / Commit / Abandon / Release) including idempotency under retry, plus the serialization-9b probe (detect dishonest internal serialization on `staged_async`), reporting pass / fail per check, so that I prove my producer is correct before shipping it.
- **Acceptance:** The conformance CLI driven against an honest producer reports pass on each terminal verb and on the 9b probe; against a deliberately-broken producer, reports FAIL with non-zero exit and a message citing the specific check.
- **Falsifier:** The 9b probe passes a dishonest producer, OR a duplicate-terminal-call failure is reported as pass, OR the CLI exits zero on failure.
- **Proof:** executable proof.
- **Existing artifact:** `lib/protocols/conformance/claimproducer/runner_terminals_test.go`, `lib/services/test/scenarios/conformance_9b/probe_test.go` and `producers_test.go`, `lib/services/test/scenarios/atomic_staging/conformance_claimproducer_cli_test.go` collectively qualify.

**STORY-claim-producer-observability** — As an operator running a dashboard against a rimsky deployment, I can fetch a claim's full detail, stream live claim-state changes, paginate the producer's claim inventory, and render custom admin views the producer declares, so that I see producer-side state without writing a custom backplane.
- **Acceptance:** With a producer advertising the claim-producer-observability protocol, the operator's dashboard queries claim detail and receives the producer's actual state for that claim; subscribes to the live stream and observes state transitions as they happen; paginates the producer's claim inventory; renders an admin view the producer declared, with data from the producer.
- **Falsifier:** Streamed claim state lags or drops, OR an admin view the producer declared isn't surfaced through the dashboard route, OR the inventory pagination synthesizes rows.
- **Proof:** executable proof.
- **Existing artifact:** `lib/services/stores/filesystem/server/observability_test.go` and `lib/services/stores/postgres/server/observability_test.go` cover per-store observability in-process; operator-side query through a running rimsky dashboard surface needs verification.

**STORY-store-filesystem** — As an operator wiring a workflow whose claims persist on a POSIX filesystem, I can use the bundled `store-filesystem` claim-producer to acquire directory-per-scope claims, opt into atomic staging (stage-then-swap at Commit), trigger on-demand queue refresh through an admin sync route when `sync_strategy: explicit`, and partition fan-out via configurable partition keys, so that I have a filesystem-backed store with the same lifecycle and atomicity guarantees the protocol claims.
- **Acceptance:** A template referencing `store-filesystem`: `Open` returns the local directory path; `Commit` performs an atomic POSIX rename swap of the staging dir into the canonical view; `Abandon` discards the staging dir; with `sync_strategy: explicit` and an empty queue, a POST to the admin sync route picks up a newly-dropped folder and the next `Open` returns it; `SplitScope` partitions on the configured partition key.
- **Falsifier:** Commit's swap is a copy-then-overwrite, OR the explicit-sync route doesn't actually refresh the queue, OR staging dir survives `Abandon`.
- **Proof:** executable proof.
- **Existing artifact:** `lib/services/stores/filesystem/store/store_test.go`, `ledger_test.go`, `pick_policy_test.go`, `drained_test.go`, `admin_sync_test.go` cover store-internal behavior; `examples/atomic-staging-fs-producer/atomic_staging_test.go` covers the reference producer; `lib/services/test/scenarios/atomic_staging/fs_held_swap_e2e_test.go` covers the held-swap cross-stack leg.

**STORY-store-postgres** — As an operator wiring a workflow whose claims persist in PostgreSQL, I can use the bundled `store-postgres` claim-producer to acquire row-locking claims via configurable pick policies, opt into atomic staging (staging schema swap at Commit), declare verifier checks including `row_count_ratio` over aggregate-only queries, and subscribe to declared error classes (`pg/claim_unavailable`, `pg/swap_failed`), so that I have a postgres-backed store that delivers staged-async semantically rather than as a no-op.
- **Acceptance:** A template referencing `store-postgres`: `Open` with staged write-semantics creates/reserves a staging schema queryable through the store's observability; the executor writes rows to staging; `Commit` performs an atomic schema swap; a swap collision emits `pg/swap_failed` routable through `error_types`; a verifier check declaring `row_count_ratio` with bounds compiles and executes as an aggregate-only query, surfacing `pg/verifier_check_failed/row_count_ratio` on out-of-bounds; an empty pick policy queue emits `pg/claim_unavailable`.
- **Falsifier:** Atomic-staging schema is created but Commit doesn't atomically swap, OR `row_count_ratio` runs a non-aggregate query, OR `pg/swap_failed` is emitted as a generic error class, OR `pg/claim_unavailable` doesn't fire on a real empty-queue Open.
- **Proof:** executable proof.
- **Existing artifact:** `lib/services/stores/postgres/store/store_test.go`, `atomic_staging_test.go`, `ledger_test.go` cover store-internal behavior; `lib/services/test/scenarios/atomic_staging/pg_verifier_test.go` and `pg_verifier_commit_abandon_test.go` cover the verifier check legs; `lib/services/test/scenarios/pg_error_classes/pg_error_classes_test.go` covers the error-class emit legs.

### Cluster 7 — Validation + data-processing + lifecycle-subscriber + openlineage

**STORY-validation-author** — As a service author writing a validation mix-in, I can implement the gRPC `Validation` server (single `Validate` RPC) and advertise it in my primary protocol's capabilities handshake, with rimsky calling my validator at registration time (for the relevant role context — executor / claim-producer / publisher / lifecycle-subscriber) and surfacing my findings to the operator as errors (blocking) or warnings (informational), so that I customize validation beyond rimsky's built-in shape checks.
- **Acceptance:** A service implementing `Validation` (alongside its primary protocol), registered with rimsky's catalog, has its validator called on template registration with the role context appropriate to where it's referenced; findings the validator returns as errors cause the registration to be refused with the finding surfaced to the operator; findings returned as warnings are surfaced without blocking.
- **Falsifier:** Error-severity finding doesn't block registration, OR warning-severity finding blocks registration, OR validator is registered but `Validate` is never called.
- **Proof:** example — `examples/validation/` extended with a worked walkthrough.
- **Existing artifact:** `examples/validation/validation_test.go` covers the example in-process; whether rimsky actually calls it on a real registration end-to-end needs verification.

**STORY-data-processing-author** — As a claim-producer author writing the typed-data mix-in, I can implement the gRPC `DataProcessing` server (`Capabilities`, `BeginCandidate`, `CommitCandidate`, `AbandonCandidate`, `ListVersions`, `ListPartitions`, `GetVersionSchema`) advertised alongside my claim-producer protocol, with rimsky allocating staging candidates per fan-out partition, finalizing on success, garbage-collecting on failure, and surfacing version history through my listing surfaces, so that I support typed-data version lifecycle with partition-aware staging.
- **Acceptance:** A claim-producer advertising the `DataProcessing` mix-in is referenced from a template's fan-out node; rimsky calls `BeginCandidate` per sub-partition; the executor writes typed data via the returned candidate handle; on leaf success, rimsky calls `CommitCandidate` and the candidate's metadata surfaces in the parent writeback; on leaf failure, rimsky calls `AbandonCandidate` and the candidate is GC'd. `ListVersions` exposes finalized versions; `ListPartitions` exposes partitions per version; `GetVersionSchema` returns schema bytes.
- **Falsifier:** `BeginCandidate` is never called on a fan-out partition, OR `CommitCandidate` is called but the producer's effect is canned, OR `AbandonCandidate` is skipped on leaf failure, OR a declared version doesn't appear in `ListVersions`.
- **Proof:** example.
- **Existing artifact:** `examples/data-processing/dataprocessing_test.go` covers the example in-process; `test/scenarios/leaf_candidate_handle_e2e_test.go` covers the leaf-candidate-handle cross-stack end-to-end.

**STORY-lifecycle-subscriber-author** — As a service author writing a lifecycle subscriber, I can implement the gRPC `LifecycleSubscriber` server (seven callbacks: template registered / deployed / undeployed / deregistered, instance created / terminated, run-scope terminal) and register it as an active subscriber, with rimsky firing each callback synchronously at the corresponding lifecycle transition carrying the relevant context (template hash, instance ID, run-scope ID, service bindings, owner key, terminal reason), so that I react to rimsky lifecycle events from an external service.
- **Acceptance:** A subscriber implementing all seven callbacks, registered with rimsky's catalog, receives each callback at the corresponding lifecycle transition: template registered fires when a template is registered, template deployed fires on `deploy`, instance created fires on `POST /instances`, instance terminated fires on terminate, run-scope terminal fires when a run-scope closes (main, sub-graph, fan-out partition). Each callback carries the documented context fields; the subscriber's response is honored synchronously at the close site.
- **Falsifier:** A callback fires for the wrong transition, OR a documented context field is missing from the callback payload, OR the subscriber's failure response on a callback is ignored by rimsky (fire-and-forget).
- **Proof:** example.
- **Existing artifact:** `examples/lifecyclesubscriber/subscriber_test.go` covers the example in-process; `test/scenarios/host_agent_latebind_all_protocols_test.go` exhibits three of the seven callbacks at cross-stack level.

**STORY-subscriber-openlineage** — As an operator running rimsky in a data-platform environment, I can use the bundled `openlineage` subscriber to translate rimsky lifecycle events and claim terminal records into OpenLineage 1.x JSON events posted to a backend (Marquez / DataHub / Collibra / etc.), so that rimsky's run DAG and data lineage surface in my governance platform without writing a custom subscriber.
- **Acceptance:** A running `openlineage` subscriber configured to post to a real OpenLineage receiver: when a rimsky template is deployed, the subscriber emits a dataset-version event; when a run-scope reaches terminal, the subscriber emits a job-run event; claim terminal records translate into lineage events; the receiver actually receives well-formed OpenLineage 1.x JSON.
- **Falsifier:** Subscriber posts to receiver but with malformed OpenLineage JSON, OR a lifecycle event the subscriber should emit on is skipped, OR the emitted event's IDs don't correspond to the rimsky-side IDs.
- **Proof:** executable proof.
- **Existing artifact:** `lib/services/subscribers/openlineage/subscriber_test.go` and `emitter_test.go` cover subscriber + emitter behavior in-process; cross-stack via running rimsky → openlineage subscriber → real receiver needs verification.

### Cluster 8 — Host-agent

**STORY-host-agent-late-bind-all-protocols** — As a template author wiring a workflow against locally-running binaries (executor, claim-producer, publisher, validation, data-processing), I can run `rimsky agent` on my dev machine connected to a remote rimsky stack, declare bindings for each protocol, and have rimsky dispatch through the proxy to spawned local children identically across every supported protocol — no protocol left as a `Unimplemented` stub, so that I exercise the assembled product against local code without rebuilding images.
- **Acceptance:** With `rimsky agent` connected to a deployed `rimsky-host-agent-proxy` and bindings declared for each protocol, instance dispatches reach spawned local binaries: a validation binding's rejecting validator causes registration rejection at the validation surface; a publisher binding publishes real messages into the instance; a data-processing binding performs a real typed-data operation; executor and claim-producer bindings already-worked continue to work. Every dispatch is served by a real spawned binary; none returns gRPC `Unimplemented`.
- **Falsifier:** Any of the five protocols returns `Unimplemented` through the proxy, OR a dispatch's effect is canned at the proxy layer rather than reaching the spawned binary.
- **Proof:** executable proof.
- **Existing artifact:** `test/scenarios/host_agent_latebind_all_protocols_test.go`; `lib/runtime/hostagent/dispatch_unary_test.go` regression-protects the dispatch-router at unit level.

**STORY-host-agent-per-run-scope-isolation** — As a template author running a fan-out workflow whose run-scopes concurrently dispatch the same late-bound executor, I can trust that each run-scope spawns its own isolated child process — they never share executor state — and the child is reaped when its run-scope terminates, so that concurrent run-scopes don't corrupt each other's state.
- **Acceptance:** With a stateful late-bound executor whose binary records which run-scope dispatched to it, an instance whose fan-out produces two concurrent run-scopes both dispatching the same binding: the agent spawns two distinct child processes (one per run-scope, not one shared); each child sees only its own run-scope's dispatches; terminating one run-scope reaps only that run-scope's child while the other keeps serving.
- **Falsifier:** The two run-scopes share a single child, OR terminating one run-scope reaps both children, OR a terminated run-scope's child survives.
- **Proof:** executable proof.
- **Existing artifact:** `test/scenarios/host_agent_per_run_scope_isolation_test.go`; `test/scenarios/host_agent_reap_test.go` covers the reap leg.

**STORY-host-agent-per-binding-overrides** — As a template author declaring late-bind bindings for varied local binaries, I can specify per-binding env vars, command-line args, working directory, and ready/spawn timeout — the agent applies them when exec'ing the child — so that I run different binaries with different configuration through the same agent without global config soup.
- **Acceptance:** With a late-bind binding declared with non-default args (e.g., a mode flag), an env var, a per-binding cwd, and a per-binding timeout, an instance dispatching against the binding produces a spawned child that actually runs with those args / env / cwd in effect (the binary echoes argv / env / cwd back through the real dispatch response); the per-binding timeout (shorter than global) actually bounds the spawn wait. A binding with no overrides spawns with inherited env, global cwd, and global timeout (backward-compatible).
- **Falsifier:** An override is declared but ignored, OR the per-binding timeout has no effect.
- **Proof:** executable proof.
- **Existing artifact:** `test/scenarios/host_agent_per_binding_exec_overrides_test.go`.

**STORY-host-agent-anonymous-mode** — As an operator running a fresh anonymous-mode rimsky deployment (no api-keys minted yet) and a `rimsky agent` connected to it, I can register and dispatch to late-bound services from an instance created in anonymous mode, so that the dev-loop with host-agent late-binding doesn't require me to mint credentials first.
- **Acceptance:** With rimsky stack in anonymous mode, a `rimsky-host-agent-proxy` deployed, and `rimsky agent` connected: an anonymous-mode instance referencing a late-bound binding dispatches through the proxy and reaches the connected agent; the late-bound child runs and returns real dispatch outcome rather than the dispatch terminating with `host_agent_not_connected` because the instance owner is NULL.
- **Falsifier:** Dispatch terminates with `host_agent_not_connected` despite the agent being connected, OR the dispatch reaches a different agent (anonymous-mode routes mis-direct).
- **Proof:** executable proof.
- **Existing artifact:** `test/scenarios/host_agent_anonymous_mode_latebind_test.go`.

**STORY-host-agent-control-plane** — As an operator running rimsky-dispatched workflows on a dev machine, I can start the host-agent locally with `rimsky agent start`, check its connection status with `rimsky agent status`, and stop it cleanly with `rimsky agent stop` (children reaped), so that I manage the agent's lifecycle from the same CLI that drives the rimsky stack.
- **Acceptance:** Through the `rimsky agent` CLI: `start` launches the agent connected to the configured proxy (or refuses with a clear diagnostic if proxy/auth aren't reachable); `status` reports the connection state, the configured proxy endpoint, and the list of currently-spawned children (per run-scope, per binding); `stop` SIGTERMs the agent, the agent reaps all spawned children with the documented grace period, and the agent exits cleanly.
- **Falsifier:** `stop` exits cleanly but leaves zombie children, OR `status` reports `connected` when the bidi stream is actually down, OR `start` silently succeeds with a misconfigured proxy URL.
- **Proof:** demo.
- **Existing artifact:** `test/scenarios/host_agent_reap_test.go` covers the reap-on-stop leg; full start/status/stop CLI flow needs fresh demo authoring.

### Cluster 9 — Operator deployment surfaces

**STORY-rimsky-deployment-bootstrap** — As an operator deploying rimsky to a stack, I can run the bundled `rimsky-entrypoint` with no command to launch all three roles together for dev (or as a single role for multi-process production), and trust that database migrations run exactly once per deployment regardless of role split — never racing across roles, never silently skipped — with an explicit env-var override for one-shot init containers, so that the deployment topology is whatever I choose and the schema arrives at the right state deterministically.
- **Acceptance:** Running `rimsky-entrypoint` with no command starts all three role binaries and runs migrations once before any of them start; running it with a single role command (`rimsky-scheduler` / `rimsky-supervisor` / `rimsky-control-api`) starts only that role and runs migrations only when the role is `rimsky-control-api` (so a three-container split migrates exactly once, not three racing runs); running it with an unknown command or multiple args exits non-zero with a clear error. `RIMSKY_ENTRYPOINT_MIGRATE=1` forces migrate; `=0` skips it.
- **Falsifier:** Migrations race when the three-container split fires three simultaneous `rimsky-entrypoint` processes, OR a three-container split never migrates, OR an unknown command silently spawns the all-in-one path.
- **Proof:** executable proof.
- **Existing artifact:** `cmd/rimsky-entrypoint/main_test.go`.

**STORY-rimsky-health-check** — As an operator running rimsky behind a load balancer or k8s liveness/readiness probe, I can query `GET /health` (or `rimsky health` CLI) and get back the control-api's deployment health status, so that infrastructure operators have a probe surface to gate traffic on.
- **Acceptance:** Against a running control-api, a request to the health surface returns a successful response while the deployment is healthy and a non-success response when a critical dependency (persistence reachable, etc.) is down. The route requires no authentication (probes don't carry bearer tokens) and is fast (probe-suitable).
- **Falsifier:** Health route returns success while a critical dependency is down (false-positive), OR requires auth (incompatible with anonymous probes).
- **Proof:** executable proof.
- **Existing artifact:** `fresh-proof-needed`.

## Technical decisions

### Workspace + module layout

- **TD-module-split** — Five-module Go workspace. **Choice:** root + `lib/foundation` + `lib/protocols` + `lib/services` + `examples` tied by `go.work` with local-path `replace`s. **Rationale:** services-side ship as standalone containers with no rimsky-internal access; protocols are the implementer-facing contract with zero internal deps; the examples module gives each protocol a copy-and-modify reference implementation independent of the orchestrator.
- **TD-layer-ordering** — Within the root module, four ordered layers. **Choice:** `foundation` → `graph` → `runtime` → `control`, enforced by depguard. **Rationale:** directed dependency DAG; lower layers never see higher ones.
- **TD-toplevel-dirs** — Idiomatic top-level code directories. **Choice:** `cmd/`, `lib/`, `test/`, `tools/`. **Rationale:** conventional Go layout; clear binary vs. lib vs. test vs. dev-tooling split.

### Lint / depguard

- **TD-depguard-pgx-isolation** — Confine `pgx` imports. **Choice:** only `foundation/persistence/postgres/`, services, cmd, test/support, scenario harness. **Rationale:** keep postgres specifics out of graph/runtime/control.
- **TD-depguard-foundation-internal** — `foundation/internal/` is private. **Choice:** external imports forbidden. **Rationale:** hide implementation details behind public foundation packages.
- **TD-depguard-protocols-purity** — `lib/protocols/` import surface. **Choice:** stdlib + grpc + protobuf + uuid + yaml.v3 only. **Rationale:** protocols are the public implementer contract — minimal, independent.
- **TD-depguard-foundation-purity** — `lib/foundation/` import surface. **Choice:** stdlib + protocols + chosen libs only; no graph/runtime/control. **Rationale:** foundation provides primitives, not workflow shape.
- **TD-depguard-graph-purity-with-scheduler-exception** — `lib/graph/` import surface. **Choice:** pure except a documented scheduler exception allowing `runtime` imports for sweep entry points. **Rationale:** scheduler tick drives runtime sweeps; refactor pending.
- **TD-depguard-runtime-purity** — `lib/runtime/` import surface. **Choice:** foundation + graph + protocols; not `control`. **Rationale:** control is the operator surface, not a runtime dep.
- **TD-depguard-consumption-isolation** — `lib/services/` import surface. **Choice:** `lib/protocols` only; not foundation/graph/runtime/control. **Rationale:** bundled services ship as standalone images — defense in depth against rimsky-internal leakage.
- **TD-revive-no-exported-rule** — Revive lint config. **Choice:** disable the `exported` rule. **Rationale:** every exported symbol carrying a comment is noise; focus on load-bearing ones.

### Library choices

- **TD-logging-slog-only** — Logging library. **Choice:** stdlib `log/slog`. **Rationale:** minimize dependencies; production-ready stdlib. **Alternatives rejected:** Zap, Zerolog.
- **TD-http-router-chi** — HTTP routing. **Choice:** `go-chi/chi/v5`. **Rationale:** lightweight, net/http-native. **Alternatives rejected:** Gin, Echo.
- **TD-postgres-pgx-v5** — Postgres driver. **Choice:** `jackc/pgx/v5`. **Rationale:** native driver, protocol-aware.
- **TD-sqlite-modernc-pure-go** — SQLite driver. **Choice:** `modernc.org/sqlite`. **Rationale:** pure-Go, no CGO; simplified deployment.
- **TD-cron-robfig-v3** — Cron expression parser. **Choice:** `robfig/cron/v3`. **Rationale:** reliable standards-aligned parser.
- **TD-uuid-google** — UUID generation. **Choice:** `google/uuid`. **Rationale:** lightweight.
- **TD-yaml-gopkg-v3** — YAML parser. **Choice:** `gopkg.in/yaml.v3`. **Rationale:** standard, widely used.
- **TD-jcs-cyberphone** — JSON canonicalization (RFC 8785) for spec hashing. **Choice:** `cyberphone/json-canonicalization`. **Rationale:** only Go impl compliant with the spec.
- **TD-grpc-google-official** — gRPC + protobuf libraries. **Choice:** `google.golang.org/grpc`, `google.golang.org/protobuf`. **Rationale:** official, required for the protocols.
- **TD-testcontainers-go** — Integration-test container management. **Choice:** `testcontainers-go`. **Rationale:** real database containers in tests, not mocks.
- **TD-metrics-prometheus-client** — Metrics export. **Choice:** `prometheus/client_golang`. **Rationale:** standard observability format.

### Conventions

- **TD-cold-read-style** — Code organization discipline. **Choice:** file-by-feature; ~500-line file / ~100-line function guidelines; max 3 nesting levels via early returns; no base classes / DI containers / "Manager" abstractions. **Rationale:** optimize for cold-read.
- **TD-blessed-invariant-annotations** — Document load-bearing safety properties. **Choice:** `@blessed-invariant` blocks naming the property + its enforcement site; scenario tests exercise them. **Rationale:** explicit safety contract.
- **TD-design-link-annotations** — Code → design citation. **Choice:** `@concept:` / `@story:` / `@decision:` annotations at points of enforcement. **Rationale:** traceability from code to design model.
- **TD-tracked-duplication** — Duplication discipline. **Choice:** prefer tracked duplication (`@source: path:func`, `@diverged: true` + `@reason:`) over hidden coupling; extract to shared only at 3+ identical stable sites under 50 lines. **Rationale:** visible duplication is editable; abstractions hide intent.

### Build / image

- **TD-build-tool-makefile** — Build orchestration. **Choice:** Makefile as single source of truth. **Rationale:** shell-native, no extra tooling.
- **TD-build-go-version** — Go version pin. **Choice:** Go 1.25.0 minimum. **Rationale:** recent stable.
- **TD-build-cgo-disabled** — CGO posture. **Choice:** `CGO_ENABLED=0` for all builds. **Rationale:** pure-Go binaries, no C runtime dep.
- **TD-image-two-stage** — Docker image structure. **Choice:** `golang:1.25-alpine` for build stage; `gcr.io/distroless/static-debian12:nonroot` for runtime. **Rationale:** minimal runtime attack surface; nonroot by default.
- **TD-image-set-four-core** — Distributed core image set. **Choice:** `rimsky` (all binaries), `rimsky-all-in-one` (SQLite-defaulted dev), `rimsky-host-agent-proxy`, `rimsky-conformance`. **Rationale:** flexible deployment topology + dev-friendly all-in-one.
- **TD-image-set-bundled-services** — Bundled-service image set. **Choice:** one image per bundled service (eleven — sensors, stores, subscribers, executors). **Rationale:** pre-packaged reference impls.
- **TD-image-entrypoint-role-selection** — Single-binary multi-role entrypoint. **Choice:** `rimsky-entrypoint` with no command → all roles; single role command → that role; migrate runs once per deployment, owner role determined by command. **Rationale:** one image, many topologies.
- **TD-image-tagging-version-and-channel** — Image tag scheme. **Choice:** `:v0.x.y` immutable + `:latest` (formal) or `:dev` (dev releases) channel. **Rationale:** immutable version + mutable channel.
- **TD-registry-hub-rimskyai-namespace** — Docker Hub namespace. **Choice:** `rimskyai` (no hyphen). **Rationale:** Hub disallows hyphens; GitHub org `rimsky-ai` is different.

### Persistence

- **TD-persistence-dual-backend** — Backend support. **Choice:** both Postgres and SQLite, selected by `persistence.driver` config. **Rationale:** SQLite for dev/test, Postgres for prod.
- **TD-migrations-append-only-numbered** — Migration discipline. **Choice:** numbered (`001-`, `002-`…), append-only, per backend. **Rationale:** migration-runner shape; ordering is the runner's contract.
- **TD-migrations-no-compat-shims** — Pre-v1 migration freedom. **Choice:** drop + recreate when cleaner; no compat shims. **Rationale:** pre-v1 (see `TD-pre-v1-break-freely`).
- **TD-advisory-locks** — Named cross-process coordination. **Choice:** postgres advisory locks + sqlite equivalent at session level. **Rationale:** migration ownership, scheduler-tick ownership, per-scope serialization.
- **TD-blob-backends-pluggable** — Blob storage abstraction. **Choice:** pluggable backend interface (`inline`, `pg-largeobject`, `filesystem`, `memory`). **Rationale:** deployment-specific spill targets.
- **TD-blob-spill-threshold-config** — Spill threshold control. **Choice:** configurable byte threshold per deployment; default inline-only. **Rationale:** tunable payload size.
- **TD-message-idempotencies-dedup-tuple** — Message dedup discriminator. **Choice:** `(instance_id, sender_kind, sender, sender_subject, idempotency_key)`. **Rationale:** prevent cross-tenant + cross-kind replay collisions.
- **TD-wait-set-topic-kind-taxonomy** — Wait-set topic discriminator. **Choice:** 5-value taxonomy (terminal, transient, attribute, event, message) + legacy `state` fallback. **Rationale:** enable targeted wait-set queries by signal class.

### Protocol design

- **TD-protocol-version-v1-namespaced** — Protocol versioning. **Choice:** `rimsky.v1` proto package; **all control-API HTTP routes mounted under `/v1/`**, including the executor async-callback URL (`/v1/callback/{async_ack_id}`) and the observability sub-router (`/v1/observability/`). The bare-path routes that exist today (`/templates`, `/instances`, `/tags`, `/nodes`, `/messages`, `/events`, `/audit`, `/auth/*`, `/lineage/*`, `/admin/*`, `/diagnostics/*`, `/backfills/*`, `/lock-holders/*`, `/health`, `/mcp`) get swept to `/v1/...`. **Rationale:** consistent versioned contract surface across the whole control-API; aligns the URL layer with the already-versioned proto package; existing `/v1/` carve-outs become the leading edge of one rule rather than exceptions. Pre-v1 freedom (per `decision:pre-v1-break-freely`) means no transition window; old bare paths are removed when the sweep lands. **Resolves:** `tension:control-api-version-prefix`. **Alternatives rejected:** committing to bare paths indefinitely (leaves the two existing `/v1/` carve-outs as permanent exceptions); adding version-discovery + client-side negotiation (introduces a new mechanism not justified by current scope).
- **TD-async-callback-post-json** — Async-callback transport. **Choice:** HTTP POST with JSON `AsyncCallbackBody` to `${callback_url}/v1/callback/{async_ack_id}`. **Rationale:** simple, debuggable.
- **TD-async-callback-outcome-oneof** — Async-callback body shape. **Choice:** oneof `success` | `error` | `park` + optional `events` array; exactly one outcome key. **Rationale:** type-safe state machine; explicit error handling.
- **TD-idempotency-key-header-universal** — Idempotency on message emit. **Choice:** mandatory `Idempotency-Key` HTTP header on `POST /instances/{id}/messages`. **Rationale:** replay-safe by construction.
- **TD-idempotency-status-code-distinction** — Operator-visible replay marker. **Choice:** `201` on fresh insert vs `200` on replay (returning original `message_id`). **Rationale:** distinguish fresh vs. replayed without body inspection.
- **TD-message-sender-kind-discriminator** — Message envelope sender. **Choice:** the `rimsky_messages.sender_kind` column is the three-value enum `operator` / `publisher` / `instance`. **Rationale:** namespace sender strings by source on the persisted envelope. Note: the orthogonal `sender_subject` column carries the actor identity (api-key id, publisher subscription, or the `anonymous` sentinel for anonymous-mode operator emits). The idempotency-dedup tuple in `rimsky_message_idempotencies` has its own three-value `sender_kind` column that differs from the envelope's by one value — `operator` / `publisher` / `anonymous` (no `instance`, since instance-sender messages are blocked at the wire by the operator-or-publisher gate), where `anonymous` buckets anonymous-mode operator emits separately so the bootstrap admin's later emits don't dedup against the anonymous-floor emits that preceded the key mint. The two `sender_kind` columns are not the same enum and should not be conflated.
- **TD-grpc-internal-protocols** — Inter-service transport. **Choice:** gRPC for all service-to-service protocols. **Rationale:** type-safe binary, codegen, streaming.
- **TD-protojson-gateway** — HTTP+JSON bridge for gRPC. **Choice:** `protojson` marshaling for the HTTP gateway. **Rationale:** REST convenience without abandoning the gRPC contract.
- **TD-spec-jcs-canonicalization** — Template-spec hashing. **Choice:** JSON Canonicalization Scheme (RFC 8785) for canonical bytes. **Rationale:** deterministic, reproducible hash.
- **TD-event-log-payload-shapes** — Event log payload shape. **Choice:** typed oneof payloads for signal-class events (the node-run-transition subset uses signal-type-path discipline); free-form JSON payload for operational events (`auth.*`, `state_transition`, etc.) whose payload is audit data rather than typed contract. **Rationale:** type safety where the signal taxonomy is settled; lightweight JSON where the payload is just consumer-facing audit data. Kind-discriminator typing is a separate decision (see `decision:event-log-kind-enum`).
- **TD-event-log-kind-enum** — Event-log kind discriminator typing. **Choice:** the canonical set of operational `rimsky_events.kind` values (non-signal-class events: `auth.*`, `state_transition`, `lock_acquired`, `work_started`, `attributes_substituted`, `breakpoint.hit`, etc.) is declared as an enum in `proto/v1/events.proto`. Signal-class kinds keep their existing type-path discipline (the five-class taxonomy validated at template registration). Rimsky's app logic — scheduler, supervisor, breakpoint evaluator, audit handler, read-API kind filters — consumes typed values exclusively (the generated Go enum for operational kinds; the parsed signal type-path for signal-class kinds), never raw strings. The persistence layer marshals typed → storage at write and storage → typed at read; an unknown string at the unmarshal boundary is a defensive error, not a control-flow input. Column storage shape (`TEXT` today) is a marshaling detail with no `CHECK` constraint required — the enum at the app boundary IS the gate. **Rationale:** kinds are not inert per `concept:inertness` — they drive cascade dispatch (signal-class), breakpoint evaluation (`breakpoint.hit`), and audit-consumer filtering by canonical name (operational). A typed boundary at the app layer prevents typo-induced silent observability blind spots without coupling persistence to schema migrations. Adding a new operational kind = adding an enum value in `proto/v1/events.proto` + regenerating Go bindings. **Resolves:** `tension:events-kind-no-enum`. **Alternatives rejected:** CHECK constraint at persistence (couples schema to enum, requires migration per new kind, redundant when enum gates the app boundary); registry-table-with-FK (introduces a mutable kind catalog through an API, which the model doesn't want); leaving operational kinds free-form (accepts the footgun for audit consumers filtering by canonical kind name).
- **TD-conformance-suite-per-protocol** — Conformance discipline. **Choice:** one conformance suite per protocol under `lib/protocols/conformance/`, exposed as `rimsky conformance <protocol>`. **Rationale:** external impls must pass conformance.

### Auth / permission

- **TD-auth-api-key-bearer** — Authentication model. **Choice:** api-key as bearer token. **Rationale:** simple, stateless, service-account-friendly.
- **TD-auth-dry-run-request-flag** — Per-request dry-run. **Choice:** `?dry_run=true` query flag on writes. **Rationale:** preview without persisting.
- **TD-auth-dry-run-mode-floor-on-key** — Identity-bound dry-run. **Choice:** grant's `mode: dry_run` pins a key to dry-run regardless of request flag. **Rationale:** attempt-only credentials, identity-bound.
- **TD-auth-grant-scope** — Per-grant scope dimensions. **Choice:** `scope` map of action-specific dimension keys (e.g., `template_tag`) constraining the action. **Rationale:** least-privilege delegation across resource lifecycle.

### Release

- **TD-release-formal-skill** — Formal release flow. **Choice:** `/release` skill drives SemVer judgment + notes draft + outward push behind a single user gate. **Rationale:** human review at the right point; automation everywhere else.
- **TD-release-semver-from-diff** — SemVer bump source. **Choice:** derived from diff inspection (proto, migrations, exports, CLI flags, env vars). **Rationale:** objective and consistent.
- **TD-release-notes-template** — Notes shape. **Choice:** template-driven sections (Breaking / What's new / Fixes / Internal / Image set / Go module / npm); every entry traces to a diff hunk. **Rationale:** comprehensive without omission.
- **TD-release-dev-mechanical** — Dev-release flow. **Choice:** `make dev-release` is mechanical; no SemVer judgment, no notes, version `v<next-minor>.0-dev.<YYYYMMDD>.g<sha>`. **Rationale:** continuous internal channel without ceremony.
- **TD-release-semver-sha-dot-joined** — Pre-release SHA encoding. **Choice:** dot-joined into the SemVer pre-release segment, not `+` build metadata. **Rationale:** `+` is invalid in Docker tags + npm + go-get.
- **TD-release-chain** — Shared release chain. **Choice:** `lint → license-lint → core-images → service-images → test-all → scan → push-images`. **Rationale:** comprehensive pre-push verification; images get built before the test suite runs so the scenario tests can drive the locally-built image set.
- **TD-release-scan-docker-scout** — CVE scanning gate. **Choice:** `docker scout cves --only-severity critical,high --exit-code` against all local images. **Rationale:** block release on unaddressed critical/high CVEs.
- **TD-release-attestations** — Supply-chain attestations on push. **Choice:** `docker buildx build --push --provenance=mode=max --sbom=true`. **Rationale:** SBOM + provenance on Hub.
- **TD-release-distribution** — Distribution channels. **Choice:** Hub images + npm (`@rimsky-ai/protocols`) + Go modules + GitHub Releases. **Rationale:** multiple consumption patterns.

### Pre-v1 policy

- **TD-pre-v1-break-freely** — Pre-v1 stance. **Choice:** no backwards-compat guarantee on wire / config / event-log / resource interface; delete dead code rather than carrying it forward. **Rationale:** no production data yet; cleaner refactors. This rule is replaced by deployed-stage rules when v1 ships.

### Licensing / project policy

- **TD-licensing-dual-apache-agpl** — License split. **Choice:** the protocols module + `lib/services/executors/claude-agent/` (the TS reference executor) + the examples module + `cold-read/` (the prose style guide) are Apache-2.0; everything else (foundation, graph, runtime, control, the other bundled services, cmd, test, tools) is AGPL-3.0-or-later with a commercial alternative. **Rationale:** Apache surface for everything an external implementer is meant to copy, modify, or link against; AGPL for the orchestrator itself.
- **TD-licensing-enforced-by-license-lint** — License-import discipline. **Choice:** enforced by `tools/license-check` (Apache packages import only stdlib + permissive + Apache). **Rationale:** prevent AGPL contamination of permissive consumers.
- **TD-implementation-language-go-plus-ts** — Implementation languages. **Choice:** Go for all core code; TypeScript only in `lib/services/executors/claude-agent/`. **Rationale:** single core ecosystem; TS where the upstream SDK lives.
- **TD-config-format-yaml** — Configuration shape. **Choice:** plain YAML for `rimsky.yml` and per-service configs. **Rationale:** human-readable, portable.
- **TD-testing-scenario-based-e2e** — Testing discipline. **Choice:** end-to-end via `test/scenarios/` + `lib/services/test/scenarios/` driving the assembled product; persistence tests use `testcontainers-go`. **Rationale:** real-stack integration tests against blessed invariants.
- **TD-project-agnostic** — Consumer neutrality. **Choice:** no code/doc/test/example/comment names or assumes a specific consumer; templates use generic names. **Rationale:** rimsky ships as embedded platform to many consumers.

## Design changes

This spec bootstraps two new artifact catalogs under `.ok-planner/design/` — `stories/` and `decisions/` — and lands one durable artifact per story and per technical decision above. Each durable body uses the same shape the spec gives but excludes the `Existing artifact:` metadata, which is spec scaffolding for `write-plan`, not part of the durable contract.

The catalogs also need TOCs analogous to the existing `concepts.md`: an auto-generated `stories.md` and `decisions.md` enumerating each artifact with a one-line summary.

**Self-containment treatment (path-bearing bodies).** The self-containment rule forbids file or directory paths in durable artifact bodies. The spec body itself cites paths freely (the spec isn't durable), and many story / decision bodies in this spec carry path citations as load-bearing context (e.g., depguard rules ARE about directory boundaries). When `execute-plan` copies each body into the durable file, paths get rewritten by these rules in order:

1. **Top-level module / group prefix** → structural noun: `lib/foundation` → "the foundation module"; `lib/protocols` → "the protocols module"; `lib/services` → "the services module"; `lib/runtime` → "the runtime layer"; `lib/graph` → "the graph layer"; `lib/control` → "the control layer"; `cmd/` → "the cmd group"; `test/` → "the test group"; `tools/` → "the tools group"; `examples/` → "the examples module".
2. **Bare-prefix forms (no `lib/` prefix)** → treated as the same noun: `foundation/...` → "the foundation module's ..."; same for `protocols/`, `services/`, `runtime/`, `graph/`, `control/`.
3. **Sub-paths under a module / group** → "the <module-noun>'s <sub-directory> package/directory". Examples: `lib/protocols/conformance/` → "the protocols module's conformance package"; `lib/services/executors/claude-agent/` → "the services module's claude-agent executor"; `examples/executor/` → "the examples module's executor reference"; `lib/services/test/scenarios/` → "the services module's scenarios test directory"; `foundation/persistence/postgres/` → "the foundation module's postgres persistence driver".
4. **Files outside any of the above** — rephrase as the structural noun: `licensing.yml` → "the workspace license map"; `Makefile` → "the build orchestration"; `RELEASING.md` → "the release runbook"; `.golangci.yml` → "the lint configuration"; `.claude/rules/rules.md` → "the after-code-changes verification rules"; `.claude/rules/cold-read-cheatsheet.md` and `cold-read/` (style guide + manifesto) → "the cold-read style guide"; `go.work` → "the workspace definition".
5. **Pass-through (NOT paths in the self-containment sense)** — survive verbatim: third-party Go import identifiers (`jackc/pgx/v5`, `gopkg.in/yaml.v3`, `google/uuid`, `cyberphone/json-canonicalization`, `go-chi/chi/v5`, `modernc.org/sqlite`, `robfig/cron/v3`, `prometheus/client_golang`, `google.golang.org/grpc`, `testcontainers-go`, etc.), user-facing config filenames the operator types (`rimsky.yml`, `rimsky-compose.yml`), and docker image identifiers (`golang:1.25-alpine`, `gcr.io/distroless/static-debian12:nonroot`). These name external artifacts or operator-facing config, not in-repo code paths, and don't rot when the codebase moves.

The other allowed-citation forms (concept / story / decision slugs, annotation IDs, spec slugs in Notes, dates) survive verbatim. The `Existing artifact:` line is stripped wholesale — it never lands in durable bodies.

**Frontmatter shape.** Each story file carries `story:` slug + `status: as-is`; each decision file carries `decision:` slug + `status: as-is`. No path-form `references:` per the self-containment rule.

**Catalog creation:**

- Create directory `.ok-planner/design/stories/` if absent.
- Create directory `.ok-planner/design/decisions/` if absent.
- Create `.ok-planner/design/stories.md` as the auto-generated stories TOC (header + sorted alphabetical list of story-slug + one-line summary), refreshed by `execute-plan` when a plan touches `stories/`.
- Create `.ok-planner/design/decisions.md` as the auto-generated decisions TOC, refreshed similarly.

**Tension moves** — the spec's stories and decisions close currently-open tensions; their files move from `tensions/` to `tensions/_resolved/` with a `status: resolved` field and a `resolution:` block summarizing the outcome:

- `tensions/substitution-grammar-count-drift.md` → `tensions/_resolved/substitution-grammar-count-drift.md`. Resolution: STORY-substitution-doc-accuracy lands the accuracy gate (header-enumeration vs. resolver case-arm cross-check via `lib/graph/attribute/substitution_test.go`'s `headerBulletPattern`) as a durable story; the muddiness is closed because the gate now exists and the story names where it lives.
- `tensions/control-api-version-prefix.md` → `tensions/_resolved/control-api-version-prefix.md`. Resolution: TD-protocol-version-v1-namespaced sweeps every control-API HTTP route under `/v1/`, aligning the URL layer with the already-versioned proto package. Pre-v1 freedom means no transition window; bare paths are removed when the sweep lands. Code work implied by this resolution: every route registration under `lib/control/controlapi/` moves under a `/v1/` mount; the MCP route catalog and every test that hits a bare path updates in lockstep; the rimsky CLI client (and the `rimsky-cli` thin client referenced in the tension's evidence) issues requests against the `/v1/` paths.
- `tensions/events-kind-no-enum.md` → `tensions/_resolved/events-kind-no-enum.md`. Resolution: TD-event-log-kind-enum declares the canonical operational kinds in `proto/v1/events.proto` and commits rimsky's app logic to consuming typed values exclusively (the generated Go enum for operational kinds; the parsed signal type-path for signal-class kinds). Code work implied: extend `proto/v1/events.proto` with an `OperationalKind` enum covering every kind currently emitted (`auth.*`, `state_transition`, `lock_acquired`, `work_started`, `attributes_substituted`, `breakpoint.hit`, etc.); regenerate Go bindings; sweep every emit site under `lib/runtime/`, `lib/graph/`, `lib/control/controlapi/`, `lib/foundation/audit/`, etc. to use the typed enum; tighten the persistence-layer marshal/unmarshal paths to fail on unknown strings; update read filters (`GET /events?kind=...`, `GET /audit?kind=...`) to validate the query parameter against the enum at the request boundary.

**Concept mutations** — two concept bodies are stale relative to decisions this spec records:

- `concepts/module-layout.md` — mutate in place. In the `## What it is` opening sentence, replace "The Go workspace ties four modules into one build" with "The Go workspace ties five modules into one build". Add a new bullet to the module list after the Root module entry: "**Examples module** (under the repo root, alongside the lib group) — copy-and-modify reference implementations of each rimsky-implementable protocol (executor, publisher, claim-producer, validation, data-processing, lifecycle-subscriber) plus the atomic-staging filesystem-producer reference. Depends only on the protocols module via workspace; never imported back into the layered packages. Apache-licensed (the protocols-module sibling for permissive consumers). The 2026-05-15 bundled-deliverables Notes entry describes its lineage; the examples module is the 2026-06-08 promotion to a full workspace module so it builds, lints, and tests as part of the workspace gate." In the `## Boundaries` section's Owns clause, replace "the four-module workspace (protocols, foundation, services, root)" with "the five-module workspace (protocols, foundation, services, root, examples)". In the `## Licensing boundary` section, replace "the Apache surface is the protocols module — the wire contract a consumer implements or links against — plus a documentation carve-out; the protocols module imports nothing internal, so the Apache code forms a single closed island with no path to AGPL source" with "the Apache surface is the protocols module + the examples module (copy-and-modify reference implementations) + the TypeScript claude-agent executor reference + the cold-read style guide and manifesto, per `decision:licensing-dual-apache-agpl`. The protocols module and the examples module form a closed Apache island in the Go import graph (examples imports only the protocols module via workspace); the TypeScript claude-agent reference sits outside the Go import graph; cold-read is documentation."; and in the same section, replace "the single Go Apache island remains the protocols module" with "the Go Apache island is the protocols-and-examples pair, both consumed via workspace by external implementers." Append a Notes entry: `2026-06-08 — Workspace promoted to five modules with the explicit addition of the examples module per spec:2026-06-08-design-corpus-bootstrap; Licensing-boundary section updated so the Apache surface lists all four members (protocols + examples + claude-agent + cold-read) and the Go Apache island expands to protocols+examples.`
- `concepts/message.md` — mutate in place. In the `## Idempotency` section, replace "accepts an `Idempotency-Key` HTTP header (string, ≤256 chars). When present, rimsky computes the dedup tuple `(instance_id, sender, idempotency_key)`" with "requires an `Idempotency-Key` HTTP header (string, ≤256 chars). Requests without the header are refused. Rimsky computes the dedup tuple `(instance_id, sender_kind, sender, sender_subject, idempotency_key)`, where the dedup-layer `sender_kind` enum is `operator | publisher | anonymous` (see `decision:message-sender-kind-discriminator` for the relationship to the envelope's three-value `sender_kind`). The `sender_subject` column carries the requester's identity (api-key id, publisher subscription id, or the `anonymous` sentinel) so distinct callers with the same key never replay each other." Append a Notes entry: `2026-06-08 — Idempotency made universal (mandatory header); dedup tuple widened to five columns with sender_kind + sender_subject discriminators per spec:2026-06-08-design-corpus-bootstrap.`
- `concepts/event-log.md` — mutate in place. In the `## What it is` section, replace "a free-form `kind` text column (no enum constraint)" with "a typed `kind` value (operational kinds drawn from a proto-declared enum; signal-class kinds carrying canonical signal type-paths)". In the `## Purpose` section, replace "The free-form `kind` column lets new event categories appear with zero migration; the price is that typos produce events no consumer finds." with "Adding a new operational kind = adding an enum value in the events proto + regenerating Go bindings (no schema migration; the storage column stays `TEXT`). Rimsky's app logic consumes typed values exclusively, never raw strings, so typo-induced silent observability blind spots are prevented at the app boundary." In the `## Invariants` section, replace "The `kind` column is free-form; no enum constraint. Zero-migration to add a new kind; typos produce events no consumer finds." with "The `kind` value is typed at rimsky's app boundary: operational kinds via the proto-declared `OperationalKind` enum (see `decision:event-log-kind-enum`); signal-class kinds via the parsed signal type-path. The persistence column stays `TEXT` for marshaling flexibility — no `CHECK` constraint, because the enum at the app boundary IS the gate (unknown strings at the unmarshal boundary are defensive errors, not control-flow inputs)." Also in `## Invariants`, replace the last line "The audit log's `kind` column is free-form — see `tension:events-kind-no-enum`." with "The audit log's `kind` value is typed at the app boundary — see `decision:event-log-kind-enum` (replaces the pre-2026-06-08 free-form posture)." Append a Notes entry: `2026-06-08 — Kind discriminator moved from free-form to typed via proto enum (operational kinds) + signal type-path (signal-class kinds); tension:events-kind-no-enum resolved; persistence column shape unchanged. Per spec:2026-06-08-design-corpus-bootstrap.`

**Story file creation** — for each STORY-«slug» above, create `.ok-planner/design/stories/«slug».md` capturing the story body (Role / Capability / Business value / Acceptance / Falsifier / Proof). Apply the path-rewriting rule above (e.g., `examples/` → "the examples module"). Strip the `Existing artifact:` line. Each file gets `story: «slug»` + `status: as-is` frontmatter and an append-only `## Notes` section ending with: `2026-06-08 — Story landed via spec 2026-06-08-design-corpus-bootstrap.`

The full list of 62 story files to create:
- `stories/template-lifecycle.md`, `stories/instance-lifecycle.md`, `stories/tag-management.md`, `stories/node-admin.md`, `stories/message-bus.md`, `stories/event-log-read.md`, `stories/audit-log-read.md`, `stories/breakpoint-debugger.md`, `stories/asset-management.md`, `stories/backfill-ops.md`, `stories/lineage-exploration.md`, `stories/lineage-admin.md`, `stories/api-key-management.md`, `stories/runtime-diagnostics.md`, `stories/client-context.md`
- `stories/operator-onboarding.md`, `stories/compose-lifecycle.md`, `stories/compose-namespace-guard.md`, `stories/mcp-transport.md`, `stories/anonymous-mode-bootstrap.md`, `stories/dry-run-request-flag.md`, `stories/dry-run-mode-floor.md`, `stories/grant-scope-enforcement.md`, `stories/forensic-last-attribute.md`, `stories/rules-doc-accuracy.md`
- `stories/claim-scope-substitution.md`, `stories/substitution-doc-accuracy.md`, `stories/ref-validation-mode.md`, `stories/mandatory-instantiation-gate.md`, `stories/lenient-marker.md`, `stories/verifier-severity-partition.md`, `stories/template-fan-out.md`, `stories/template-sub-graph-delegation.md`, `stories/template-error-policy.md`, `stories/template-subscriptions.md`
- `stories/executor-protocol.md`, `stories/executor-trace-observability.md`, `stories/http-node.md`, `stories/claude-agent.md`, `stories/verifier-http.md`
- `stories/publisher-protocol.md`, `stories/sensor-cron.md`, `stories/sensor-http.md`, `stories/sensor-webhook.md`, `stories/sensor-object-store.md`
- `stories/claim-producer-protocol.md`, `stories/claim-producer-scopes-conflict.md`, `stories/claim-producer-conformance.md`, `stories/claim-producer-observability.md`, `stories/store-filesystem.md`, `stories/store-postgres.md`
- `stories/validation-author.md`, `stories/data-processing-author.md`, `stories/lifecycle-subscriber-author.md`, `stories/subscriber-openlineage.md`
- `stories/host-agent-late-bind-all-protocols.md`, `stories/host-agent-per-run-scope-isolation.md`, `stories/host-agent-per-binding-overrides.md`, `stories/host-agent-anonymous-mode.md`, `stories/host-agent-control-plane.md`
- `stories/rimsky-deployment-bootstrap.md`, `stories/rimsky-health-check.md`

**Decision file creation** — for each TD-«slug» above, create `.ok-planner/design/decisions/«slug».md` capturing the decision body (Choice / Rationale / Alternatives where present). Apply the path-rewriting rule above for the depguard / layout / language-split / licensing TDs whose Choice text names directories. Each file gets `decision: «slug»` + `status: as-is` frontmatter and an append-only `## Notes` section ending with: `2026-06-08 — Decision recorded via spec 2026-06-08-design-corpus-bootstrap.`

The full list of 75 decision files to create:
- `decisions/module-split.md`, `decisions/layer-ordering.md`, `decisions/toplevel-dirs.md`
- `decisions/depguard-pgx-isolation.md`, `decisions/depguard-foundation-internal.md`, `decisions/depguard-protocols-purity.md`, `decisions/depguard-foundation-purity.md`, `decisions/depguard-graph-purity-with-scheduler-exception.md`, `decisions/depguard-runtime-purity.md`, `decisions/depguard-consumption-isolation.md`, `decisions/revive-no-exported-rule.md`
- `decisions/logging-slog-only.md`, `decisions/http-router-chi.md`, `decisions/postgres-pgx-v5.md`, `decisions/sqlite-modernc-pure-go.md`, `decisions/cron-robfig-v3.md`, `decisions/uuid-google.md`, `decisions/yaml-gopkg-v3.md`, `decisions/jcs-cyberphone.md`, `decisions/grpc-google-official.md`, `decisions/testcontainers-go.md`, `decisions/metrics-prometheus-client.md`
- `decisions/cold-read-style.md`, `decisions/blessed-invariant-annotations.md`, `decisions/design-link-annotations.md`, `decisions/tracked-duplication.md`
- `decisions/build-tool-makefile.md`, `decisions/build-go-version.md`, `decisions/build-cgo-disabled.md`, `decisions/image-two-stage.md`, `decisions/image-set-four-core.md`, `decisions/image-set-bundled-services.md`, `decisions/image-entrypoint-role-selection.md`, `decisions/image-tagging-version-and-channel.md`, `decisions/registry-hub-rimskyai-namespace.md`
- `decisions/persistence-dual-backend.md`, `decisions/migrations-append-only-numbered.md`, `decisions/migrations-no-compat-shims.md`, `decisions/advisory-locks.md`, `decisions/blob-backends-pluggable.md`, `decisions/blob-spill-threshold-config.md`, `decisions/message-idempotencies-dedup-tuple.md`, `decisions/wait-set-topic-kind-taxonomy.md`
- `decisions/protocol-version-v1-namespaced.md`, `decisions/async-callback-post-json.md`, `decisions/async-callback-outcome-oneof.md`, `decisions/idempotency-key-header-universal.md`, `decisions/idempotency-status-code-distinction.md`, `decisions/message-sender-kind-discriminator.md`, `decisions/grpc-internal-protocols.md`, `decisions/protojson-gateway.md`, `decisions/spec-jcs-canonicalization.md`, `decisions/event-log-payload-shapes.md`, `decisions/event-log-kind-enum.md`, `decisions/conformance-suite-per-protocol.md`
- `decisions/auth-api-key-bearer.md`, `decisions/auth-dry-run-request-flag.md`, `decisions/auth-dry-run-mode-floor-on-key.md`, `decisions/auth-grant-scope.md`
- `decisions/release-formal-skill.md`, `decisions/release-semver-from-diff.md`, `decisions/release-notes-template.md`, `decisions/release-dev-mechanical.md`, `decisions/release-semver-sha-dot-joined.md`, `decisions/release-chain.md`, `decisions/release-scan-docker-scout.md`, `decisions/release-attestations.md`, `decisions/release-distribution.md`
- `decisions/pre-v1-break-freely.md`
- `decisions/licensing-dual-apache-agpl.md`, `decisions/licensing-enforced-by-license-lint.md`, `decisions/implementation-language-go-plus-ts.md`, `decisions/config-format-yaml.md`, `decisions/testing-scenario-based-e2e.md`, `decisions/project-agnostic.md`

**Story / decision annotations on code** — Add `@story:` and `@decision:` annotations at load-bearing code sites for each story and TD where the implementation is unambiguous (the wired entry point for stories; the embodiment site for decisions). Where the load-bearing site is ambiguous or distributed, the annotation is omitted; the design catalog stands on its own without exhaustive code citations. Annotations follow the same granularity discipline as `@blessed-invariant`: where the design is enforced or expressed, not every file touching it.

## Manifest

### Stories

- **STORY-template-lifecycle** — operator manages template catalog (Proof: executable proof)
- **STORY-instance-lifecycle** — operator manages instance runtime lifecycle (Proof: executable proof)
- **STORY-tag-management** — operator manages movable template-hash names (Proof: executable proof)
- **STORY-node-admin** — operator inspects and admin-invalidates nodes (Proof: executable proof)
- **STORY-message-bus** — sender emits idempotent messages into instance bus (Proof: executable proof)
- **STORY-event-log-read** — operator reads unified chronological event feed (Proof: executable proof)
- **STORY-audit-log-read** — operator reads auth-relevant action audit (Proof: executable proof)
- **STORY-breakpoint-debugger** — operator debugs live instance via breakpoints (Proof: executable proof)
- **STORY-asset-management** — operator manages instance-produced data assets (Proof: executable proof)
- **STORY-backfill-ops** — operator re-processes historical data via backfill (Proof: executable proof)
- **STORY-lineage-exploration** — operator walks lineage forward and backward (Proof: executable proof)
- **STORY-lineage-admin** — operator prunes lineage records (Proof: executable proof)
- **STORY-api-key-management** — operator administers api-key lifecycle (Proof: executable proof)
- **STORY-runtime-diagnostics** — operator inspects runtime wedge state (Proof: executable proof)
- **STORY-client-context** — operator switches between control-api endpoints (Proof: demo)
- **STORY-operator-onboarding** — new operator runs first dev-loop end-to-end (Proof: demo)
- **STORY-compose-lifecycle** — operator drives multi-resource compose manifest (Proof: executable proof)
- **STORY-compose-namespace-guard** — server enforces reserved compose prefix (Proof: executable proof)
- **STORY-mcp-transport** — operator/agent drives rimsky entirely via MCP (Proof: executable proof)
- **STORY-anonymous-mode-bootstrap** — fresh deployment opens then locks down (Proof: executable proof)
- **STORY-dry-run-request-flag** — operator previews any write per-request (Proof: executable proof)
- **STORY-dry-run-mode-floor** — operator mints attempt-only key (Proof: executable proof)
- **STORY-grant-scope-enforcement** — least-privilege delegation across lifecycle (Proof: executable proof)
- **STORY-forensic-last-attribute** — operator reads node's latest attribute bag (Proof: executable proof)
- **STORY-rules-doc-accuracy** — contributor trusts rules.md citations (Proof: executable proof)
- **STORY-claim-scope-substitution** — template author uses canonical claim_scope (Proof: executable proof)
- **STORY-substitution-doc-accuracy** — substitution module header matches resolver (Proof: executable proof)
- **STORY-ref-validation-mode** — operator chooses registration-time strictness (Proof: executable proof)
- **STORY-mandatory-instantiation-gate** — instance create validates value constraints (Proof: executable proof)
- **STORY-lenient-marker** — template author marks substitution lenient (Proof: executable proof)
- **STORY-verifier-severity-partition** — template author distinguishes warning vs error (Proof: executable proof)
- **STORY-template-fan-out** — template author declares fan-out partitioning (Proof: executable proof)
- **STORY-template-sub-graph-delegation** — template author composes via sub-graphs (Proof: executable proof)
- **STORY-template-error-policy** — template author routes error classes (Proof: executable proof)
- **STORY-template-subscriptions** — template author wires CEL-predicated subscriptions (Proof: executable proof)
- **STORY-executor-protocol** — service author writes custom executor (Proof: example)
- **STORY-executor-trace-observability** — operator queries/streams executor traces (Proof: executable proof)
- **STORY-http-node** — template author integrates HTTP upstreams (Proof: executable proof)
- **STORY-claude-agent** — operator wires agentic node with full controls (Proof: executable proof)
- **STORY-verifier-http** — template author validates via external check service (Proof: executable proof)
- **STORY-publisher-protocol** — service author writes custom publisher (Proof: example)
- **STORY-sensor-cron** — operator wires durable cron-driven message (Proof: executable proof)
- **STORY-sensor-http** — operator wires poll-driven HTTP message (Proof: executable proof)
- **STORY-sensor-webhook** — operator wires inbound-webhook message (Proof: executable proof)
- **STORY-sensor-object-store** — operator wires object-store-driven message (Proof: executable proof)
- **STORY-claim-producer-protocol** — service author writes custom claim-producer (Proof: example)
- **STORY-claim-producer-scopes-conflict** — operator uses non-trivial overlap rules (Proof: executable proof)
- **STORY-claim-producer-conformance** — author proves producer correct via conformance CLI (Proof: executable proof)
- **STORY-claim-producer-observability** — operator dashboards producer-side state (Proof: executable proof)
- **STORY-store-filesystem** — operator uses filesystem-backed store (Proof: executable proof)
- **STORY-store-postgres** — operator uses postgres-backed staged-async store (Proof: executable proof)
- **STORY-validation-author** — service author writes validation mix-in (Proof: example)
- **STORY-data-processing-author** — claim-producer author writes typed-data mix-in (Proof: example)
- **STORY-lifecycle-subscriber-author** — service author writes lifecycle subscriber (Proof: example)
- **STORY-subscriber-openlineage** — operator emits OpenLineage to data-platform (Proof: executable proof)
- **STORY-host-agent-late-bind-all-protocols** — every protocol works through late-bind (Proof: executable proof)
- **STORY-host-agent-per-run-scope-isolation** — concurrent run-scopes get isolated children (Proof: executable proof)
- **STORY-host-agent-per-binding-overrides** — per-binding env/args/cwd/timeout honored (Proof: executable proof)
- **STORY-host-agent-anonymous-mode** — late-bind works under anonymous mode (Proof: executable proof)
- **STORY-host-agent-control-plane** — operator manages agent lifecycle via CLI (Proof: demo)
- **STORY-rimsky-deployment-bootstrap** — entrypoint role selection + migrate discipline (Proof: executable proof)
- **STORY-rimsky-health-check** — health probe surface for LBs and k8s (Proof: executable proof)

### Technical decisions

- **TD-module-split** — Five-module Go workspace (root + foundation + protocols + services + examples)
- **TD-layer-ordering** — Four-layer ordered code split under the root module
- **TD-toplevel-dirs** — `cmd/` / `lib/` / `test/` / `tools/` top-level layout
- **TD-depguard-pgx-isolation** — `pgx` confined to persistence + services + cmd + test
- **TD-depguard-foundation-internal** — `foundation/internal/` package-private
- **TD-depguard-protocols-purity** — protocols imports stdlib + grpc/protobuf + uuid + yaml only
- **TD-depguard-foundation-purity** — foundation imports stdlib + protocols + chosen libs
- **TD-depguard-graph-purity-with-scheduler-exception** — graph pure with documented scheduler exception
- **TD-depguard-runtime-purity** — runtime imports foundation + graph + protocols only
- **TD-depguard-consumption-isolation** — services imports protocols only
- **TD-revive-no-exported-rule** — revive's `exported` rule disabled
- **TD-logging-slog-only** — stdlib `log/slog` for logging
- **TD-http-router-chi** — `go-chi/chi/v5` for HTTP routing
- **TD-postgres-pgx-v5** — `jackc/pgx/v5` postgres driver
- **TD-sqlite-modernc-pure-go** — `modernc.org/sqlite` pure-Go driver
- **TD-cron-robfig-v3** — `robfig/cron/v3` for cron parsing
- **TD-uuid-google** — `google/uuid` for UUID generation
- **TD-yaml-gopkg-v3** — `gopkg.in/yaml.v3` for YAML parsing
- **TD-jcs-cyberphone** — `cyberphone/json-canonicalization` for JCS canonicalization
- **TD-grpc-google-official** — `google.golang.org/grpc` + `protobuf`
- **TD-testcontainers-go** — `testcontainers-go` for integration tests
- **TD-metrics-prometheus-client** — `prometheus/client_golang` for metrics
- **TD-cold-read-style** — file-by-feature; ~500-line file / ~100-line function guidelines; max 3 nesting
- **TD-blessed-invariant-annotations** — `@blessed-invariant` for safety properties
- **TD-design-link-annotations** — `@concept:` / `@story:` / `@decision:` for code-to-design links
- **TD-tracked-duplication** — `@source:` / `@diverged:` over hidden coupling
- **TD-build-tool-makefile** — Makefile as build orchestration single source of truth
- **TD-build-go-version** — Go 1.25.0 minimum
- **TD-build-cgo-disabled** — `CGO_ENABLED=0` for all builds
- **TD-image-two-stage** — alpine build → distroless static nonroot runtime
- **TD-image-set-four-core** — four core images (rimsky / all-in-one / host-agent-proxy / conformance)
- **TD-image-set-bundled-services** — one image per bundled service
- **TD-image-entrypoint-role-selection** — `rimsky-entrypoint` selects roles by command arg
- **TD-image-tagging-version-and-channel** — `:v0.x.y` immutable + `:latest`/`:dev` channel
- **TD-registry-hub-rimskyai-namespace** — Docker Hub namespace `rimskyai`
- **TD-persistence-dual-backend** — both Postgres and SQLite supported
- **TD-migrations-append-only-numbered** — numbered append-only migrations per backend
- **TD-migrations-no-compat-shims** — pre-v1 freedom to drop+recreate
- **TD-advisory-locks** — postgres advisory locks + sqlite equivalent at session level
- **TD-blob-backends-pluggable** — pluggable blob backend interface
- **TD-blob-spill-threshold-config** — configurable inline-vs-spill threshold
- **TD-message-idempotencies-dedup-tuple** — 5-tuple message dedup discriminator
- **TD-wait-set-topic-kind-taxonomy** — 5-value topic-kind taxonomy + legacy fallback
- **TD-protocol-version-v1-namespaced** — `rimsky.v1` proto package + all control-API HTTP routes under `/v1/` (resolves `tension:control-api-version-prefix`; pre-v1 sweep, no transition window)
- **TD-async-callback-post-json** — HTTP POST JSON to callback URL
- **TD-async-callback-outcome-oneof** — exactly-one outcome key in callback body
- **TD-idempotency-key-header-universal** — `Idempotency-Key` header mandatory on emit
- **TD-idempotency-status-code-distinction** — 201 fresh / 200 replay
- **TD-message-sender-kind-discriminator** — `sender_kind` enum on envelope
- **TD-grpc-internal-protocols** — gRPC for all service-to-service protocols
- **TD-protojson-gateway** — protojson marshaling for HTTP+JSON bridge
- **TD-spec-jcs-canonicalization** — JCS (RFC 8785) for template-spec hashing
- **TD-event-log-payload-shapes** — typed oneof payloads for signal-class events; free-form JSON for operational events
- **TD-event-log-kind-enum** — canonical kind enum declared in proto; app logic consumes typed values, persistence is marshaling detail (resolves `tension:events-kind-no-enum`)
- **TD-conformance-suite-per-protocol** — one conformance suite per protocol
- **TD-auth-api-key-bearer** — api-key bearer token auth
- **TD-auth-dry-run-request-flag** — `?dry_run=true` per-request flag
- **TD-auth-dry-run-mode-floor-on-key** — identity-bound dry-run via grant mode
- **TD-auth-grant-scope** — per-grant scope dimension constraint
- **TD-release-formal-skill** — `/release` skill drives formal release
- **TD-release-semver-from-diff** — SemVer derived from diff inspection
- **TD-release-notes-template** — template-driven notes sections
- **TD-release-dev-mechanical** — `make dev-release` is mechanical
- **TD-release-semver-sha-dot-joined** — SHA dot-joined into SemVer pre-release segment
- **TD-release-chain** — shared `lint → license-lint → core-images → service-images → test-all → scan → push-images` chain
- **TD-release-scan-docker-scout** — `docker scout cves` gate on critical/high
- **TD-release-attestations** — SBOM + provenance via `buildx --push`
- **TD-release-distribution** — Hub + npm + Go modules + GitHub Releases
- **TD-pre-v1-break-freely** — no backwards-compat guarantee pre-v1
- **TD-licensing-dual-apache-agpl** — protocols Apache; rest AGPL
- **TD-licensing-enforced-by-license-lint** — license-check enforces import direction
- **TD-implementation-language-go-plus-ts** — Go core; TS only for claude-agent
- **TD-config-format-yaml** — plain YAML config
- **TD-testing-scenario-based-e2e** — scenario-based e2e via testcontainers
- **TD-project-agnostic** — no consumer specifics in code/docs/tests

### Design changes

- 62 new `design/stories/<slug>.md` files (one per STORY-«slug»; bodies path-rewritten to structural language per the self-containment treatment)
- 75 new `design/decisions/<slug>.md` files (one per TD-«slug»; bodies path-rewritten to structural language per the self-containment treatment)
- 2 new auto-generated TOCs: `design/stories.md` and `design/decisions.md`
- 3 concept mutations: `concepts/module-layout.md` updated to reflect the five-module workspace (adds the examples module) per TD-module-split; `concepts/message.md` updated to reflect mandatory `Idempotency-Key` and the five-column dedup tuple per TD-idempotency-key-header-universal and TD-message-idempotencies-dedup-tuple; `concepts/event-log.md` updated to reflect the typed-kind discipline (proto-declared enum + signal type-path) per TD-event-log-kind-enum
- 3 tension moves: `tensions/substitution-grammar-count-drift.md` → `tensions/_resolved/` (resolved by STORY-substitution-doc-accuracy); `tensions/control-api-version-prefix.md` → `tensions/_resolved/` (resolved by TD-protocol-version-v1-namespaced — implies a code sweep of every bare control-API route to `/v1/`); `tensions/events-kind-no-enum.md` → `tensions/_resolved/` (resolved by TD-event-log-kind-enum — implies a proto-enum addition + typed-enum sweep across every event-kind emit and read site)
- `@story:` and `@decision:` annotations on load-bearing code sites where the embodiment is unambiguous
