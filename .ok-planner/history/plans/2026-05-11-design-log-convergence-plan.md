# Design Log Convergence — Implementation Plan

**Spec:** `.ok-planner/specs/2026-05-11-design-log-convergence.md`
**Goal:** Land 13 design-log resolutions (concept catalog convergence) plus one paired code refactor (`abandonOpenedClaim` helper) in one run, then move all resolved tensions to `_resolved/` and regenerate `concepts.md`.
**Architecture:** Pure design-log mutations (concept files + tension lifecycle) for 12 of 13 resolutions; one new file in `foundation/integration/` for the helper, with two call-site updates and a paired doc-language sweep.
**Tech Stack:** Go 1.22+ (foundation module), Markdown for the design log.

---

## Background the implementer needs

The project keeps a durable design log at `.ok-planner/design/` with three subdirectories:

- `.ok-planner/design/concepts/<slug>.md` — load-bearing nouns the project traffics in. **Mutable**: editing Definition / Purpose / Boundaries / Invariants in place is the normal mode. The "Notes" or "Open within this concept" tail is treated as append-only.
- `.ok-planner/design/tensions/<slug>.md` — catalog of muddy / unspecified / conflicting bits. Tensions move through `open → resolving (spec: <slug>) → resolved`. Resolved tensions move to `.ok-planner/design/tensions/_resolved/<slug>.md` with `status: resolved` and a `resolution:` block summarizing the outcome.
- `.ok-planner/design/concepts.md` — auto-generated TOC; regenerated at the end of this plan.

13 tensions in `.ok-planner/design/tensions/` currently carry `status: resolving` and `spec: 2026-05-11-design-log-convergence`. Each task below resolves one or more of them by mutating the affected concept file(s) and moving the tension file to `_resolved/`. The 14th tension (`events-table-name-overlap.md`, status `open`) is superseded as a side effect of Task 7 and moves to `_resolved/` there.

**`@concept:` annotation discipline.** The repo's design log uses inline `@concept: <slug>` annotations at load-bearing code sites. Where this plan adds, slims, or splits a concept, the task notes call out which annotations to leave at the touched code sites. Untouched code stays bare.

**Atomicity.** Task 1 (code refactor + paired doc sweep) is one atomic change-set: the helper and the two concept-doc wordings must land together. The other tasks are independent and can be executed in any order; the plan presents them in the order from the spec's "Order of operations" section for predictability.

---

## Task 1: Code refactor — `abandonOpenedClaim` helper + paired doc sweep

Atomic single change-set. Resolves `abandon-on-pass-duplicated-path` (and folds the doc-language inconsistency identified in the tension's Additional context section).

**Spec section:** §4 "Code refactor: `abandon-already-opened-claim` helper".
**Concept boundaries respected:** `concepts/terminal-resolution.md`, `concepts/auto-terminal.md`, `concepts/claim-handle.md`, `concepts/claim-producer.md`. Helper preserves `@blessed-invariant 4` (claimant-guarded release: the helper does NOT delete `rimsky_claim_handle` rows; callers own the delete) and `@blessed-invariant 20` (claim content inert: scope/address pass opaque).

**Files:**
- NEW: `foundation/integration/abandon_claim.go`
- NEW: `foundation/integration/abandon_claim_test.go`
- EDIT: `foundation/integration/runner_lifecycle.go`
- EDIT: `foundation/integration/terminal_decision.go`
- EDIT: `.ok-planner/design/concepts/auto-terminal.md`
- EDIT: `.ok-planner/design/concepts/terminal-resolution.md`
- EDIT: `.ok-planner/design/tensions/abandon-on-pass-duplicated-path.md`
- MOVE: `.ok-planner/design/tensions/abandon-on-pass-duplicated-path.md` → `.ok-planner/design/tensions/_resolved/abandon-on-pass-duplicated-path.md`

**Steps:**

1. Create `foundation/integration/abandon_claim.go` with the following exact content (license header + package + helper). The helper has narrow scope: it builds `claim_id` from `claim_handle_id` and calls `producer.Abandon`. It does NOT touch `rimsky_claim_handle` rows — that is contextual to the caller (post-dispatch caller owns the claimant-guarded delete in its tx; pre-dispatch caller has no row to delete because the acquisition tx rolled back). Add `@concept: terminal-resolution` annotation in the file-level doc comment because this helper is part of the spine the `terminal-resolution` concept describes:

   ```go
   // Copyright © 2026 Fall Guy Consulting.
   // Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
   // license. See LICENSE.agpl and COPYRIGHT at the repo root.

   // Shared helper for Producer.Abandon on already-Open'd claims.
   //
   // @concept: terminal-resolution
   //
   // Two sites need to call Producer.Abandon on a claim whose Open already
   // succeeded:
   //
   //   1. The post-dispatch unified terminal-decision engine
   //      (ResolveClaimHandleTerminal in terminal_decision.go), Abandon
   //      branch. Runs inside a caller-provided tx and is followed by a
   //      claimant-guarded ClaimHandles.Delete.
   //
   //   2. The pre-dispatch on_acquire_unavailable carve-out
   //      (abandonPartialLocks in runner_lifecycle.go). Runs post-rollback;
   //      the rimsky_claim_handle rows have already been removed by the
   //      acquisition tx rollback, so no delete is needed.
   //
   // abandonOpenedClaim centralizes the producer-Abandon call so the
   // two sites share a single audited site for any future audit emit
   // or telemetry. It does NOT delete the rimsky_claim_handle row —
   // see the two callers for the delete semantics that apply at each
   // site.
   //
   // Preserves @blessed-invariant 4 (claimant-guarded release): the
   // helper never touches the row; callers do.
   //
   // Preserves @blessed-invariant 20 (claim content inert): scope and
   // address pass through opaque; the helper does not log, format with
   // %v, validate, transform, or otherwise act on them.

   package integration

   import (
   	"context"

   	"github.com/fallguy/rimsky/foundation/locks"
   	"github.com/fallguy/rimsky/modeling/shared"
   )

   // abandonOpenedClaim fires Producer.Abandon on a claim whose Open
   // already succeeded. claim_id is built from the claim_handle_id so
   // the producer can correlate state across verbs.
   func abandonOpenedClaim(
   	ctx context.Context,
   	producer locks.ClaimProducer,
   	claimHandleID shared.UUID,
   	scope, address []byte,
   ) error {
   	claimID := locks.ClaimID(claimHandleID.String())
   	return producer.Abandon(ctx, claimID, scope, address)
   }
   ```

2. Verify the helper compiles:
   ```sh
   go build ./foundation/integration/...
   ```
   Expected: clean build, no errors.

3. Edit `foundation/integration/runner_lifecycle.go::abandonPartialLocks` (the function at lines 64-80 of the current file). Replace the direct `lk.Store.Abandon(ctx, claimID, scope, address)` call with `abandonOpenedClaim(ctx, lk.Store, lk.ClaimHandleID, scope, address)`. The surrounding warn-on-failure behavior stays. The `claimScope(lk)` / `claimAddress(lk)` / `claimID := locks.ClaimID(lk.ClaimHandleID.String())` setup lines should be simplified: scope and address are still computed locally, but `claimID` no longer needs to be constructed in this function (the helper builds it from `lk.ClaimHandleID`).

   Final function shape:
   ```go
   // abandonPartialLocks calls Abandon on every already-Open'd ClaimSpec
   // in the partial-acquired list. Mirrors handleOrphanedClaim's release
   // branch (the tx-side rollback already removed the lock-holder rows).
   //
   // @concept: terminal-resolution
   func abandonPartialLocks(ctx context.Context, args RunArgs, partial []AcquiredLock) {
   	for _, lk := range partial {
   		if lk.Store == nil {
   			continue
   		}
   		scope := claimScope(lk)
   		address := claimAddress(lk)
   		if err := abandonOpenedClaim(ctx, lk.Store, lk.ClaimHandleID, scope, address); err != nil {
   			args.Logger.Warn("handleAcquireUnavailable: Abandon failed",
   				"store", storeNameForSpec(lk.Spec), "error", err.Error())
   		}
   	}
   }
   ```

   Add `@concept: terminal-resolution` annotation in the function doc comment because this function is part of the terminal-resolution spine (it's the pre-dispatch carve-out).

4. Edit `foundation/integration/terminal_decision.go::ResolveClaimHandleTerminal` (the function at lines 110-135 of the current file). The `claimID := locks.ClaimID(td.ClaimHandleID.String())` construction at line 117 must remain because the Commit branch (line 121) uses it. Only the Abandon branch at line 123 changes.

   Current code (around line 117-127):
   ```go
   claimID := locks.ClaimID(td.ClaimHandleID.String())
   var verbErr error
   switch td.Outcome {
   case AggregateCommit:
   	verbErr = td.Producer.Commit(ctx, claimID, td.Scope, td.Address)
   case AggregateAbandon:
   	verbErr = td.Producer.Abandon(ctx, claimID, td.Scope, td.Address)
   default:
   	return fmt.Errorf("ResolveClaimHandleTerminal: unknown outcome %v", td.Outcome)
   }
   ```

   Replace ONLY the `AggregateAbandon` case body:
   ```go
   case AggregateAbandon:
   	verbErr = abandonOpenedClaim(ctx, td.Producer, td.ClaimHandleID, td.Scope, td.Address)
   ```

   The `claimID` local is still used by the Commit branch and stays.

5. Verify the package builds and existing tests still pass:
   ```sh
   go build ./foundation/integration/...
   go test ./foundation/integration/... -count=1
   ```
   Expected: clean build; all existing tests pass with no edits required. If any existing test fails, treat it as a regression to investigate before proceeding (the helper extraction is supposed to be behavior-preserving).

6. Create `foundation/integration/abandon_claim_test.go` with a small unit test verifying that the helper forwards args correctly to the producer's `Abandon`. Use a minimal in-memory test double for `locks.ClaimProducer`. Two table cases: success path returns `nil`; producer-Abandon-error is returned unchanged.

   Use this exact content:

   ```go
   // Copyright © 2026 Fall Guy Consulting.
   // Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
   // license. See LICENSE.agpl and COPYRIGHT at the repo root.

   package integration

   import (
   	"context"
   	"errors"
   	"testing"

   	"github.com/fallguy/rimsky/foundation/locks"
   	"github.com/google/uuid"
   )

   // abandonStub is a minimal locks.ClaimProducer test double that
   // records the most-recent Abandon call and returns a preset error.
   // ClaimProducer has six methods (Name + Capabilities startup-handshake
   // plus the four runtime verbs Open / Commit / Abandon / Release); we
   // implement all six so the stub satisfies the interface, but only
   // Abandon is exercised.
   type abandonStub struct {
   	lastClaimID locks.ClaimID
   	lastScope   []byte
   	lastAddress []byte
   	abandonErr  error
   }

   func (s *abandonStub) Name() string { return "abandon-stub" }

   func (s *abandonStub) Capabilities(context.Context) (locks.Capabilities, error) {
   	return locks.Capabilities{}, errors.New("Capabilities not implemented in stub")
   }

   func (s *abandonStub) Open(context.Context, locks.ClaimID, locks.ClaimSpec) (locks.OpenOutcome, error) {
   	return locks.OpenOutcome{}, errors.New("Open not implemented in stub")
   }

   func (s *abandonStub) Commit(context.Context, locks.ClaimID, []byte, []byte) error {
   	return errors.New("Commit not implemented in stub")
   }

   func (s *abandonStub) Abandon(_ context.Context, claimID locks.ClaimID, scope, address []byte) error {
   	s.lastClaimID = claimID
   	s.lastScope = scope
   	s.lastAddress = address
   	return s.abandonErr
   }

   func (s *abandonStub) Release(context.Context, locks.ClaimID, []byte, []byte) error {
   	return errors.New("Release not implemented in stub")
   }

   func TestAbandonOpenedClaim(t *testing.T) {
   	t.Parallel()

   	handleID := uuid.MustParse("00000000-0000-4000-8000-000000000001")
   	scope := []byte(`{"path":"/tmp/x"}`)
   	address := []byte(`{"version":"abc"}`)

   	t.Run("forwards args to producer.Abandon", func(t *testing.T) {
   		stub := &abandonStub{}
   		err := abandonOpenedClaim(context.Background(), stub, handleID, scope, address)
   		if err != nil {
   			t.Fatalf("expected nil, got %v", err)
   		}
   		want := locks.ClaimID(handleID.String())
   		if stub.lastClaimID != want {
   			t.Errorf("claim_id = %q, want %q", stub.lastClaimID, want)
   		}
   		if string(stub.lastScope) != string(scope) {
   			t.Errorf("scope = %q, want %q", stub.lastScope, scope)
   		}
   		if string(stub.lastAddress) != string(address) {
   			t.Errorf("address = %q, want %q", stub.lastAddress, address)
   		}
   	})

   	t.Run("returns producer.Abandon error", func(t *testing.T) {
   		want := errors.New("producer go boom")
   		stub := &abandonStub{abandonErr: want}
   		err := abandonOpenedClaim(context.Background(), stub, handleID, scope, address)
   		if !errors.Is(err, want) {
   			t.Errorf("err = %v, want %v", err, want)
   		}
   	})
   }
   ```

   Notes on the stub:
   - `locks.ClaimProducer` is a type alias for `claimproducer.ClaimProducer` defined at `foundation/locks/interface.go:58`. The interface has **six** methods: `Name() string`, `Capabilities`, `Open`, `Commit`, `Abandon`, `Release`. The "4 verbs + Capabilities() handshake" prose framing covers five of them; `Name()` is the sixth (a rimsky-side identifier, not on the wire). All six must be implemented for the stub to satisfy the interface.
   - `Open` returns `locks.OpenOutcome` (defined at `protocols/claimproducer/types.go:113`), not `ClaimResult`. The stub returns the zero value plus an error; it is never called by the helper.
   - `shared.UUID` is a type alias for `uuid.UUID` (`modeling/shared/types.go:14`). Use `uuid.MustParse(...)` from `github.com/google/uuid` to construct test UUIDs. The helper accepts `shared.UUID` (which is `uuid.UUID`), so the value typechecks directly without conversion.
   - If, at execute-plan time, the interface signature has drifted from what is described above (the spec was authored 2026-05-11), read `foundation/locks/interface.go` and `protocols/claimproducer/claimproducer.go` for the current truth and adjust the stub accordingly.

7. Run the new test alone to verify it passes:
   ```sh
   go test ./foundation/integration/ -run TestAbandonOpenedClaim -count=1 -v
   ```
   Expected: PASS for both subtests.

8. Run the entire integration package tests again with race detection:
   ```sh
   go test ./foundation/integration/... -race -count=1
   ```
   Expected: all tests pass cleanly.

9. Edit `.ok-planner/design/concepts/auto-terminal.md` — reword Invariant 5 (line 31 of the current file) to qualify the carve-out. Current text:

   > Unified `ResolveClaimHandleTerminal` is also the entry point for orphan-reaper bail paths and error-policy `pass`/`error` resolutions on already-Open'd claims.

   Replace with this exact text:

   > Unified `ResolveClaimHandleTerminal` is the audited post-dispatch entry point for orphan-reaper bail paths and error-policy `pass`/`error` resolutions on already-Open'd claims. The pre-dispatch `OnAcquireUnavailable` `pass`/`error` carve-out routes through the shared `abandonOpenedClaim` helper (`foundation/integration/abandon_claim.go`) instead — the `rimsky_claim_handle` rows are already gone (rolled back by the acquisition tx) by the time it fires, so the unified engine's delete step has nothing to do.

10. Edit `.ok-planner/design/concepts/terminal-resolution.md` — reword the `OnAcquireUnavailable` paragraph (the paragraph after the five-stage list, around line 26 of the current file: "The `OnAcquireUnavailable` handler is the upstream sibling..."). Current text:

    > The `OnAcquireUnavailable` handler is the upstream sibling (`runner_lifecycle.go::handleAcquireUnavailable`): it runs *before* dispatch when `tryAcquire` returns the `errAcquireUnavailable` sentinel, and on `pass`/`error` it Abandons already-Open'd claims by direct producer call rather than routing through `releaseLocksInTx`.

    Replace with this exact text:

    > The `OnAcquireUnavailable` handler is the upstream sibling (`runner_lifecycle.go::handleAcquireUnavailable`): it runs *before* dispatch when `tryAcquire` returns the `errAcquireUnavailable` sentinel, and on `pass`/`error` it Abandons already-Open'd claims via the shared `abandonOpenedClaim` helper (`foundation/integration/abandon_claim.go`). The carve-out exists because the acquisition tx has already rolled back by this point — the `rimsky_claim_handle` rows are gone, so there is no claimant-guarded delete to fold into the unified engine. Post-dispatch terminal paths (`OnExecutorBlocked`/`OnExecutorErrored` `pass`) route through `releaseLocksInTx` → `ResolveClaimHandleTerminal`, which now calls the same helper for its Abandon branch (and adds the claimant-guarded `rimsky_claim_handle` delete after the verb).

    Also update the "Terminal kind → producer verb" table row for `OnAcquireUnavailable` pass/error — the "Active-claim verb" cell currently reads `Abandon (direct producer call)`. Change to `Abandon (via abandonOpenedClaim helper)`.

11. Edit `.ok-planner/design/tensions/abandon-on-pass-duplicated-path.md` — reword the "What is muddy" body so it correctly identifies the genuinely duplicated path (pre-dispatch only) per the Additional context section that was added during refine-design intake. Specifically, the current "But there are two pre-dispatch / handler-pass siblings that *don't*:" enumerated list incorrectly lists `applyTerminalPass` as a second site that bypasses `ResolveClaimHandleTerminal`. In the actual code, `applyTerminalPass` calls `releaseLocksInTx(success=false)` which DOES route through `ResolveClaimHandleTerminal` for both held and non-held branches. The genuinely duplicated path was ONLY the pre-dispatch `handleAcquireUnavailable.abandonPartialLocks` site, and after this task lands, both sites route through the shared helper. Reword "What is muddy" to read (replace the entire section):

    > Before this resolution: the pre-dispatch path (`handleAcquireUnavailable.abandonPartialLocks` in `runner_lifecycle.go`) called `producer.Abandon` directly, while the post-dispatch path (`ResolveClaimHandleTerminal` in `terminal_decision.go`) was framed as "the single audited site for producer-verb fire + claim-handle delete." That framing was almost-true — `ResolveClaimHandleTerminal` was indeed the single site that fired the post-dispatch verb and the claimant-guarded `rimsky_claim_handle` delete — but it gave the misleading impression that *every* `producer.Abandon` on an already-Open'd claim ran through that engine. The pre-dispatch carve-out did not. If a future change added telemetry, a metric, or an audit-event emit at the unified-engine site, the pre-dispatch path would silently miss it.
    >
    > This tension is resolved by `2026-05-11-design-log-convergence`'s extraction of the shared `abandonOpenedClaim` helper in `foundation/integration/abandon_claim.go`. Both sites now call the same helper for the producer-Abandon step. The post-dispatch site continues to own the claimant-guarded delete in its caller-provided tx; the pre-dispatch site has no row to delete (the acquisition tx rolled back).

12. Add the `resolution:` block to the tension frontmatter and move the file. First, edit the frontmatter to look like:

    ```yaml
    ---
    tension: abandon-on-pass-duplicated-path
    category: muddy-boundary
    status: resolved
    spec: 2026-05-11-design-log-convergence
    affects:
      - terminal-resolution
      - auto-terminal
      - lifecycle-handler
    resolution:
      shape: extract-shared-helper
      helper: foundation/integration/abandon_claim.go::abandonOpenedClaim
      doc-sweep:
        - concepts/auto-terminal.md (Invariant 5 reworded)
        - concepts/terminal-resolution.md (OnAcquireUnavailable paragraph + kind→verb table reworded)
      summary: |
        Extracted a narrow shared helper centralizing producer.Abandon on
        already-Open'd claims. Both the pre-dispatch carve-out
        (handleAcquireUnavailable.abandonPartialLocks) and the post-dispatch
        unified-engine Abandon branch (ResolveClaimHandleTerminal) now call
        the same helper. The doc-language inconsistency between
        auto-terminal.md and terminal-resolution.md is reconciled in the
        same change.
    ---
    ```

    Then move the file:
    ```sh
    mkdir -p .ok-planner/design/tensions/_resolved
    mv .ok-planner/design/tensions/abandon-on-pass-duplicated-path.md .ok-planner/design/tensions/_resolved/abandon-on-pass-duplicated-path.md
    ```

**Verification:**

```sh
# Build clean
go build ./foundation/integration/...

# All integration tests pass with no edits
go test ./foundation/integration/... -count=1 -race

# Verify the helper file exists
test -f foundation/integration/abandon_claim.go && echo OK || echo MISSING

# Verify tension moved
test -f .ok-planner/design/tensions/_resolved/abandon-on-pass-duplicated-path.md && echo OK || echo MISSING
test ! -f .ok-planner/design/tensions/abandon-on-pass-duplicated-path.md && echo OK || echo STILL_THERE

# Verify both call sites use the helper
grep -n abandonOpenedClaim foundation/integration/runner_lifecycle.go foundation/integration/terminal_decision.go
# Expected: at least 2 grep hits (one per file).
```

All expected to be OK.

---

## Task 2: NEW concept `transition-reason.md` + update `node-state.md` + resolve tension

Resolves `transition-reason-missing-concept`.

**Spec section:** §3a NEW concepts, `transition-reason.md` verbatim draft.

**Files:**
- NEW: `.ok-planner/design/concepts/transition-reason.md`
- EDIT: `.ok-planner/design/concepts/node-state.md`
- MOVE: `.ok-planner/design/tensions/transition-reason-missing-concept.md` → `_resolved/`

**Steps:**

1. Create `.ok-planner/design/concepts/transition-reason.md` with the exact verbatim content from the spec's §3a NEW concepts → `concepts/transition-reason.md` block (the code block in `2026-05-11-design-log-convergence.md` between the lines `#### concepts/transition-reason.md` and the next `####` heading). Copy the content inside that code block (excluding the wrapping ```markdown fence) verbatim into the new file.

2. Edit `.ok-planner/design/concepts/node-state.md`:
   - In the "Boundaries" section (around line 24), the sentence reading `does NOT own: cascade-firing decisions (those live on `last-outcome`), audit metadata (those live in `transition-reason`), claim disposition (those live on the claim handle row).` is already correct — verify it remains.
   - In the "Adjacent:" list at the end of Boundaries (around line 24), confirm `transition-reason` is included. If not present, add it. Final Adjacent list should be: `last-outcome, transition-reason, frame, parked-state, cascade`.

3. Move the tension. First edit the frontmatter to `status: resolved` and add a `resolution:` block:

   ```yaml
   ---
   tension: transition-reason-missing-concept
   category: unspecified
   status: resolved
   spec: 2026-05-11-design-log-convergence
   affects:
     - node-state
     - last-outcome
     - cascade
     - event-log
   resolution:
     shape: promote-new-concept
     new-concept: concepts/transition-reason.md
     summary: |
       Promoted transition-reason to a concept with Definition, Purpose,
       Boundaries (owns the audit enum + write site at state transitions),
       Invariants (ReasonHandlerError dead-end sentinel; exhaustive
       enumeration), Adjacent (node-state, last-outcome, cascade, event-log).
       Node-state cross-references updated. The relationship-tension
       transition-reason-vs-last-outcome.md remains open by design.
   ---
   ```

   Then:
   ```sh
   mv .ok-planner/design/tensions/transition-reason-missing-concept.md .ok-planner/design/tensions/_resolved/transition-reason-missing-concept.md
   ```

**Verification:**

```sh
test -f .ok-planner/design/concepts/transition-reason.md && echo OK || echo MISSING
test -f .ok-planner/design/tensions/_resolved/transition-reason-missing-concept.md && echo OK || echo MISSING
test ! -f .ok-planner/design/tensions/transition-reason-missing-concept.md && echo OK || echo STILL_THERE
grep -q "transition-reason" .ok-planner/design/concepts/node-state.md && echo OK || echo MISSING_REF
```

---

## Task 3: NEW concept `on-event-handler.md` + update three Adjacent blocks + resolve tension

Resolves `on-event-handler-missing-concept` (also closes three dangling Adjacent slugs across `lifecycle-handler.md`, `named-event.md`, `node.md`).

**Spec section:** §3a NEW concepts, `on-event-handler.md` verbatim draft.

**Files:**
- NEW: `.ok-planner/design/concepts/on-event-handler.md`
- EDIT: `.ok-planner/design/concepts/lifecycle-handler.md`
- EDIT: `.ok-planner/design/concepts/named-event.md`
- EDIT: `.ok-planner/design/concepts/node.md`
- MOVE: `.ok-planner/design/tensions/on-event-handler-missing-concept.md` → `_resolved/`

**Steps:**

1. Create `.ok-planner/design/concepts/on-event-handler.md` with the verbatim content from the spec's §3a `concepts/on-event-handler.md` block.

2. Edit `.ok-planner/design/concepts/lifecycle-handler.md`:
   - In the "Adjacent:" list, replace `on-event-handler` (which was a dangling slug pointing at nothing) with `on-event-handler` (now points at the new concept file). The slug stays the same; the cross-link is now valid. No prose change needed if the reference is already by-slug. Verify `grep -n "on-event-handler" .ok-planner/design/concepts/lifecycle-handler.md` returns at least one line.

3. Edit `.ok-planner/design/concepts/named-event.md`:
   - In the "Adjacent:" list, same verification as step 2.

4. Edit `.ok-planner/design/concepts/node.md`:
   - In the "Adjacent:" list, same verification as step 2.

5. Move the tension. Edit frontmatter to `status: resolved` and add a `resolution:` block:

   ```yaml
   resolution:
     shape: promote-new-concept
     new-concept: concepts/on-event-handler.md
     summary: |
       Promoted on-event-handler to a concept with Definition (key-indexed
       map of {event_name → handler}), Purpose, Boundaries, Invariants
       (capabilities cross-check at template registration; unknown
       event names as no-ops if peer unreachable). Three dangling
       Adjacent slugs in lifecycle-handler.md, named-event.md, node.md
       are now valid cross-links to the new concept.
   ---
   ```

   Then:
   ```sh
   mv .ok-planner/design/tensions/on-event-handler-missing-concept.md .ok-planner/design/tensions/_resolved/
   ```

**Verification:**

```sh
test -f .ok-planner/design/concepts/on-event-handler.md && echo OK || echo MISSING
test -f .ok-planner/design/tensions/_resolved/on-event-handler-missing-concept.md && echo OK || echo MISSING
test ! -f .ok-planner/design/tensions/on-event-handler-missing-concept.md && echo OK || echo STILL_THERE
# All three citing concepts still reference on-event-handler:
grep -l "on-event-handler" .ok-planner/design/concepts/lifecycle-handler.md \
  .ok-planner/design/concepts/named-event.md \
  .ok-planner/design/concepts/node.md
# Expected: all three files listed
```

---

## Task 4: NEW concept `cascade-graph.md`

Part of resolving `observability-split-cascade-graph-and-discovery-cache` (the full tension resolves in Task 6 when `observability.md` is slimmed; this task just creates the new concept file).

**Spec section:** §3a `concepts/cascade-graph.md` verbatim draft.

**Files:**
- NEW: `.ok-planner/design/concepts/cascade-graph.md`

**Steps:**

1. Create `.ok-planner/design/concepts/cascade-graph.md` with the verbatim content from the spec's §3a `concepts/cascade-graph.md` block.

**Verification:**

```sh
test -f .ok-planner/design/concepts/cascade-graph.md && echo OK || echo MISSING
grep -q "operator-dashboard HTTP-route backplane" .ok-planner/design/concepts/cascade-graph.md && echo OK || echo MISSING_CONTENT
```

---

## Task 5: NEW concept `discovery-cache.md`

Part of resolving `observability-split-cascade-graph-and-discovery-cache`.

**Spec section:** §3a `concepts/discovery-cache.md` verbatim draft.

**Files:**
- NEW: `.ok-planner/design/concepts/discovery-cache.md`

**Steps:**

1. Create `.ok-planner/design/concepts/discovery-cache.md` with the verbatim content from the spec's §3a `concepts/discovery-cache.md` block.

**Verification:**

```sh
test -f .ok-planner/design/concepts/discovery-cache.md && echo OK || echo MISSING
grep -q "in-memory per-peer" .ok-planner/design/concepts/discovery-cache.md && echo OK || echo MISSING_CONTENT
```

---

## Task 6: SLIM `observability.md` + resolve observability-split tension

Resolves `observability-split-cascade-graph-and-discovery-cache` (the new concepts were created in Tasks 4 + 5; this task slims the source).

**Spec section:** §3b SLIM concepts, `concepts/observability.md (post-slim)` verbatim rewrite.

**Files:**
- EDIT: `.ok-planner/design/concepts/observability.md` (rewrite entirely)
- MOVE: `.ok-planner/design/tensions/observability-split-cascade-graph-and-discovery-cache.md` → `_resolved/`

**Steps:**

1. Rewrite `.ok-planner/design/concepts/observability.md` with the verbatim content from the spec's §3b `concepts/observability.md (post-slim)` block. The new file is shorter than the current one; replace the entire body (everything after the frontmatter `---` separator can be replaced; the frontmatter itself stays but `references:` should be updated to match the spec's draft — only `_discover/2026-05-10-observability-optional-protocols.md` remains).

2. Move the tension. Edit frontmatter to `status: resolved` and add resolution block:

   ```yaml
   resolution:
     shape: split-into-three
     new-concepts:
       - concepts/cascade-graph.md (operator-dashboard backplane)
       - concepts/discovery-cache.md (per-peer Capabilities cache)
     slimmed: concepts/observability.md (now covers only peer protocols + handshake + userdata_schema)
     summary: |
       Split observability into three sharper concepts. cascade-graph
       owns the operator-dashboard HTTP routes; discovery-cache owns
       the in-memory per-peer Capabilities cache; observability owns
       the peer-facing optional protocols and the handshake that
       populates discovery-cache, plus userdata_schema validation.
   ---
   ```

   Then:
   ```sh
   mv .ok-planner/design/tensions/observability-split-cascade-graph-and-discovery-cache.md .ok-planner/design/tensions/_resolved/
   ```

**Verification:**

```sh
test -f .ok-planner/design/tensions/_resolved/observability-split-cascade-graph-and-discovery-cache.md && echo OK || echo MISSING
test ! -f .ok-planner/design/tensions/observability-split-cascade-graph-and-discovery-cache.md && echo OK || echo STILL_THERE
# observability.md no longer mentions cascade-graph routes directly:
! grep -q '/observability/\*' .ok-planner/design/concepts/observability.md && echo OK || echo NOT_SLIMMED
# observability.md cross-links the two new concepts:
grep -q "discovery-cache" .ok-planner/design/concepts/observability.md && echo OK || echo MISSING_DC_REF
grep -q "cascade-graph" .ok-planner/design/concepts/observability.md && echo OK || echo MISSING_CG_REF
```

---

## Task 7: SLIM `event-log.md` to audit-log-only + add `named-event.md` ledger subsection + resolve two tensions

Resolves `event-log-split-into-two` and supersedes `events-table-name-overlap`.

**Spec section:** §3b SLIM concepts, `concepts/event-log.md (post-slim to audit-log-only)` verbatim rewrite.

**Files:**
- EDIT: `.ok-planner/design/concepts/event-log.md` (rewrite to audit-log-only; keep filename)
- EDIT: `.ok-planner/design/concepts/named-event.md` (append "Ledger storage" subsection)
- MOVE: `.ok-planner/design/tensions/event-log-split-into-two.md` → `_resolved/`
- MOVE: `.ok-planner/design/tensions/events-table-name-overlap.md` → `_resolved/`

**Steps:**

1. Rewrite `.ok-planner/design/concepts/event-log.md` with the verbatim content from the spec's §3b `concepts/event-log.md (post-slim to audit-log-only)` block. Replace the entire body; update the `aliases:` frontmatter to `audit log` + `rimsky_events table`; update `references:` to only `_discover/2026-05-10-event-log-append-only-jsonb.md`.

2. Append a "Ledger storage" subsection to `.ok-planner/design/concepts/named-event.md`. The subsection should document the `rimsky_node_events` ledger material moved out of `event-log.md`. Insert it BEFORE the "## Aliases and historical names" section (or wherever the named-event.md structure suggests it fits naturally). Use this exact text:

   ```markdown
   ## Ledger storage

   The persisted form of named events is `rimsky_node_events`, an append-only ledger with columns `emitter_node_type`, `event_name`, `payload_inline` / `payload_handle` / `payload_handle_backend`, `seq`. Payloads can be spilled via the configured `BlobBackend` (per `persistence.blob.backend` ∈ {`inline` | `pg-largeobject` | `filesystem` | `memory`}). Read by attribute substitution `{{nodes.<emitter>.event.<name>.<path>}}` and by `on_event` handlers (see `on-event-handler`).

   Opacity discipline (`@blessed-invariant 21`): the payload bytes are inert in rimsky — read only via `walkPath` substitution and the persistence-layer fetch on event consumption. Never logged, formatted with `%v`, validated beyond schema gates, transformed, attached to traces, or included in error messages.

   Most-recent emission of `(emitter, event_name)` wins at substitution time. No built-in retention; operator-managed.
   ```

3. Move both tensions. First the primary one — add resolution block to `event-log-split-into-two.md`:

   ```yaml
   resolution:
     shape: slim-source-and-fold-ledger
     slimmed: concepts/event-log.md (audit-log-only; filename retained)
     folded-into: concepts/named-event.md (Ledger storage subsection)
     supersedes: events-table-name-overlap
     summary: |
       Folded the rimsky_node_events named-event ledger into
       concepts/named-event.md as a "Ledger storage" subsection. Slimmed
       concepts/event-log.md to cover only the rimsky_events audit log
       (filename and slug retained per refine-design step 5, option C).
       events-table-name-overlap is automatically resolved by this split.
   ---
   ```

   Then add a resolution block to `events-table-name-overlap.md` (the superseded tension):

   ```yaml
   resolution:
     shape: superseded
     superseded-by: event-log-split-into-two
     summary: |
       The two-tables-under-one-noun overlap is resolved by splitting
       event-log into an audit-log-only concept and folding the named-event
       ledger material into concepts/named-event.md. See event-log-split-into-two
       in _resolved/ for the picked shape and outcome.
   ---
   ```

   (Update the `status:` line in `events-table-name-overlap.md`'s frontmatter to `resolved` as part of this edit; the current frontmatter has `status: open`.)

   Move both:
   ```sh
   mv .ok-planner/design/tensions/event-log-split-into-two.md .ok-planner/design/tensions/_resolved/
   mv .ok-planner/design/tensions/events-table-name-overlap.md .ok-planner/design/tensions/_resolved/
   ```

**Verification:**

```sh
test -f .ok-planner/design/tensions/_resolved/event-log-split-into-two.md && echo OK || echo MISSING_1
test -f .ok-planner/design/tensions/_resolved/events-table-name-overlap.md && echo OK || echo MISSING_2
test ! -f .ok-planner/design/tensions/event-log-split-into-two.md && echo OK || echo STILL_THERE_1
test ! -f .ok-planner/design/tensions/events-table-name-overlap.md && echo OK || echo STILL_THERE_2
# event-log.md no longer covers named-event ledger:
! grep -q "rimsky_node_events" .ok-planner/design/concepts/event-log.md && echo OK || echo STILL_HAS_LEDGER
# named-event.md now has the Ledger storage section:
grep -q "## Ledger storage" .ok-planner/design/concepts/named-event.md && echo OK || echo MISSING_SUBSECTION
```

---

## Task 8: DROP `licensing-boundary.md` → fold into `module-layout.md` + resolve tension

Resolves `licensing-boundary-fold-candidate`.

**Spec section:** §3c DROP concepts → "Drop `concepts/licensing-boundary.md` → fold into `concepts/module-layout.md`".

**Files:**
- EDIT: `.ok-planner/design/concepts/module-layout.md` (append "Licensing boundary" subsection)
- DELETE: `.ok-planner/design/concepts/licensing-boundary.md`
- EDIT: any concept file containing `Adjacent: licensing-boundary` (replace with `module-layout` or drop)
- MOVE: `.ok-planner/design/tensions/licensing-boundary-fold-candidate.md` → `_resolved/`

**Steps:**

1. Append the "## Licensing boundary" subsection to `.ok-planner/design/concepts/module-layout.md`. Use the verbatim text from the spec's §3c "Drop `concepts/licensing-boundary.md`" fold-destination block.

2. Delete the standalone file:
   ```sh
   rm .ok-planner/design/concepts/licensing-boundary.md
   ```

3. Find any other concept file that mentions `licensing-boundary` in its Adjacent list and update:
   ```sh
   grep -rln "licensing-boundary" .ok-planner/design/concepts/
   ```
   For each file found, replace `licensing-boundary` with `module-layout` in the Adjacent block (or drop if `module-layout` is already in the list). Likely candidates: `module-layout.md` itself may have a self-reference that should be removed; `quality-rule.md` references the Apache/AGPL split in passing.

4. Add resolution block to the tension and move:

   ```yaml
   resolution:
     shape: fold-into-module-layout
     dropped: concepts/licensing-boundary.md
     folded-into: concepts/module-layout.md (Licensing boundary subsection)
     summary: |
       Folded licensing-boundary into module-layout as a final subsection.
       Repo-organization concern, not a runtime noun; module-layout already
       cited it as Adjacent. Standalone file dropped.
   ---
   ```

   ```sh
   mv .ok-planner/design/tensions/licensing-boundary-fold-candidate.md .ok-planner/design/tensions/_resolved/
   ```

**Verification:**

```sh
test ! -f .ok-planner/design/concepts/licensing-boundary.md && echo OK || echo STILL_THERE
grep -q "## Licensing boundary" .ok-planner/design/concepts/module-layout.md && echo OK || echo MISSING_SUBSECTION
# No surviving Adjacent: licensing-boundary refs:
! grep -rln "Adjacent: licensing-boundary" .ok-planner/design/concepts/ && echo OK || echo DANGLING_REF
test -f .ok-planner/design/tensions/_resolved/licensing-boundary-fold-candidate.md && echo OK || echo TENSION_MISSING
```

---

## Task 9: DROP `mcp-server.md` → fold into `control-api.md` + resolve tension

Resolves `mcp-server-fold-into-control-api`.

**Spec section:** §3c "Drop `concepts/mcp-server.md` → fold into `concepts/control-api.md`".

**Files:**
- EDIT: `.ok-planner/design/concepts/control-api.md` (append "Agentic MCP shim" subsection)
- DELETE: `.ok-planner/design/concepts/mcp-server.md`
- EDIT: `.ok-planner/design/concepts/executor.md` (optionally add Adjacent note for dual-MCP-role; only if natural)
- EDIT: any other concept file containing `mcp-server` (replace with `control-api` or drop)
- MOVE: `.ok-planner/design/tensions/mcp-server-fold-into-control-api.md` → `_resolved/`

**Steps:**

1. Append the "## Agentic MCP shim" subsection to `.ok-planner/design/concepts/control-api.md`. Use the verbatim text from the spec's §3c "Drop `concepts/mcp-server.md`" fold-destination block (the markdown block starting with `## Agentic MCP shim`).

2. Delete the standalone file:
   ```sh
   rm .ok-planner/design/concepts/mcp-server.md
   ```

3. Update `concepts/executor.md` Adjacent block. Currently it references `mcp-server` in some way (verify with grep first). If the reference is part of the dual-MCP-role note (claude-agent's internal MCP server is a different surface), reword to point at `control-api` and the new Agentic MCP shim subsection. If the reference is just the slug in Adjacent, replace with `control-api`.

4. Search for any other Adjacent / cross-link references to `mcp-server`:
   ```sh
   grep -rln "mcp-server" .ok-planner/design/concepts/
   ```
   For each remaining hit, replace with `control-api` or drop.

5. Add resolution block and move tension:

   ```yaml
   resolution:
     shape: fold-into-control-api
     dropped: concepts/mcp-server.md
     folded-into: concepts/control-api.md (Agentic MCP shim subsection)
     summary: |
       Folded mcp-server into control-api as the "Agentic MCP shim"
       subsection. Strict pass-through frontend, no business logic — not
       a standalone noun. Dual-MCP-role distinction (claude-agent's
       per-run internal MCP server vs. operator control-plane shim)
       noted inside the new subsection.
   ---
   ```

   ```sh
   mv .ok-planner/design/tensions/mcp-server-fold-into-control-api.md .ok-planner/design/tensions/_resolved/
   ```

**Verification:**

```sh
test ! -f .ok-planner/design/concepts/mcp-server.md && echo OK || echo STILL_THERE
grep -q "## Agentic MCP shim" .ok-planner/design/concepts/control-api.md && echo OK || echo MISSING_SUBSECTION
! grep -rln "Adjacent: mcp-server" .ok-planner/design/concepts/ && echo OK || echo DANGLING_REF
test -f .ok-planner/design/tensions/_resolved/mcp-server-fold-into-control-api.md && echo OK || echo TENSION_MISSING
```

---

## Task 10: DROP `scenario-harness.md` + update `conformance.md` Adjacent + resolve tension

Resolves `scenario-harness-drop-from-catalog`.

**Spec section:** §3c "Drop `concepts/scenario-harness.md` (no fold)".

**Files:**
- DELETE: `.ok-planner/design/concepts/scenario-harness.md`
- EDIT: `.ok-planner/design/concepts/conformance.md` (Adjacent block — remove or reword `scenario-harness` reference)
- EDIT: any other concept file containing `scenario-harness`
- MOVE: `.ok-planner/design/tensions/scenario-harness-drop-from-catalog.md` → `_resolved/`

**Steps:**

1. Delete the standalone file:
   ```sh
   rm .ok-planner/design/concepts/scenario-harness.md
   ```

2. Edit `.ok-planner/design/concepts/conformance.md`:
   - In the "Boundaries" section, find the sentence `Does NOT own: ... scenario harness (see `scenario-harness`), ...` and reword to remove the `scenario-harness` slug. Suggested rewording: `Does NOT own: in-repo *_test.go unit tests (those live with the source), the in-repo scenario harness under modeling/scenario.Start (documented in CLAUDE.md "Build & test"), ...`
   - In the "Adjacent:" list, drop `scenario-harness`.

3. Search for any other references:
   ```sh
   grep -rln "scenario-harness" .ok-planner/design/concepts/
   ```
   For each remaining hit, reword inline (the in-repo harness is documented in CLAUDE.md "Build & test"; the slug is no longer a concept) or drop.

4. Add resolution block and move tension:

   ```yaml
   resolution:
     shape: drop-from-catalog
     dropped: concepts/scenario-harness.md
     no-fold: true
     summary: |
       Dropped scenario-harness as a standalone concept. It is in-repo
       test scaffolding (modeling/scenario/harness.go), not a runtime
       noun. The harness usage is documented in CLAUDE.md "Build & test"
       and test/scenarios/ is grep-discoverable. Conformance.md Adjacent
       reworded to remove the dangling slug.
   ---
   ```

   ```sh
   mv .ok-planner/design/tensions/scenario-harness-drop-from-catalog.md .ok-planner/design/tensions/_resolved/
   ```

**Verification:**

```sh
test ! -f .ok-planner/design/concepts/scenario-harness.md && echo OK || echo STILL_THERE
! grep -rln "scenario-harness" .ok-planner/design/concepts/ && echo OK || echo DANGLING_REF
test -f .ok-planner/design/tensions/_resolved/scenario-harness-drop-from-catalog.md && echo OK || echo TENSION_MISSING
```

---

## Task 11: DROP `userdata-overrides.md` → fold into `userdata.md` + resolve tension

Resolves `userdata-overrides-fold-into-userdata`.

**Spec section:** §3c "Drop `concepts/userdata-overrides.md` → fold into `concepts/userdata.md`".

**Files:**
- EDIT: `.ok-planner/design/concepts/userdata.md` (append "Per-instance overrides" subsection)
- DELETE: `.ok-planner/design/concepts/userdata-overrides.md`
- EDIT: any concept file containing `userdata-overrides` references
- MOVE: `.ok-planner/design/tensions/userdata-overrides-fold-into-userdata.md` → `_resolved/`

**Steps:**

1. Append the "## Per-instance overrides" subsection to `.ok-planner/design/concepts/userdata.md` using the verbatim text from the spec's §3c "Drop `concepts/userdata-overrides.md`" fold-destination block.

2. Delete the standalone file:
   ```sh
   rm .ok-planner/design/concepts/userdata-overrides.md
   ```

3. Search for any other references:
   ```sh
   grep -rln "userdata-overrides" .ok-planner/design/concepts/
   ```
   For each remaining hit, replace with `userdata` (the fold destination) or drop.

4. Add resolution block and move tension:

   ```yaml
   resolution:
     shape: fold-into-userdata
     dropped: concepts/userdata-overrides.md
     folded-into: concepts/userdata.md (Per-instance overrides subsection)
     summary: |
       Folded userdata-overrides into userdata as a "Per-instance
       overrides" subsection. The override mechanism has no independent
       existence (exists only to mutate userdata); userdata's Boundaries
       already claimed the merge mechanism. The subsection covers
       routing-key shape, merge order, validation discipline preserving
       @blessed-invariant 11, platform-extensions provenance.
   ---
   ```

   ```sh
   mv .ok-planner/design/tensions/userdata-overrides-fold-into-userdata.md .ok-planner/design/tensions/_resolved/
   ```

**Verification:**

```sh
test ! -f .ok-planner/design/concepts/userdata-overrides.md && echo OK || echo STILL_THERE
grep -q "## Per-instance overrides" .ok-planner/design/concepts/userdata.md && echo OK || echo MISSING_SUBSECTION
! grep -rln "userdata-overrides" .ok-planner/design/concepts/ && echo OK || echo DANGLING_REF
test -f .ok-planner/design/tensions/_resolved/userdata-overrides-fold-into-userdata.md && echo OK || echo TENSION_MISSING
```

---

## Task 12: EDIT `error-policy.md` — drop `frame-stuck` from Adjacent + resolve tension

Resolves `frame-stuck-dangling-adjacent`.

**Spec section:** §3d EDIT concepts table — `concepts/error-policy.md` row.

**Files:**
- EDIT: `.ok-planner/design/concepts/error-policy.md`
- MOVE: `.ok-planner/design/tensions/frame-stuck-dangling-adjacent.md` → `_resolved/`

**Steps:**

1. Edit `.ok-planner/design/concepts/error-policy.md`. Find the `Adjacent:` block (typically near the end of the Boundaries section). Remove the `frame-stuck` slug. If the surrounding prose says something like "...see frame-stuck for the no-progress observation" or similar, reword to "...see `frame` for the frame_timeout / `frame.stuck.observed` no-progress observation."

2. Add resolution block and move:

   ```yaml
   resolution:
     shape: reword-adjacent
     summary: |
       Dropped frame-stuck from error-policy.md Adjacent (the slug pointed
       at no concept file). The mechanism it referred to is the
       frame.stuck.observed slog warning, which lives in concepts/frame.md
       as part of the advisory frame_timeout mechanism. Prose updated to
       point at frame directly.
   ---
   ```

   ```sh
   mv .ok-planner/design/tensions/frame-stuck-dangling-adjacent.md .ok-planner/design/tensions/_resolved/
   ```

**Verification:**

```sh
! grep -q "frame-stuck" .ok-planner/design/concepts/error-policy.md && echo OK || echo STILL_DANGLING
test -f .ok-planner/design/tensions/_resolved/frame-stuck-dangling-adjacent.md && echo OK || echo TENSION_MISSING
```

---

## Task 13: EDIT `lifecycle-handler.md` — strip `claimant-guarded` backticks + resolve tension

Resolves `claimant-guarded-backtick-noun`.

**Spec section:** §3d EDIT concepts table — `concepts/lifecycle-handler.md` row (the backtick-strip entry).

**Files:**
- EDIT: `.ok-planner/design/concepts/lifecycle-handler.md`
- MOVE: `.ok-planner/design/tensions/claimant-guarded-backtick-noun.md` → `_resolved/`

**Steps:**

1. Edit `.ok-planner/design/concepts/lifecycle-handler.md`. Find the string `` `claimant-guarded` `` (backticked). It appears in a phrase like "claim release (see `auto-terminal`, `claimant-guarded`)". Reword to one of:
   - Recommended: `claim release (see `auto-terminal`; the claimant-guarded release discipline per @blessed-invariant 4 governs every rimsky_claim_handle delete and worker-request claimed_by null)`
   - Or shorter: `claim release (see `auto-terminal`; the release discipline is claimant-guarded per @blessed-invariant 4)`

   The key change: strip the backticks around `claimant-guarded` because it is not a concept slug — it is an invariant pattern documented at `@blessed-invariant 4`. Use plain text for invariant phrases.

2. Add resolution block and move:

   ```yaml
   resolution:
     shape: strip-backticks
     summary: |
       Stripped backticks around `claimant-guarded` in lifecycle-handler.md.
       Claimant-guarded is an invariant pattern documented at
       @blessed-invariant 4, not a concept slug. Typographic convention:
       backticks reserved for concept slugs and code identifiers, not
       invariant-phrase shorthand.
   ---
   ```

   ```sh
   mv .ok-planner/design/tensions/claimant-guarded-backtick-noun.md .ok-planner/design/tensions/_resolved/
   ```

**Verification:**

```sh
! grep -q '`claimant-guarded`' .ok-planner/design/concepts/lifecycle-handler.md && echo OK || echo STILL_BACKTICKED
grep -q "claimant-guarded" .ok-planner/design/concepts/lifecycle-handler.md && echo OK || echo PHRASE_REMOVED_TOO_AGGRESSIVELY
test -f .ok-planner/design/tensions/_resolved/claimant-guarded-backtick-noun.md && echo OK || echo TENSION_MISSING
```

---

## Task 14: EDIT `claim-producer.md` — unify "4 verbs + Capabilities()" framing + resolve tension

Resolves `claim-producer-method-count-framing`.

**Spec section:** §3d EDIT concepts table — `concepts/claim-producer.md` row.

**Files:**
- EDIT: `.ok-planner/design/concepts/claim-producer.md` (BOTH "What it is" section AND Invariants block)
- MOVE: `.ok-planner/design/tensions/claim-producer-method-count-framing.md` → `_resolved/`

**Steps:**

1. Edit `.ok-planner/design/concepts/claim-producer.md`:

   - In the "What it is" section (around line 19), find: `5 methods: `Open`, `Commit`, `Abandon`, `Release`, `Capabilities`)`. Replace with: `4 verbs (`Open` / `Commit` / `Abandon` / `Release`) plus the `Capabilities()` startup handshake`. Adjust surrounding sentence as needed for grammatical flow.

   - In the "Invariants" section (around line 31), find: `The five-method protocol plus `Capabilities()` startup handshake is the only contract.` Replace with: `The 4-verb protocol (`Open` / `Commit` / `Abandon` / `Release`) plus the `Capabilities()` startup handshake is the only contract.`

   Both edits required — the unification doesn't work if only one is done.

2. Add resolution block and move:

   ```yaml
   resolution:
     shape: unify-on-4-verbs-plus-capabilities
     summary: |
       Unified claim-producer.md on "4 verbs (Open / Commit / Abandon /
       Release) plus the Capabilities() startup handshake" framing in
       both the "What it is" section (which previously listed
       Capabilities as a 5th method) and the Invariants block (which
       previously said "five-method protocol plus Capabilities()
       handshake," double-counting Capabilities). Matches CLAUDE.md
       framing.
   ---
   ```

   ```sh
   mv .ok-planner/design/tensions/claim-producer-method-count-framing.md .ok-planner/design/tensions/_resolved/
   ```

**Verification:**

```sh
! grep -q "5 methods" .ok-planner/design/concepts/claim-producer.md && echo OK || echo STILL_HAS_5_METHODS
! grep -q "five-method" .ok-planner/design/concepts/claim-producer.md && echo OK || echo STILL_HAS_FIVE_METHOD
grep -q "4 verbs" .ok-planner/design/concepts/claim-producer.md && echo OK || echo MISSING_NEW_FRAMING
test -f .ok-planner/design/tensions/_resolved/claim-producer-method-count-framing.md && echo OK || echo TENSION_MISSING
```

---

## Task 15: EDIT `claim.md` + `claim-handle.md` — add layer annotations + resolve tension

Resolves `claim-vs-claim-handle-layer-annotation`.

**Spec section:** §3d EDIT concepts table — `concepts/claim.md` and `concepts/claim-handle.md` rows.

**Files:**
- EDIT: `.ok-planner/design/concepts/claim.md`
- EDIT: `.ok-planner/design/concepts/claim-handle.md`
- MOVE: `.ok-planner/design/tensions/claim-vs-claim-handle-layer-annotation.md` → `_resolved/`

**Steps:**

1. Edit `.ok-planner/design/concepts/claim.md`. At the very top of the "What it is" section (or as a new first paragraph), insert the following exact text:

   > `claim` is the protocol-layer noun returned by `ClaimProducer.Open`; `claim-handle` is the rimsky-persistence-layer noun for the same conceptual thing. They have different invariants by layer — `@blessed-invariant 20` (claim content inert) gates content; `@blessed-invariant 4` (claimant-guarded release) gates the persistence row.

2. Edit `.ok-planner/design/concepts/claim-handle.md`. At the very top of the "What it is" section (or as a new first paragraph), insert the same exact text (mirrored — the annotation is identical, surfaced from each side).

3. Add resolution block and move:

   ```yaml
   resolution:
     shape: add-layer-annotation
     summary: |
       Added one-line layer annotation at the top of both claim.md and
       claim-handle.md, naming the protocol-layer vs rimsky-persistence-layer
       split explicitly. Each concept's own Boundaries already implied the
       split; the annotation makes it the first thing a reader sees.
   ---
   ```

   ```sh
   mv .ok-planner/design/tensions/claim-vs-claim-handle-layer-annotation.md .ok-planner/design/tensions/_resolved/
   ```

**Verification:**

```sh
grep -q "protocol-layer noun" .ok-planner/design/concepts/claim.md && echo OK || echo MISSING_CLAIM
grep -q "protocol-layer noun" .ok-planner/design/concepts/claim-handle.md && echo OK || echo MISSING_HANDLE
test -f .ok-planner/design/tensions/_resolved/claim-vs-claim-handle-layer-annotation.md && echo OK || echo TENSION_MISSING
```

---

## Task 16: CLAUDE.md sweep — "4 verbs + Capabilities()" framing + dropped-concept references

**Spec section:** §5 "CLAUDE.md updates".

**Files:**
- EDIT: `CLAUDE.md` (repo root)

**Steps:**

1. Search CLAUDE.md for any surviving "5 methods" / "five-method" / "5-method" / "five methods" framing of the ClaimProducer protocol:
   ```sh
   grep -nE "5 methods|five[- ]method|five methods" CLAUDE.md
   ```
   For each hit, reword to "4 verbs (`Open` / `Commit` / `Abandon` / `Release`) plus the `Capabilities()` startup handshake" (or equivalent — the canonical short form is "4 verbs + Capabilities() startup handshake").

   Note: CLAUDE.md "What this repo is" section already uses the "4 verbs" framing at line ~39 of the current CLAUDE.md — that one is correct. Only fix any sites that use the "5 methods" double-count framing.

2. Search CLAUDE.md for references to dropped concepts:
   ```sh
   grep -nE "mcp-server|licensing-boundary|scenario-harness|userdata-overrides" CLAUDE.md
   ```
   For each hit:
   - `mcp-server` references in "Reference deployment & local stack" section — reword to reference the agentic MCP shim (now documented as a subsection of `control-api`).
   - `licensing-boundary` references — reword to point at `module-layout` (with the licensing-boundary subsection).
   - `scenario-harness` references — the harness usage is documented in CLAUDE.md "Build & test" section already; remove any references that imply it is its own concept (e.g., "see scenario-harness" prose should be reworded to point at `modeling/scenario.Start` directly).
   - `userdata-overrides` references — reword to point at `userdata` (with the per-instance overrides subsection).

3. No new gotchas or blessed invariants are added by this spec. Do not add to CLAUDE.md beyond the sweeps above.

**Verification:**

```sh
! grep -qE "(5 methods|five[- ]method|five methods)" CLAUDE.md && echo OK || echo STILL_HAS_5_METHODS
# All references to dropped concept slugs are either reworded out or contextual (e.g., quoting an old tension):
grep -nE "mcp-server|licensing-boundary|scenario-harness|userdata-overrides" CLAUDE.md || echo CLEAN
```

If the grep finds any hits, manually inspect each and confirm it has been reworded to point at the fold-destination concept (or removed). Acceptable to retain references in clearly-historical contexts (e.g., a sentence describing what *used to* be a concept), but production-current statements about the platform should use the new homes.

---

## Task 17: Adjacent block scrub across all concept files

A sweep to catch any concept file whose `Adjacent:` block still references a dropped, folded, or renamed concept slug after Tasks 8-11.

**Files:**
- EDIT: any concept file in `.ok-planner/design/concepts/` that contains a stale Adjacent reference

**Steps:**

1. Run a sweep grep for each dropped slug:
   ```sh
   for slug in licensing-boundary mcp-server scenario-harness userdata-overrides; do
     echo "=== $slug ==="
     grep -rln "$slug" .ok-planner/design/concepts/ || echo "(none)"
   done
   ```

2. For each remaining reference (excluding the fold-destination concept file itself, which legitimately references the dropped slug in its historical note like "Previously documented as a standalone concept; folded here..."), reword:
   - `licensing-boundary` → `module-layout`
   - `mcp-server` → `control-api`
   - `scenario-harness` → drop the reference or reword to point at the in-repo test harness inline (no concept replacement)
   - `userdata-overrides` → `userdata`

3. Also scan for cross-links that should point at the new concepts created in Tasks 2-5:
   ```sh
   grep -rln "cascade-graph\|discovery-cache\|transition-reason\|on-event-handler" .ok-planner/design/concepts/
   ```
   This is informational — confirms the new concepts ARE cross-linked from their natural neighbors. If a natural neighbor is missing a cross-link, add it. (Example: `concepts/control-api.md` Adjacent should mention `cascade-graph` since cascade-graph's HTTP routes are mounted on control-api.)

**Verification:**

```sh
for slug in licensing-boundary mcp-server scenario-harness userdata-overrides; do
  hits=$(grep -rln "$slug" .ok-planner/design/concepts/ | wc -l)
  echo "$slug: $hits remaining"
done
# Expected: each slug appears in at most 1 file — its fold-destination concept's historical-note section.
# `scenario-harness`: 0 files expected (no fold destination).
```

If any slug appears in more files than the expected count, inspect those files and reword as needed.

---

## Task 18: Regenerate `concepts.md` TOC

**Files:**
- EDIT: `.ok-planner/design/concepts.md`

**Steps:**

1. The `concepts.md` TOC is auto-generated by `discover-design` or `execute-plan` from the frontmatter and "What it is" first sentence of each `concepts/<slug>.md` file. Regenerate it by walking the current `concepts/` directory.

2. The regeneration procedure is the same as `discover-design`'s TOC generation: for each file in `.ok-planner/design/concepts/`, extract:
   - The concept slug from the `concept:` frontmatter field
   - The aliases list from the `aliases:` frontmatter field (if non-empty)
   - The first sentence of the "## What it is" section (the file's opening Definition)

3. Emit a Markdown file with this structure (the format the existing `concepts.md` already uses):

   ```markdown
   # Concept catalog (auto-generated)

   Read first. Then either grep for `@concept: <slug>` annotations in the code under consideration, or read `concepts/<slug>.md` for the full definition. Generated by `discover-design` and refreshed by `execute-plan` when a plan touches `concepts/`. Do not edit by hand — changes will be overwritten.

   ## Concepts

   - `<slug>` (aliases: <list>) — <first-sentence>.
   - ...
   ```

   Each line: dash + slug in backticks + optional `(aliases: <comma-separated>)` parenthetical + ` — ` + first sentence + period.

4. After regeneration, verify the file lists exactly 46 concepts, with:
   - 4 new entries: `cascade-graph`, `discovery-cache`, `on-event-handler`, `transition-reason`
   - 4 absent entries: `licensing-boundary`, `mcp-server`, `scenario-harness`, `userdata-overrides`
   - 2 slimmed entries with updated first-sentence wording: `event-log` (now describes audit-log only), `observability` (now describes peer protocols + handshake)

**Verification:**

```sh
# Count concepts in TOC:
grep -cE "^- \`" .ok-planner/design/concepts.md
# Expected: 46

# Verify new entries:
for new in cascade-graph discovery-cache on-event-handler transition-reason; do
  grep -q "^- \`$new\`" .ok-planner/design/concepts.md && echo "OK $new" || echo "MISSING $new"
done

# Verify dropped entries absent:
for dropped in licensing-boundary mcp-server scenario-harness userdata-overrides; do
  ! grep -q "^- \`$dropped\`" .ok-planner/design/concepts.md && echo "OK absent $dropped" || echo "STILL PRESENT $dropped"
done
```

All should be OK.

---

## Task 19: Final verification — build, test, lint

**Files:** none (verification-only).

**Steps:**

1. Run the full build across all three Go modules:
   ```sh
   make build-all
   ```
   Expected: clean build.

2. Run the full test suite:
   ```sh
   make test-all
   ```
   Expected: all tests pass.

3. Run the linter:
   ```sh
   make lint
   ```
   Expected: clean.

4. Run the integration package tests with race detection (this is the package the helper extraction touches):
   ```sh
   go test ./foundation/integration/... -race -count=1
   ```
   Expected: clean.

5. Run scenario tests:
   ```sh
   go test ./test/scenarios/... -count=1
   ```
   Expected: clean. Requires Docker for testcontainers-go.

6. Final design-log integrity check:
   ```sh
   # All 13 + 1 superseded tensions are in _resolved/
   ls .ok-planner/design/tensions/_resolved/ | wc -l
   # Expected: 14 (or higher if _resolved/ already had other tensions; baseline before this plan: zero, since this is the first refine-design pass)

   # No surviving open tensions reference spec 2026-05-11-design-log-convergence:
   ! grep -l "spec: 2026-05-11-design-log-convergence" .ok-planner/design/tensions/*.md && echo OK || echo STRAGGLER

   # 46 concepts:
   ls .ok-planner/design/concepts/*.md | wc -l
   # Expected: 46
   ```

**If anything fails at this stage**, investigate root cause before reporting completion. Do not skip tests or `--no-verify` past hook failures.

---

## Manual checks after completion

(None — this plan has no manual verification step. Everything is testable by command.)

---

## Tension lifecycle summary

After this plan completes, all 13 resolving tensions are moved to `.ok-planner/design/tensions/_resolved/` with `status: resolved` and a `resolution:` block. The superseded `events-table-name-overlap.md` is also moved. Final state:

```
.ok-planner/design/tensions/_resolved/
├── abandon-on-pass-duplicated-path.md        (Task 1)
├── transition-reason-missing-concept.md      (Task 2)
├── on-event-handler-missing-concept.md       (Task 3)
├── observability-split-cascade-graph-and-discovery-cache.md  (Task 6)
├── event-log-split-into-two.md               (Task 7)
├── events-table-name-overlap.md              (Task 7 — superseded)
├── licensing-boundary-fold-candidate.md      (Task 8)
├── mcp-server-fold-into-control-api.md       (Task 9)
├── scenario-harness-drop-from-catalog.md     (Task 10)
├── userdata-overrides-fold-into-userdata.md  (Task 11)
├── frame-stuck-dangling-adjacent.md          (Task 12)
├── claimant-guarded-backtick-noun.md         (Task 13)
├── claim-producer-method-count-framing.md    (Task 14)
└── claim-vs-claim-handle-layer-annotation.md (Task 15)
```

14 files in `_resolved/`. All other tensions in `.ok-planner/design/tensions/` retain `status: open` and are untouched by this plan.
