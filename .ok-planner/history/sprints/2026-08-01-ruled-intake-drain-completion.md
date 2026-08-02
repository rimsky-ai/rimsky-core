# Completion report: 2026-08-01-ruled-intake-drain

Execution record, kept current as stages land.

## Work done

- **Concept amendments (5)** — applied the sprint's final bodies to `sensor` (webhook definitive-outcome ack sentence), `claim-producer` (pick-policy terminal-disposition sentence on the split-scope bullet), `host-agent` (argv/cwd/ready-timeout defaults joined to the env-inheritance invariant), `control-api` (health-probe success semantics: persistence the one dependency checked), `cascade` (new two-boundary/opacity run-scope invariant). Each existing file matched the sprint body except the one stated change, applied surgically; no concepts-TOC summary line changed.
- **Decisions: three amendments** — `subscription-reconciler` (resync issues only rows missing from the live set), `image-entrypoint-role-selection` (unknown command exits non-zero naming the value; migrate synchronous before roles in all topologies), `artifact-layout` (openness constraint: both stores openable with widely available tooling, no rimsky-specific reader; third alternative recorded). Applied verbatim.
- **Decisions: five new** — `claude-agent-error-classes-closed`, `fanout-list-array-store-agnostic`, `bundled-recipes-production-paths`, `doc-accuracy-gates`, `lineage-subscriber-poller`. Applied verbatim.
- **Decisions TOC** — three amended summary lines updated; five new lines inserted alphabetically.

- **Stories: sixteen reductions + two new + two retired + TOC** — all sixteen amended story bodies applied verbatim; `fanout-list-array` and `verifier-shape-checks` created; `fs-fanout-list-array` and `pg-fanout-list-array` deleted; stories TOC updated (two lines removed, two added; amended stories' summary lines were already accurate).
- **Annotation repoint + sweep** — three live annotations citing a retired slug repointed to `fanout-list-array`: `@story: fs-fanout-list-array` in the sub-claim runner test, and `# @story: fs-fanout-list-array` / `# @story: pg-fanout-list-array` at `examples/fanout-fs-list-array/demo.sh:5` and `examples/fanout-pg-list-array/demo.sh:5` (the first sweep covered only the root module and missed the `examples/` module; caught at certification and fixed). The companion prose naming the dead slugs in both `template.yaml` headers/descriptions and `examples/fanout-pg-list-array/producer-config.yml` was updated to `STORY-fanout-list-array` with the backend named alongside. The only remaining hits are in archived plan records, left untouched per record discipline.
- **List fan-out e2e against a bundled store** — the previously annotated runner test exercises fan-out machinery against a fake store, so a new e2e was added: `lib/services/test/scenarios/claim_producers/fs_fanout_list_e2e_test.go` boots the bundled filesystem claim-producer container plus a rimsky stack, drives a fan-out node whose `partition_request` is a three-item list, and asserts one partition run-scope and one sub-claim per item; carries `@story: fanout-list-array`.
- **Shape-checks story test** — the existing severity-partition e2e (`lib/services/test/scenarios/verifier_severity_partition_e2e_test.go`) drives the shape-checks verifier through real dispatches with real checks (no-nulls, numeric-range); annotated with both `@story: verifier-shape-checks` and `@story: verifier-severity-partition` (it had no annotation before).
- **@decision annotations at enforcement sites** — `claude-agent-error-classes-closed` at the claude-agent emission gate (`errorClassDeclared`), `lineage-subscriber-poller` at the openlineage subscriber's poll loop (`Run`), `doc-accuracy-gates` at both doc-gate tests (rulesdoc + substitution-doc), `bundled-recipes-production-paths` at the park-resume recipe's rate-limit handler (the park-induction site), `fanout-list-array-store-agnostic` at both bundled producers' list split functions.
- **Carry-verbatim erasure** — decision file deleted, TOC line removed; the validator's named `carry_verbatim` case folded into the generic unknown-value rejection (the generic message listing the four valid kinds is now the only failure mode); the `@decision: carry-verbatim-requires-one` annotation removed with it; the value-specific test replaced by `TestValidateFanOut_RejectsUnknownAggregationPolicyKind`, asserting the retired value and an arbitrary junk value both hit the generic rejection. The spec-layer aggregation vocabulary already carried only the four live values — no other named recognition existed.

## Verification

- All 34 corpus deltas mechanically diffed against the sprint's fenced bodies: verbatim, including the two story retirements and the decision retirement (files gone).
- Every `@concept:`/`@story:`/`@decision:` annotation in the changed files resolves to a live artifact.
- `make lint` clean (golangci across all five modules + license-check, 0 violations).
- `make test-all` (core + service + test images rebuilt from this tree, then root, foundation, protocols, services, and examples module suites) exited 0.
- The new list fan-out e2e passes against the bundled filesystem producer (`TestFSFanOutListArrayE2E`).

## Divergences

- The sprint's "confirm the annotated test exercises the merged story end-to-end against at least one bundled store" could not be confirmed on the existing runner test (fake store); the new filesystem-producer e2e above closes that gap rather than leaving the confirmation unmet.

## Calls made where the sprint was silent

- The shape-checks story annotation landed on the existing severity-partition e2e rather than a new test: it already drives the shape-checks verifier's check capability through real dispatches, and the work item asks for a new test only "if none exists".
- The subgraph delegation exit-carry tests (`SettleFromDelegate_CarryVerbatim_*` under `test/scenarios/subgraph/`) were left untouched: they name a live delegation settlement mechanism, not the retired fan-out aggregation-policy value the pure-removal rule targets.

---

# Certification — 2026-08-01-ruled-intake-drain

Status: certified clean

## Outcomes delivered

- **One list fan-out story, store-agnostic** (`story:fanout-list-array`, `decision:fanout-list-array-store-agnostic`): the backend-specific story pair is retired; a template author's list fan-out is one capability across both bundled producers, now proven end-to-end against the bundled filesystem producer by a new containerized e2e.
- **The shape-checks verifier has corpus presence** (`story:verifier-shape-checks`): the last bundled public-surface service without a story is documented, its e2e annotated, reconciled with the severity-partition story.
- **Sixteen stories reduced to their canonical sentence** — every commitment their prose carried is homed: webhook definitive-outcome ack in `concept:sensor`, pick-policy terminal disposition in `concept:claim-producer`, spawn-override defaults in `concept:host-agent`, health-probe semantics in `concept:control-api`, error-class closedness / recipe fidelity / doc gates / lineage polling / audit-artifact openness in five new-or-amended decisions, resync no-reissue and entrypoint discipline in two completed decisions.
- **The ratified cascade two-boundary/opacity invariant is in the corpus** (`concept:cascade`), no longer transcript-only.
- **Carry-verbatim is erased** per the pure-removal rule: decision gone, the validator's named case folded into the generic unknown-value rejection, tests assert the generic path.

## Divergences

- The annotation-sweep work item was initially under-executed: the first sweep covered only the root module and missed two `@story` citations in `examples/` demo scripts; certification caught it and the fixer repointed both (plus companion prose in two template headers and one producer config) and corrected this report's sweep claim.
- Fixer calls, surfaced for veto: prose wording `STORY-fanout-list-array proof, <backend> backend` chosen to keep each example naming the story while identifying its backend; one unlisted prose hit (`examples/fanout-pg-list-array/producer-config.yml`) fixed alongside; the `"fs-fanout-list-array"` template-name string in the new e2e left alone (test data, not a citation); other `spec :=` locals in the validator test left alone (no package reference inside their initializers).
- Executor divergences recorded during the run (see Divergences above): the bundled-store e2e gap closed by adding the filesystem-producer test rather than leaving the sprint's confirmation unmet.
- No corpus edits were made by the fixer or architect; no kickbacks were refuted (none were made).

## Findings fixed

- Sprint alignment: 1 (dangling retired-slug citations + false sweep claim) — fixed; re-review clean.
- Code review: 2 (same dangling citations; `spec` package/local collision in the new validator test) — fixed; re-review clean.
- Test suites: clean on first pass (`make lint`, `make test-all` exit 0; fixed packages re-run green).
- Mechanical floor: clean on first pass (27 annotation pairs in changed files, all resolving).

## Issues promoted

None — no forks confirmed, no cap escalation; the intake gained nothing from this run.
