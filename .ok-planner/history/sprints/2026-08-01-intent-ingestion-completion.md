# Completion report: 2026-08-01-intent-ingestion

Execution record.

## Corpus deltas

Applied all five delta bodies verbatim (extracted mechanically from the sprint's fenced blocks, byte-for-byte; re-verified byte-equal after all later normalization passes):

- `concepts/breakpoint.md`
- `concepts/host-agent-proxy.md`
- `concepts/instance.md`
- `concepts/claim-tree.md`
- `decisions/release-distribution.md`

## Sub-claim realized-write-semantics inheritance

Code: `AcquireSubClaimsInput` gained `ParentRealizedWriteSemantics`; `AcquireSubClaims` stamps it onto every sub-claim row at insert (`lib/runtime/runner_subclaim.go`). The fan-out call site threads the parent's realized value from both parent-resolution paths — the acquired-locks path (from the open's `ClaimResult`) and the claim-handle-row path (`lib/runtime/runner_acquire_helpers.go`).

Test: `TestSubClaimInheritsParentRealizedWriteSemantics_OutsiderGetsNormalCoexistence` (`lib/runtime/runner_subclaim_write_semantics_test.go`) fans out a sub-claim from a read/sync parent and proves (1) the sub-claim row carries the parent's realized value from insert, (2) an overlapping outside read acquisition coexists through the normal evaluation instead of erroring on a missing realized value, (3) an overlapping outside read-write acquisition conflicts through the same evaluation. Green, including `-race`. Full root-module `go test ./...` green; `make lint` clean across all five modules.

## CLI-archive SBOMs

`.goreleaser.yaml` gained an `sboms` step (`artifacts: archive`): each of the four platform archives gets a syft-generated `<archive>.sbom.json` uploaded to the GitHub release beside it. Verified end-to-end with `make cli-snapshot` (four archives, four SBOMs). `RELEASING.md` documents the SBOM and the syft prerequisite; the `/release` skill's preflight now checks `goreleaser --version` and `syft version`.

## Upstream lint-conflict report

Filed in the suites' maintainer's own intake (the ok-plugins repo, local, carries its own ok-planner estate): `.ok-planner/issues/2026-08-01-220000-workspaces-src-tag-payload-fails-consumer-plumbline-lint.md` there — naming the two lint failures (prose header comments; the payload's upstream `content-addressed-src-tag` decision citation, which resolves only inside the suites' monorepo and dangles in every consumer), this project's temporary lint-ignore bridge, and candidate upstream fixes.

## Expression normalization (repairs, each open to after-the-fact veto)

- **Frontmatter strip** — 425 catalog files: removed retired `status:` fields, empty `aliases: []` lists, and `references:` blocks. Non-empty aliases kept on the 33 files that carry them — 25 concepts (22 in block-list form, 3 inline: `node-subscription`, `persistence-database`, `publisher-subscription`) and 8 decisions; no story carries an alias. Re-derive by listing every catalog file whose `aliases:` key has at least one entry in either form. The five delta files excluded (already canonical).
- **`_retired/` deleted** — `concepts/` (11 files), `stories/` (3), `decisions/` (4); git history is the archive. The three TOCs' trailing "Retired …" sections, which pointed at those files, removed with them.
- **Story-body reduction** — all 122 stories reviewed (six parallel reviewers checking every extra clause against the concept/decision catalogs); 56 reduced to the canonical sentence, each only where every removed clause survives in the sentence, a named concept/decision, or was pure restatement. 52 already canonical. 14 left untouched because their prose carries a commitment stated nowhere else — each now has a restructure issue (below). This three-way split is the reviewers' judgment classification, not a mechanical derivation: whether a removed clause survives elsewhere in the corpus is exactly the judgment the pass exists to make, and no textual measure stands in for it. `story:audit-artifact` moved from the reduced bucket to the untouched one during certification — see Certification fixes.
- **TOC regeneration** — verified all three TOC entry lists already match the live catalogs one-to-one after the deletions; header text re-pointed from the retired "discover-design / execute-plan" verbs to sprint execution as the refresh mechanism.
- **Root CLAUDE.md** — one stale phrase ("updated … through `/execute-plan` runs") re-pointed to sprint execution, matching the TOC repair.
- Five delta files re-verified byte-equal to the sprint's deltas after all of the above.

## Story restructure proposals — 22 issues filed, 21 standing (kind `sprint`)

Splits (6): `story-node-admin-split`, `story-http-node-split`, `story-idempotent-mode-dedupes-split`, `story-instance-lifecycle-split`, `story-claude-agent-restructure`, `story-runtime-diagnostics-split`.

Collapses (3): `stories-fanout-partition-collapse`, `stories-bundled-sensor-collapse`, `stories-claim-producer-backend-collapse`.

Prescriptive-content rehoming (12 standing): `story-bundled-park-resume-recipe-mechanism-home`, `story-host-agent-control-plane-verb-contract-home`, `story-host-agent-per-binding-overrides-defaults-home`, `story-publisher-protocol-restart-reissue-home`, `story-rimsky-deployment-bootstrap-unknown-command-home`, `story-rimsky-health-check-dependency-semantics-home`, `stories-doc-accuracy-gates-decision`, `story-single-process-migrate-ordering-home`, `story-verifier-severity-allowlist-home`, `story-fanout-intent-inheritance-prescriptive-tail`, `story-subscriber-lineage-receiver-poller-decision`, `story-audit-artifact-no-special-reader-home`.

Answered by the corpus, not standing (1): `story-compose-namespace-guard-capability-clause` — see Certification fixes.

## Intent-ledger disposition — all 76 dossiers

Ten parallel disposition agents; every claim in every dossier's Net position / Required behaviors / Intentional absences / Corrections-and-restorations sections classified (a) already committed to by the corpus, (b) settled by a delta above or by an issue already in the intake or its history, (c) superseded within the ledger's own record, or (d) divergence → intake issue.

### Claim population, per dossier (mechanically re-derivable)

The population dispositioned is the literal top-level `- ` bullet count of each of the four sections, over the 76 dossier files at `.ok-planner/history/intent/` (`README.md` is not a dossier). Counts below are `net/required/absences/corrections`. Re-derive with:

```sh
for f in .ok-planner/history/intent/*.md; do b=$(basename "$f"); [ "$b" = README.md ] && continue
  awk -v n="${b%.md}" '
    /^## Net position/{s="n";next} /^## Required behaviors/{s="r";next}
    /^## Intentional absences/{s="a";next} /^## Corrections and restorations/{s="c";next}
    /^## /{s="";next} /^- /{k[s]++}
    END{printf "%s %d/%d/%d/%d\n", n, k["n"], k["r"], k["a"], k["c"]}' "$f"
done
```

- _retired 0/0/15/1 · advisory-lock 5/5/3/1 · anonymous-mode 5/10/3/2 · api-key 8/16/5/3 · area--misc 57/21/20/22 · asset 6/9/3/3 · atomic-staging 4/10/3/3 · attribute 9/51/30/20
- auto-terminal 7/15/8/5 · blob-backend 5/9/4/2 · breakpoint 11/17/9/4 · cancel-siblings 5/7/3/1 · cascade-graph 3/2/0/0 · cascade-mode 9/13/7/5 · cascade 12/32/26/13 · child-execution 5/8/4/2
- claim-co-holdership 6/13/5/5 · claim-handle 11/25/11/12 · claim-lifetime 6/15/5/4 · claim-producer 14/30/17/14 · claim-scope 7/10/5/4 · claim-tree 5/9/3/4 · claim 9/25/10/6 · conformance 7/14/7/7
- control-api 16/37/19/13 · data-processing 6/9/6/3 · delegation 6/10/4/3 · discovery-cache 6/7/3/1 · dry-run 5/9/3/2 · error-policy 17/17/13/12 · event-log 10/14/8/6 · executor 17/40/29/18
- fan-out 22/13/12/14 · frame 19/20/21/13 · graph 4/4/2/0 · host-agent-proxy 11/14/6/5 · host-agent 8/16/5/4 · inertness 4/15/4/3 · instance 17/18/12/8 · lifecycle-subscriber 8/12/7/4
- lineage-record 6/5/4/3 · lineage 8/12/9/4 · message-schema 7/13/8/2 · message-sender-node 6/7/6/2 · message 10/20/25/10 · module-layout 10/16/12/7 · named-lock 6/6/2/2 · node-run 21/30/17/19
- node-subscription 17/16/17/8 · node 12/21/14/8 · observability 8/26/10/5 · orphan-reaper 7/14/6/3 · parked-state 18/15/17/9 · peer-auth 6/8/6/2 · permission 9/15/8/3 · persistence-database 13/27/16/13
- publisher-subscription 6/12/8/2 · publisher 7/11/7/5 · replica 4/8/4/2 · rimsky-yml 8/11/7/5 · rimsky 7/10/8/3 · role-template 4/5/3/0 · run-scope 12/26/13/9 · sensor 9/14/9/8
- service 9/19/10/4 · signal 19/22/16/13 · sub-graph 6/13/5/7 · supervisor 18/26/12/12 · tag 5/8/4/2 · template 15/40/19/7 · terminal-resolution 10/15/8/7 · terminal-tag 6/7/4/1
- transition-reason 5/7/4/3 · validation 8/42/13/13 · wait-set 9/13/9/7 · write-semantics 7/12/5/5

Section totals: net 730, required 1193, absences 705, corrections 467 — **3095 bullets across 76 dossiers**. `_retired`'s Net position is a single prose paragraph rather than bullets, and 5 of the 3095 bullets carry an in-ledger `~~STRUCK~~` marking (asset ×2, data-processing ×1, dry-run ×1, validation ×1 — four dossiers), which is itself a class-(c) supersession recorded in the ledger.

### Divergence outcomes

**14 divergence candidates → 13 issues filed** (two dossiers shared the retired frame-timeout fact). Each issue maps to the dossier whose claim diverged:

| Issue | Dossier |
| --- | --- |
| `intent-claim-co-holdership-wire-parity-narrowed` | claim-co-holdership |
| `intent-claim-tree-vs-held-conflated` | claim-tree |
| `intent-inertness-payload-predicate-supersession` | inertness |
| `intent-role-template-grant-enumeration-stale` | role-template |
| `intent-transition-reason-last-outcome-tension-moot` | transition-reason |
| `intent-parked-failed-watchdog-phantom` | transition-reason |
| `intent-frame-timeout-claims-stale` | validation, observability |
| `intent-verifier-shape-checks-executor-uncited` | validation |
| `intent-node-subscription-next-frame-shape-unimplemented` | node-subscription |
| `intent-creation-reason-enum-stale` | persistence-database |
| `intent-cascade-what-it-is-stale-walk-vocabulary` | cascade |
| `intent-cascade-two-boundary-opacity-uncarried` | cascade |
| `intent-carry-verbatim-decision-doc-stale` | child-execution |

Most are retire-the-stale-ledger-claim shapes; the notable exceptions are the next-frame-shape contradiction (live corpus text with no implementing code — genuine owner ruling), the uncarried two-boundary/opacity cascade invariant (ratified transcript-tier ruling stated nowhere), and the two stale-corpus-prose rewrites (cascade What-it-is; `decision:carry-verbatim-requires-one`). Every remaining bullet in the population above was dispositioned class (a), (b), or (c) — eleven dossiers (cascade, child-execution, claim-co-holdership, claim-tree, inertness, node-subscription, observability, persistence-database, role-template, transition-reason, validation) carry the class-(d) claims listed here; no other dossier produced one.

The agents also recorded per-section class-(b) and class-(c) annotations — which claims were settled by a delta or an existing issue slug, and which were superseded inside the ledger. Those annotations are the agents' own reading, and their per-claim tallies do not line up with the literal bullet counts above (the agents merged multi-part bullets and folded struck bullets into the parent claim), so they are kept as a qualitative record, not as a count: class-(b) settlements were recorded on attribute, auto-terminal, breakpoint, cancel-siblings, cascade, cascade-graph, cascade-mode, claim, claim-handle, claim-lifetime, claim-tree, data-processing, fan-out, peer-auth, permission, persistence-database, rimsky, signal, supervisor, template, and validation; class-(c) ledger-internal supersessions on asset, claim-lifetime, error-policy, frame, host-agent, host-agent-proxy, inertness, lifecycle-subscriber, lineage-record, message-schema, node, parked-state, signal, sub-graph, supervisor, transition-reason, and validation.

### Conflicts needing human ruling

44 conflict entries across the 31 dossiers that record any (re-derive by counting `- ` bullets under `## Conflicts needing human ruling`). 7 are explicit "None" entries; the other 37 were each already RESOLVED inside the dossier's own record or settled by an existing issue slug. Zero new conflict issues were needed.

## Ingestion stamp

One stamp appended to `.ok-planner/history/intent/README.md`: dispositioned in full by this sprint; consumed history, not an unprocessed queue.

## Divergences and calls made where the sprint was silent

- Installed `syft` locally (Homebrew) to verify the goreleaser SBOM step end-to-end.
- Upstream report realized as an issue file in the ok-plugins repo's own intake (maintainer's repo is local and uses the same intake convention) rather than an external channel.
- The three catalog TOCs carried trailing "Retired …" sections pointing at the deleted `_retired/` files; removed as part of the TOC item (the sections' pointers would otherwise dangle).
- Root `CLAUDE.md`'s one reference to the retired `/execute-plan` refresh mechanism re-pointed to sprint execution while repairing the same phrase in the TOC headers.
- Two dossiers' frame-timeout claims merged into one issue (one underlying retirement, migration 024).
- Story reviewers noted two erroneous claims that existed only in prose the reduction removed (cascade-signal-blind's transient-transition observability; lifecycle-subscriber-author's response-honored claim vs the empty LifecycleAck) — no live artifact carries either, so no issue was filed.
- **The sprint's Intent says "77 per-concept dossiers"; the ledger holds 76.** `.ok-planner/history/intent/` contains 77 markdown files, one of which is `README.md` — the dossier count is 76 (and 76 files carry a `## Net position` section). The sprint's figure is a pre-existing miscount in approved prose; execution never edits an approved sprint, so it stands as written. This report and the ingestion stamp use 76 throughout. The disposition work item's "all 77 dossiers" phrasing has the same off-by-one and the same reading: every dossier in the directory.

## Certification fixes (review-fix loop)

- **`story:audit-artifact`'s reduction reverted.** The reduction dropped "The operator opens the artifact with widely-available tooling for the format — no rimsky-specific reader is required" — a constraint on the artifact's storage format that no live concept or decision restates (`decision:artifact-layout` fixes the directory shape only; `decision:persistence-driver` picks drivers without making the choice load-bearing for operator inspection). Per the expression-normalization rule, a story whose reduction fails the preservation test is left untouched and handled by the restructure-proposals item, so the pre-reduction body is restored (with the retired `status:` frontmatter still stripped) and issue `story-audit-artifact-no-special-reader-home` filed. This moves the reduction tally from 57/13 to 56/14.
- **Issue `story-compose-namespace-guard-capability-clause` answered by the corpus, not standing.** It claimed the dropped "(holding the appropriate capability)" clause named an enforcement mechanism no artifact documents. Three live invariants document it under different wording — `concept:tag` ("rejecting a `compose:`-prefixed name unless the request originates from the privileged compose path"), `concept:rimsky` ("the CLI's compose workflow identifies itself as the compose origin so the server permits it"), and `concept:control-api` ("requests originating outside the CLI's compose surface are rejected"). The reduction preserved the commitment; the story stays reduced and the issue file is rewritten to `status: answered` with the citations.
- **Fan-out threading of the parent's realized write semantics now has direct coverage.** The original test drove `AcquireSubClaims` directly, so neither `acquireFanOutIfDeclared` nor `resolveFanOutParentClaim` was exercised with a real split descriptor. Added `TestAcquireFanOutIfDeclared_SubClaimInheritsParentRealizedWriteSemantics_HoldsPath` and `…_AcquiredLockPath` (`lib/runtime/runner_acquire_helpers_fanout_write_semantics_test.go`): both go through `acquireFanOutIfDeclared` with a `SplitClaimScopeFunc` returning a real sub-scope descriptor and assert the inserted sub-claim row's realized write semantics. The Holds-binding variant asserts it equals the parent claim-handle row's value; the acquired-lock variant seeds the row with a different value so the assertion can only pass if the branch reads `AcquiredLock.ClaimResult.RealizedWriteSemantics`. Both fail when the threading is removed.
- **Alias, ledger, and story-reduction counts restated so a reader can re-derive them.** The kept-aliases figure was 3 concepts; it is 33 files (25 concepts, 8 decisions). The per-dossier ledger tallies were the agents' per-claim letter classifications and diverged from literal bullet counts in both directions; the disposition section now carries mechanical bullet counts with the derivation command, the 13 divergence issues mapped to their dossiers, and the agents' class-(b)/(c) notes retained qualitatively. The story-reduction split is marked as reviewer judgment, since no textual measure decides whether a removed clause survives elsewhere.

---

# Certification — 2026-08-01-intent-ingestion

Status: certified clean

## Outcomes delivered

- The four amended concepts and one amended decision are live verbatim: breakpoint (resume-overlay visibility across sequential pauses), host-agent-proxy (latest-wins registration for api-key identities, anonymous collisions still rejected), instance (per-template key uniqueness with idempotent re-create), claim-tree (sub-claims inherit realized write semantics), release-distribution (CLI archives ship with published SBOMs).
- A later acquisition overlapping an active fan-out sub-claim now receives a normal coexistence evaluation instead of the misleading "holder open still in flight" error — sub-claim rows carry the parent's realized write semantics from insert, proven end-to-end at both the direct-acquire layer and through the fan-out call path's two parent-resolution branches.
- A formal release now publishes one SBOM per CLI platform archive on the GitHub release, verified by snapshot build; the release preflight checks the syft prerequisite.
- The suites' maintainer holds the lint-conflict report in their own intake; this project's lint-ignore bridge is on record as temporary.
- The corpus reads in canonical form: no retired frontmatter, no `_retired/` holdovers, 56 stories reduced to their canonical sentence, TOCs regenerated with sprint execution named as the refresh mechanism.
- The intent ledger is fully dispositioned — 3,095 claims across 76 dossiers, counts mechanically re-derivable — and stamped as consumed history; 35 intake issues (21 story-restructure standing, 13 intent-ledger divergences, 1 audit-artifact rehoming) carry everything commitment-shaped to the next planning ceremony, and 1 filed issue was answered by the corpus and closed.

## Divergences

- Upstream report realized as an issue file in the ok-plugins repo's own intake rather than an external channel (maintainer's repo is local, same intake convention).
- `syft` installed locally via Homebrew to verify the SBOM chain.
- The TOCs' trailing "Retired …" sections (pointing at the deleted `_retired/` files) removed with the directories; root `CLAUDE.md`'s one `/execute-plan` reference re-pointed to sprint execution alongside the TOC-header repair.
- The sprint's Intent section says "77 per-concept dossiers"; 76 exist (`README.md` is the 77th file). Execution used 76 throughout; the sprint text stays unedited.
- Two dossiers' frame-timeout claims merged into one issue (one underlying retirement).
- Fixer call: completion-report ledger counts reconciled to mechanical bullet counts rather than re-dispatched per-cell letter tallies — the letters encode judgment no recount makes checkable; the mechanical population is re-derivable by the embedded script.
- Fixer call: the acquired-lock fan-out test seeds the parent row with a different realized value than the lock so the branch is discriminated; the code's precedence (lock value wins) is the only reading.
- Corpus edit (fixer): `stories/audit-artifact.md` restored to its pre-reduction body — the reduction had dropped the no-rimsky-specific-reader commitment, which has no other corpus home; a rehoming issue now carries it.
- Fixer call: `story-compose-namespace-guard-capability-clause` resolved as answered-by-the-corpus (concept:tag, concept:rimsky, concept:control-api all state the privileged-compose-origin enforcement) and closed to `history/issues/`; the reduction of that story stands.

## Findings fixed

- Sprint alignment: 4 (cycle 1: non-re-derivable ledger counts; audit-artifact reduction dropped a commitment; compose-namespace-guard reduction/issue premise; 77-vs-76 dossier miscount) + 2 (cycle 2: answered issue file not yet moved to history; `## Problem` heading missing from the 34 newly filed open issues). Cycle 3 clean.
- Test suites: clean on first pass (full root-module suite, scenario + storage suites via testcontainers, race-sensitive suites at -race -count=3, lint across all five modules) and re-run green after each fix cycle.
- Mechanical floor: 1 (the completion report quoted the upstream citation in `@decision:` tag shape, scanning as a dangling annotation; rephrased). All annotations in changed files resolve to live artifacts.
- Code review: 2 (fan-out threading of the parent's realized write semantics untested through `acquireFanOutIfDeclared` — two discriminating tests added, mutation-verified; alias-count error in the completion report). Cycle 2 clean.

## Issues promoted

None — the gate promoted nothing (no kickbacks reached the architect, no cap escalation). The 41 issues in the intake are the sprint's own deliverables: 6 promoted into this sprint (closing with it) and 35 filed by it (34 open awaiting /verify-issues + the next planning ceremony; 1 answered and closed).
