# Rimsky licensing — Design

## Status

- Proposal, 2026-05-02.
- Outcome of a 2026-05-02 conversation about how to license Rimsky for the first time. The repo currently has no `LICENSE` file at the root and no per-file copyright headers; this is a first-time license declaration, not a relicense.
- No prior design doc on licensing. References:
  - `CLAUDE.md` — package import rules and module layout (load-bearing for §4 boundary mapping).
  - `docs/specs/2026-05-02-rimsky-cli-and-compose-design.md` — the CLI surface and embeddability story.
  - `docs/specs/2026-04-27-stores-redesign-v3-design.md` — peer-service architecture (executors, stores) that the boundary mapping leans on.
  - The pre-v1 / break-freely posture in `.claude/rules/rules.md` — no backwards-compat constraints to satisfy.

## Context

Rimsky is positioned to be embedded by many independent consumers (as a Go module, as Docker images, as a git submodule). Two pressures are in tension:

1. **Embedder reach.** Anything that touches Rimsky's wire surface — protocol definitions, executor SDK, reference stores, the CLI — needs to be permissively licensed so consumers can build proprietary executors, in-house stores, and product-side tooling against it without copyleft contagion. This is non-negotiable: a project-agnostic platform that embedders won't embed has no consumers.
2. **Anti-repackaging protection.** The orchestrator itself (the scheduler / supervisor / control-api binaries plus their internal logic) is the part that someone could repackage and offer as a competing managed service. The Elasticsearch/MongoDB/Redis/Terraform pattern. Permissive licensing on that surface gives that protection up.

A single license can't satisfy both pressures cleanly. This spec lands a tri-license structure that draws an explicit boundary between the two layers, plus a commercial grant for the segment that needs orchestrator-internal modifications without AGPL obligations.

This is a pre-v1 project with no production users and no existing copyright contributions to grandfather. The clean-slate condition makes the multi-license approach mechanically simple — every file gets its license declared once, in a single landing PR, with no historical contributors to chase.

## Goals

1. **Embedders can compose Rimsky into proprietary products without copyleft obligations.** The wire surface, executor SDK, reference stores, conformance suites, CLI, and reference deployment artifacts ship under a permissive OSI-approved license.
2. **The orchestrator is not trivially repackageable as a competing SaaS.** The scheduler, supervisor, and control-api ship under AGPL-3.0-or-later, which forces any hosted-service operator to publish their modifications under the same terms.
3. **A commercial path exists for organizations that need to run modified orchestrator code without AGPL §13 obligations.** Sold by Fall Guy Consulting under a separately-negotiated agreement.
4. **The license boundary is enforceable mechanically**, not just by convention. CI rejects PRs that import across the boundary in the wrong direction.
5. **Contributor onboarding is uncomfortable but standard.** Inbound terms are clear, automated, and don't require contributors to assign copyright.
6. **OSI legitimacy is preserved.** Both runtime licenses (AGPL-3.0-or-later, Apache 2.0) are OSI-approved; the project can call itself open source without qualification.

## Non-goals

1. **Hard prohibition of hosted Rimsky-as-a-Service.** AGPL deters but does not forbid. Operators can host a hosted Rimsky service as long as they publish patches under AGPL. If a hard prohibition becomes necessary, this spec is not the answer; a follow-up would relicense the orchestrator to BSL or FSL. Out of scope.
2. **Trademark policy.** Mentioned in §10 because trademark is the *real* defense against fork-and-rename; full policy is a separate filing-and-drafting exercise.
3. **Pricing, deal terms, and the commercial license text.** §9 sketches the shape; the actual contract is a lawyer-drafted artifact that lands when the first prospect surfaces.
4. **Retroactive contributor agreements.** There are no significant external contributors yet; the doc assumes a clean slate.
5. **Documentation licensing.** `docs/`, `cold-read/`, `CHANGELOG.md`, and `README.md` are addressed in §4 but a full Creative Commons / Apache split is not relitigated; they ship under Apache 2.0 with the rest of the embedder layer.

## 1. Decision

Three licenses, applied per-file:

1. **AGPL-3.0-or-later** — the orchestrator binaries and the rimsky-internal packages they depend on.
2. **Apache License 2.0** — the wire-surface IDL, the executor SDK, the reference store and executor binaries, the conformance suites, the CLI, the reference deployment, and documentation.
3. **Fall Guy Consulting Commercial License** — sold separately, releases the licensee from AGPL §5 (copyleft on distribution) and §13 (network-service source disclosure) for the orchestrator-internal code. Apache-licensed code is unaffected (already permissive).

The choice between Apache and MIT for the permissive layer is Apache. Rationale: explicit patent grant (MIT is silent), patent-retaliation clause, explicit inbound-contribution terms, explicit trademark non-grant, and the standardized NOTICE-file mechanism. All of these matter more for an infrastructure platform with many embedders than MIT's brevity advantage matters. Convention also leans Apache: Kubernetes, Airflow, Argo, Dagster, Prefect, Terraform (originally), and most of CNCF use Apache.

The choice between AGPL and a stricter source-available license (BSL, FSL, Elastic v2, SSPL) is AGPL. Rationale: the goal is to deter casual repackaging while keeping OSI legitimacy and a healthy contributor base; large enterprises whose legal departments refuse AGPL are the commercial-license target customer, not lost users. If casual SaaS repackaging materializes despite AGPL, this can be revisited; tightening from AGPL to BSL is mechanically straightforward (Fall Guy holds copyright via the CLA in §6) and has direct precedent (HashiCorp 2023).

## 2. Layer definitions

### 2.1 The orchestrator-internal layer (AGPL)

Code that implements the orchestration semantics: state machine, dispatch, frame resolution, lock acquisition, claim resolution, queue management, scheduler ticking, control-API endpoints, postgres state, attribute substitution, validation, canonical hashing, scenario harness, migrations.

This is the layer where the engineering value of Rimsky lives. A repackager wanting to offer hosted Rimsky-as-a-Service would either need to ship this layer unmodified under AGPL (publishing all their integrations as derivative works) or buy a commercial license.

### 2.2 The embedder layer (Apache 2.0)

Code that consumers must read, link against, fork, or implement against to integrate with Rimsky:

- **Wire IDL and generated bindings.** Anyone writing an executor or store implements against `proto/v1/*.proto`. AGPL on this would make every conforming executor a derivative work, which contradicts the project-agnostic thesis.
- **Executor SDK** — the TypeScript `@fallguy/claude-agent` npm package, the Go `executors/http-node/` and `executors/stub/` reference binaries. Authors fork these as starting points for their own executors.
- **Reference store binaries** — `stores/filesystem/`, `stores/postgres/`, `stores/stub/`. Operators run these as-is or fork them to build internal stores. They speak the Rimsky wire protocol but are independent processes.
- **CLI** (`rimsky-cli`, planned in `docs/specs/2026-05-02-rimsky-cli-and-compose-design.md`). Operator-facing; consumers script against it.
- **Conformance suites** (`rimsky-conformance`, `rimsky-store-conformance`, `rimsky-conformance-probe`). Authors run these against their own implementations to verify protocol conformance and fork them into their own CI.
- **Reference deployment artifacts** under `deploy/`. Forked freely.
- **Documentation, README, CHANGELOG, cold-read style guide.** Apache 2.0 covers documentation; this is conventional for the rest of the CNCF graph.
- **Shared interface-shape Go packages** that both the orchestrator and the standalone stores import: `core/store/` (the `Store` interface, shared types like `ClaimID` / `ClaimSpec` / `Capabilities`, the `RegionsByteEqual` and `ModeCoexists` helpers, the `rimsky_lock_holders` helpers, the `Registry`, the `remote/` gRPC client, the `storetest/` fake). These have no orchestration logic of their own; they declare the contract the orchestrator and stores share. The reference store binaries already import `core/store` (verified 2026-05-02), so this layer assignment is forced by the existing dependency graph and is also the right call on principle.
- **Pure-utility packages** with no orchestration logic: `core/shared/` (stdlib-only utilities), `core/canonical/` (RFC 8785 JCS hashing — also used by the CLI for client-side template hashing).

### 2.3 The commercial layer

Not a separate code layer; a separate *grant* on the AGPL-licensed code. A commercial licensee gets a non-exclusive license to use, modify, and distribute the orchestrator-internal code without AGPL §5 or §13 obligations. The Apache layer is unaffected (already permissive). See §9.

## 3. Why this works

The compatibility math:

- **Apache 2.0 → AGPL-3.0** is fine. AGPL-licensed code can freely link against Apache-licensed libraries; the combined work distributes under AGPL. Apache 2.0 is GPLv3-compatible by FSF's stance, and AGPL is a GPLv3 sibling.
- **AGPL → Apache 2.0 is forbidden.** An Apache-licensed package cannot import an AGPL-licensed package; doing so would force the importing file under AGPL, which contradicts its own license declaration. This is the boundary CI must enforce (§7).

Practically: every package in the embedder layer must depend only on (a) the standard library, (b) third-party permissively-licensed packages, and (c) other embedder-layer packages. The orchestrator layer is unrestricted in what it imports (it can pull from either layer freely).

The license boundary roughly mirrors the architectural import-direction rules already documented in `CLAUDE.md`: subsystem packages cannot cross-import each other, shared logic flows down into `shared/` / `node/` / `message/` / `queue/` / `storage/` / `store/`, and those lower layers don't import upward. The licensing rule adds a direction overlay: lower layers are Apache, upper layers are AGPL.

## 4. Boundary mapping (directory-by-directory)

The table below names every directory currently in the repo (as of 2026-05-02) and assigns a license. Where a directory is split, the split rule is given.

### 4.1 Apache 2.0

| Path | Notes |
|---|---|
| `proto/v1/*.proto`, `proto/v1/gen/` | Wire IDL and generated Go bindings. The contract every executor and store implements. |
| `core/shared/` | Stdlib-only utilities. No orchestration logic. |
| `core/canonical/` | RFC 8785 JCS hashing. Used by the CLI for client-side template hashing; needs to be embeddable. |
| `core/store/` (entire package) | `Store` interface, `ClaimID` / `ClaimSpec` / `Capabilities` / `ClaimResult` types, `RegionsByteEqual` and `ModeCoexists` helpers, `rimsky_lock_holders` helpers, `Registry`, `remote/` gRPC client, `storetest/` fake. The reference store binaries already depend on this package; it must be Apache for that import to be legal under §3. |
| `core/executor/` | The supervisor's outbound HTTP/gRPC executor client and resolver. Currently orchestrator-internal but interface-shaped; classifying as Apache lets out-of-tree adopters embed the client without an AGPL boundary (e.g. a custom orchestrator harness). Verify in §11 that no orchestration logic is leaking here; if it is, refactor it out before landing. |
| `core/cmd/rimsky-cli/` | Planned by the 2026-05-02 CLI spec. Operator-facing thin client; no orchestration logic. |
| `core/cmd/rimsky-conformance/`, `core/cmd/rimsky-store-conformance/`, `core/cmd/rimsky-conformance-probe/` | Conformance test suites. Authors fork these into their own CI. |
| `executors/claude-agent/` | TypeScript executor SDK. Published as an npm package; consumers compose it into their own executors. |
| `executors/http-node/` | Go HTTP-shaped reference executor. Forked as an example. |
| `executors/stub/` | Go test-fixture executor. Forked into authors' tests. |
| `stores/filesystem/`, `stores/postgres/`, `stores/stub/` | Reference store binaries. Independent processes; speak the wire protocol; importable as starting points for forks. |
| `stores/internal/` | Shared scaffolding for the reference stores; only depends on `proto/v1/gen`. |
| `deploy/` | Reference Docker compose, k8s helm chart, build scripts. Operators fork freely. |
| `docs/`, `cold-read/`, `README.md`, `CHANGELOG.md` | Documentation. Apache 2.0 (not Creative Commons) keeps it consistent with the codebase. |
| `Makefile`, `.golangci.yml`, top-level dotfiles | Build configuration. Trivially permissive. |

### 4.2 AGPL-3.0-or-later

| Path | Notes |
|---|---|
| `core/cmd/rimsky-scheduler/` | Scheduler binary entry point. |
| `core/cmd/rimsky-supervisor/` | Supervisor binary entry point. |
| `core/cmd/rimsky-control-api/` | Control-API binary entry point. |
| `core/cmd/rimsky-migrate/` | Migration runner binary entry point. |
| `core/scheduler/` | Scheduler subsystem: tick loop, schedule advancement, orphan reapers, named-lock sweeps. |
| `core/supervisor/` | Supervisor subsystem: dispatch claim, multi-lock acquisition, executor invocation, terminal application, auto-terminal claim resolution. |
| `core/controlapi/` | Control-API: HTTP routes, template registration, tag movement, instance lifecycle, store-lifecycle event RPCs. |
| `core/queue/` | Postgres-backed dispatch queue. |
| `core/storage/` | Postgres-backed state machinery. |
| `core/migrations/` | Migration runner + migration files for `rimsky_*` tables. |
| `core/frame/` | Frame resolution. |
| `core/node/` | Node state machine and pure logic. |
| `core/message/` | Cascade message types and handlers. |
| `core/attributes/` | Attribute schema, substitution, and validation. |
| `core/qualityrule/` | Quality rule evaluation. |
| `core/config/` | Library entry points (`StartScheduler`, `StartSupervisor`, `StartControlAPI`) and YAML config parsing for the orchestrator binaries. |
| `core/scenario/` | Scenario test harness (drives the orchestrator end-to-end against a real Postgres for tests). |
| `core/internal/` | Orchestrator-internal helpers (Postgres testcontainer fixture, etc.). |
| `core/doc.go` | Package doc for the orchestrator. |
| `test/scenarios/`, `test/smoke/` | Integration tests that exercise orchestrator internals. |

### 4.3 Notes on contested directories

- **`core/store/`**: classified Apache despite living at `core/`. Forced by the existing import graph (the reference stores depend on it) and consistent with its character (interface declarations, shared types, plumbing — not orchestration logic). If a future change adds orchestration logic to `core/store/`, that logic must be moved out (e.g. into `core/supervisor/`) before landing. A `// LICENSE: Apache-2.0` header on every file in the package is the visible reminder.
- **`core/executor/`**: classified Apache by extension of the same principle. Currently consumed only by the orchestrator, but interface-shaped. If verification in §11 shows orchestration logic has leaked in, refactor before landing.
- **`core/cmd/rimsky-cli/`**: planned for the 2026-05-02 CLI spec, not yet in the tree. The CLI spec already declares the CLI as a thin wire-surface client with no orchestration logic; that posture lines up with the Apache classification.
- **`core/canonical/`**: the JCS hash is referenced by the CLI for client-side template hashing (per the CLI spec §10) and by the orchestrator for content-addressed template IDs. Apache lets the CLI consume it without crossing the boundary.
- **`core/migrations/`**: AGPL because the migration files are inseparable from orchestrator schema. The runner *infrastructure* is fungible but not worth splitting.

## 5. File header conventions

Every source file gets a short two-or-three-line header at the very top.

### 5.1 Apache files

```go
// Copyright © 2026 Fall Guy Consulting.
// Licensed under the Apache License, Version 2.0. See LICENSE.apache at the
// repo root, or http://www.apache.org/licenses/LICENSE-2.0.
```

TypeScript:

```ts
// Copyright © 2026 Fall Guy Consulting.
// Licensed under the Apache License, Version 2.0.
// See LICENSE.apache at the repo root.
```

Proto:

```proto
// Copyright © 2026 Fall Guy Consulting.
// Licensed under the Apache License, Version 2.0.
```

### 5.2 AGPL files

```go
// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.
```

The "dual-licensed" wording on AGPL files is mandatory: it preserves the commercial-license track. A bare "Licensed under AGPL-3.0-or-later" header would foreclose Fall Guy's ability to grant a commercial license on that file.

### 5.3 Top-level files

| File | Purpose |
|---|---|
| `LICENSE.apache` | Full Apache 2.0 text. |
| `LICENSE.agpl` | Full AGPL-3.0 text. |
| `COPYRIGHT` | Project-level copyright statement, list of licenses, pointer to commercial-license contact. (See §5.4.) |
| `NOTICE` | Apache §4(d) attribution file. Lists copyright holders and any third-party attributions. |
| `CONTRIBUTING.md` | Contribution process, including CLA pointer (§6). |
| `CLA.md` | Contributor License Agreement text. |
| `TRADEMARKS.md` | Trademark usage policy (sketch in §10). |

### 5.4 `COPYRIGHT` text (proposed)

```
Rimsky is Copyright © 2026 Fall Guy Consulting.

Rimsky is multi-licensed:

  1. Source files in the embedder layer are licensed under the Apache License,
     Version 2.0 (see LICENSE.apache). The embedder layer is identified
     per-file by an "Apache License, Version 2.0" header and is documented in
     docs/specs/2026-05-02-licensing-design.md §4.1.

  2. Source files in the orchestrator layer are dual-licensed:

       (a) under the GNU Affero General Public License v3.0 or later (see
           LICENSE.agpl). This is the default; no action is required to use
           Rimsky under AGPL terms.

       (b) under a Fall Guy Consulting commercial license, available to
           organizations that prefer to use, modify, or distribute the
           orchestrator without AGPL §5 (copyleft) or §13 (network-service
           source disclosure) obligations. Contact
           licensing@fallguyconsulting.com.

     The orchestrator layer is identified per-file by a "Dual-licensed under
     AGPL-3.0-or-later or a Fall Guy Consulting commercial license" header
     and is documented in docs/specs/2026-05-02-licensing-design.md §4.2.

Contributions are accepted under the terms of the Contributor License
Agreement at CLA.md, which grants Fall Guy Consulting the rights necessary
to maintain the multi-license structure.

"Rimsky" and the Rimsky logo are trademarks of Fall Guy Consulting. See
TRADEMARKS.md for the trademark usage policy.
```

## 6. Contributor License Agreement

Dual licensing requires Fall Guy to be able to grant the commercial license on contributed code. There are two viable mechanical shapes:

1. **Inbound=outbound + relicensing grant.** Contributors keep their copyright but grant Fall Guy a perpetual, irrevocable, sublicensable right to relicense their contribution under any terms. Used by Grafana, Sentry, GitLab. Contributor experience: lighter; signature is one page.
2. **Copyright assignment.** Contributors transfer copyright to Fall Guy. Used by FSF projects, MongoDB. Contributor experience: heavier; many open-source contributors refuse on principle.

**Decision: shape (1).**

### 6.1 CLA mechanics

- **One-time signature.** Per-contributor, not per-PR. Signed via `cla-assistant.io` (the standard GitHub bot). Bot blocks PR merges from contributors who haven't signed.
- **DCO sign-off.** Every commit carries `Signed-off-by:` per the [Developer Certificate of Origin](https://developercertificate.org/). DCO is enforced by a separate bot (`probot/dco`).
- **CLA text.** Adapted from the [Apache ICLA](https://www.apache.org/licenses/icla.pdf) with an added relicensing-grant clause. About one page. Lands as `CLA.md` at repo root with a one-line summary in `CONTRIBUTING.md`.
- **Versioning.** The CLA text is versioned. If it changes, contributors are re-prompted to sign the new version on their next PR. No retroactive signature drift.

### 6.2 Pre-existing contributions

There are no significant external contributions before this lands. If any surface during the audit (§11), the contributor is contacted; if they're unreachable or refuse, the affected code is removed and rewritten. This is mechanical because the project is small.

## 7. Boundary enforcement

The license boundary is not enforceable by convention alone. PRs add new imports; reviewers miss things; contributors can innocently break the rule.

### 7.1 CI lint: `make license-lint`

A new check that walks every Go file, classifies it by directory (against the §4 mapping), and verifies that:

- Every Apache-classified Go file's imports of `github.com/fallguy/rimsky/...` resolve to other Apache-classified packages.
- Every AGPL-classified Go file's header declares dual-licensing (catches drift where someone copies a file and forgets the header).
- Every Apache-classified Go file's header declares Apache 2.0.
- Every TypeScript file in `executors/claude-agent/` has the Apache header.

Implementation: a small Go program at `core/cmd/rimsky-license-check/` (also Apache, since it's tooling) that reads `licensing.yml` (the §4 directory-to-license map, lifted from this doc) and walks the tree.

The check runs in CI on every PR. Failing the check blocks merge.

### 7.2 The directory-to-license map

A canonical YAML at `licensing.yml` at the repo root, generated from §4. Single source of truth for the lint and for the file headers. Any change to the map requires a doc update (this spec) plus a corresponding bulk-header update.

### 7.3 What CI does *not* check

- The TypeScript executor doesn't have a Go-style import graph; the check trusts the per-file headers there.
- Generated files (`proto/v1/gen/`) carry the Apache header but are regenerated by `make proto-gen`; the protoc plugin needs a header-stamping configuration.
- The license of third-party Go modules in `go.mod` is not checked here; that's a separate audit.

## 8. Trademark

Trademark is the actual defense against fork-and-call-it-Rimsky-Plus. License protects copyright; trademark protects the project name. WordPress's Mullenweg/WP Engine fight was fundamentally a trademark dispute, not a license dispute.

### 8.1 What lands in this spec

- A `TRADEMARKS.md` at repo root, documenting acceptable use:
  - Forks may not be called "Rimsky" or "Rimsky-*". Acceptable: "FooEngine, based on Rimsky."
  - The Rimsky logo (when one exists) may not be used by forks or by third-party services.
  - "Rimsky-compatible" is allowed if the fork passes the conformance suite.
- A pointer in `COPYRIGHT` (§5.4) to `TRADEMARKS.md`.

### 8.2 What does *not* land in this spec

- USPTO trademark filing for "Rimsky" — separate process, requires a lawyer, costs money. Recommended as a follow-up but out of scope here.
- A Rimsky logo. None exists yet.

## 9. Commercial license shape

The commercial license is a separately-negotiated contract between Fall Guy Consulting and a customer. Not drafted in this spec; sketched here so future drafting has a starting point.

### 9.1 Grant

Non-exclusive, non-transferable right to use, modify, distribute, and offer the orchestrator layer (the AGPL-licensed packages) as a service, *without* the obligations of AGPL §5 (copyleft on distribution of derivative works) or §13 (network-service source disclosure).

### 9.2 Scope dimensions

Pick one or compose several:
- Per-deployment / per-cluster.
- Per-instance (count of running Rimsky control-planes).
- Revenue-share above a threshold (HashiCorp BSL-style, repurposed for commercial).
- Unlimited / enterprise-wide.

Per-deployment-with-support is the default in the infrastructure-software space.

### 9.3 Term

Annual, with renewal. Perpetual licenses simplify enterprise procurement but break the recurring-revenue model.

### 9.4 Warranty / indemnity

Standard:
- Liability cap at fees paid in the prior 12 months.
- IP indemnity (Fall Guy defends licensee against patent-infringement claims arising from Rimsky).

### 9.5 Audit

Light-touch right to verify deployment count. Standard; rarely exercised.

### 9.6 What's *not* in the commercial license

- Support, SLAs, custom features, professional services. These are separately-priced agreements. Keeps the license clean.

### 9.7 Reference templates

When the first prospect surfaces, the lawyer-drafted contract should reference:
- MariaDB BSL commercial agreement (public).
- Sentry commercial license (public).
- Grafana Labs commercial terms (public).

These three cover the relevant patterns; a license attorney should bless the final draft.

## 10. Operational surface

Things outside the codebase that need to exist for the model to work:

| Surface | Action |
|---|---|
| `licensing@fallguyconsulting.com` mailbox | Create. Routes to whoever handles licensing inquiries. |
| Licensing landing page | One page on fallguyconsulting.com. Three paragraphs: what AGPL means for users, when to consider commercial, contact. Defer building a self-serve portal until volume justifies it. |
| `cla-assistant.io` GitHub integration | Configure on the Rimsky repo. Block merges without signatures. |
| `probot/dco` GitHub integration | Configure on the Rimsky repo. Enforce DCO sign-off. |
| Trademark filing | Out of scope here; recommended follow-up. |
| License lawyer engagement | Identify and onboard a license/IP attorney before the first commercial-license prospect surfaces. Used for: drafting the commercial license, reviewing the CLA, advising on trademark filing. |

## 11. Migration from current state

This is a clean-slate landing — no existing license to relicense, no contributor backlog. The order:

1. **Verify §4 boundary mapping against the current import graph.** Run `grep -rh "fallguy/rimsky" <Apache-classified-paths>/` and confirm every cross-module import resolves to another Apache-classified path. Most of this was verified during this spec's drafting; a final pass before landing catches drift.
2. **Refactor any orchestration logic out of `core/executor/` and `core/store/` if it's leaked there.** Both were sampled during drafting and look interface-shaped; a closer review before landing confirms.
3. **Land `LICENSE.apache`, `LICENSE.agpl`, `COPYRIGHT`, `NOTICE`, `TRADEMARKS.md`, `CLA.md`, `CONTRIBUTING.md`, `licensing.yml`.** Single PR. Ordering doesn't matter within the PR.
4. **Bulk-add file headers.** A scripted pass that reads `licensing.yml` and prepends the appropriate header to every source file. Run it; commit the result.
5. **Land `core/cmd/rimsky-license-check/` and wire it into CI.** Subsequent PRs are gated on it.
6. **Configure cla-assistant and probot/dco on the GitHub repo.** Subsequent PRs are gated on these.
7. **Update `README.md` and add `docs/licensing.md`** (operator-facing FAQ — see §12 for the scope).
8. **Add a `CHANGELOG.md` entry under `## Unreleased`** noting the licensing decision and pointing at this spec.
9. **Defer the commercial license draft until the first prospect.** Identify a license attorney now to avoid scrambling later.

Steps 1–8 land in a single dev session. Step 9 is durative.

## 12. `docs/licensing.md` — operator FAQ

Lands alongside the license files. Single page. Sections:

- "Can I run Rimsky internally without disclosing source?" — Yes. AGPL §13 only triggers on offering Rimsky as a network service to *third parties*. Internal use within a single legal entity is unaffected.
- "Can I offer Rimsky as a hosted service to my customers under AGPL?" — Yes, but you must publish your modifications and let users download corresponding source. The commercial license removes that obligation.
- "I'm a SaaS company embedding Rimsky inside my product. Do I need a commercial license?" — If your product is offered over a network and includes a modified orchestrator, AGPL §13 reaches it. Commercial license is the clean answer; otherwise you publish your modifications.
- "Can I link my proprietary code to unmodified Rimsky?" — AGPL is copyleft; FSF's reading is that linking creates a derivative work. The combined work has to be AGPL-compatible. Commercial license sidesteps the question.
- "What about my executor / store / CLI extensions?" — Those are Apache-licensed. No copyleft. Build whatever you want; you owe nothing back.
- "Can I write a closed-source executor that talks to Rimsky?" — Yes. Executors are independent processes that speak the wire protocol. The protocol is Apache-licensed. No license relationship between the executor and the orchestrator beyond the wire.
- "Can I fork Rimsky and offer hosted Rimsky-as-a-Service?" — Under AGPL, yes — provided you publish your modifications under AGPL. We discourage it but do not forbid it. Trademark policy (`TRADEMARKS.md`) prohibits calling the fork "Rimsky."

## 13. Tradeoffs and known concerns

- **AGPL friction at large enterprises.** Many enterprise legal departments have a blanket "no AGPL anywhere" policy. The commercial-license track is the answer for those organizations; the friction is a feature, not a bug, but it does slow adoption-by-default. Acceptable: large-enterprise users buy commercial; small users and individual operators run AGPL.
- **CLA friction.** A subset of open-source contributors refuses CLAs on principle. The dual-license model fundamentally requires copyright control via CLA; there's no workaround. Acceptable: lose some contributions; the project is small enough that this isn't existential. The DCO + inbound=outbound + relicensing-grant shape is the lightest-weight CLA structure available.
- **Boundary leakage risk.** A future contribution adds orchestration logic to `core/store/` or `core/executor/`, the lint doesn't catch it (the lint checks imports, not semantics), and now Apache code does meaningful orchestration work. Mitigation: PR review, plus the durable design rule that "if it does orchestration, it's AGPL." Not perfect; acceptable.
- **CLA bot drift.** If the CLA text changes substantively, all contributors re-sign. If a contributor disappears between text versions, their merged code remains under the old version (which is fine). If a contributor disappears before signing the *first* version and we need to ship their merged work, we have a problem — but PR merging is gated on signature, so this can only happen via mistake.
- **AGPL doesn't actually prevent SaaS repackaging.** Stated in §1 / non-goals. AGPL deters but doesn't forbid; a hoster who's willing to publish their patches under AGPL can host modified Rimsky as a service. If this happens at scale, the project relicenses the orchestrator to BSL or FSL. Optionality is preserved.
- **Apache's patent grant doesn't extend to AGPL files.** A contributor who provides an Apache-licensed contribution grants a patent license under Apache §3. A contributor who provides an AGPL-licensed contribution grants a patent license under GPLv3 §11. Both contain patent-retaliation clauses; the wording differs slightly. This is fine — the protections compose — but worth noting.
- **License-header maintenance.** Every new file needs the right header. Mitigated by the lint and by template-generation in editors / `gopls`. Acceptable ongoing cost.
- **`core/canonical/` dual usage.** Used by both the CLI (Apache) and the orchestrator (AGPL). Apache classification is correct (the AGPL orchestrator can import Apache freely). If a future change adds AGPL-only logic to `core/canonical/`, it must move to a new AGPL package and `core/canonical/` stays interface-shaped.
- **Helm chart staleness.** Per `CLAUDE.md`, the Helm chart in `deploy/kubernetes/rimsky-chart/` is known stale. License headers should be added during the staleness fix, not separately.

## 14. Out of scope / deferred

- **OSI / SPDX submission.** SPDX identifiers (`SPDX-License-Identifier: AGPL-3.0-or-later`, `SPDX-License-Identifier: Apache-2.0`) on every file is a nice-to-have. Add in a follow-up if needed for tooling compatibility (some scanners want SPDX, not full headers).
- **Source-available follow-ups.** Relicensing the orchestrator to BSL or FSL if AGPL deterrence proves insufficient. Out of scope here; a separate spec when and if the trigger fires.
- **Logo and brand guidelines.** No Rimsky logo exists yet. Trademark policy in `TRADEMARKS.md` references it as a placeholder.
- **Pricing and packaging of the commercial license.** §9.2's scope dimensions sketch the surface but don't decide. Decide when the first prospect surfaces.
- **Translation of license text.** AGPL and Apache are English-canonical. No translations.
- **Patent strategy beyond the license-level grants.** No defensive patent pool, no patent filings.
- **Audit of third-party Go module licenses in `go.mod`.** Should be done before v1 ships; out of scope here.
- **Audit of `executors/claude-agent/` npm dependencies.** Same.
- **Documentation under a Creative Commons license.** Considered; rejected for consistency. Apache 2.0 covers documentation adequately and matches the rest of the codebase.

## 15. Summary of decisions

| # | Decision | Rationale |
|---|---|---|
| 1 | Tri-license: AGPL-3.0-or-later (orchestrator) + Apache 2.0 (embedder layer) + commercial grant | Embedder reach + anti-repackaging deterrent + revenue path for AGPL-averse enterprises |
| 2 | Apache 2.0 (not MIT) for the permissive layer | Patent grant, retaliation clause, contribution terms, trademark non-grant; CNCF-standard |
| 3 | AGPL (not BSL/FSL/SSPL/Elastic v2) for the orchestrator | OSI legitimacy + healthy contributor base; commercial license absorbs AGPL-averse enterprises; tighten later if needed |
| 4 | `core/store/` and `core/executor/` classified Apache despite living under `core/` | Interface-shaped, not orchestration logic; existing imports from `stores/*` already require Apache |
| 5 | `core/canonical/` and `core/shared/` Apache | Pure utility, shared with CLI and embedders |
| 6 | Per-file headers with explicit "dual-licensed" wording on AGPL files | Preserves the commercial-license track |
| 7 | Inbound=outbound + relicensing-grant CLA shape, signed via cla-assistant.io | Lightest CLA structure that supports dual licensing |
| 8 | DCO sign-off enforced via probot/dco | Standard, low-friction provenance |
| 9 | CI lint (`make license-lint` / `core/cmd/rimsky-license-check/`) walks the import graph and rejects Apache→AGPL imports | Boundary is mechanical, not aspirational |
| 10 | `licensing.yml` is the single source of truth for the directory-to-license map | One file drives lint, file headers, and this doc |
| 11 | Trademark `TRADEMARKS.md` lands now; USPTO filing deferred | Trademark is the real fork-and-rename defense; full filing is a follow-up |
| 12 | Commercial license contract drafted on first prospect, not pre-emptively | License lawyer engaged early; contract drafted on demand |
| 13 | `docs/licensing.md` operator FAQ lands with the license files | Pre-empts common operator questions |
| 14 | Single-PR landing: license files + headers + lint + CLA bot configuration | Clean-slate condition makes this mechanically simple |
| 15 | Pre-v1 break-freely posture means no compatibility shim for downstream consumers | Per `.claude/rules/rules.md`; consumers re-pick licenses on next pull |
