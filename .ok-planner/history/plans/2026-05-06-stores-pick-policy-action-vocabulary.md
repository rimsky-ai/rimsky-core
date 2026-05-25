# Stores: Pick-Policy Action Vocabulary v2 + fs-store `sync_strategy: on_drain` — Implementation Plan

**Spec:** `.ok-planner/specs/2026-05-06-fs-store-pick-policy-action-vocabulary-design.md`

**Goal:** Replace the legacy `release_to_back | release_to_head | delete` action vocabulary on both bundled stores with the v2 named-action set (`pop`, `pop_and_move`, `pop_and_delete`, `recycle`); add `sync_strategy: on_drain` to the filesystem store with a `drained` sentinel mechanism that produces single-pass-then-refresh queue mode.

**Architecture:** A new shared `stores/common/action/` Go package defines the `Action` tagged-union type, the four action-name constants, the `ValidationResult` struct, and YAML unmarshal/marshal for the inline parameterized form. Both `stores/filesystem/` and `stores/postgres/` import this package. Each store has its own validator returning `ValidationResult`; each store's `applyPickAction` switches on the same constants. The filesystem store additionally implements the `drained` sentinel mechanism (a single empty file under `<store-root>/.fs-store/<policy>/drained`) gated by `sync_strategy: on_drain`. Pre-v1 break-cleanly migration: old field names and old action values are rejected at config-load with errors pointing at the new vocabulary.

**Tech Stack:** Go 1.x in three modules (`foundation/`, `protocols/`, root). Postgres via `jackc/pgx/v5` (postgres store). YAML via `gopkg.in/yaml.v3`. Logging via stdlib `log/slog`. Tests use `testcontainers-go` for postgres scenario tests; `testfixture/` already runs the bundled store binaries against ephemeral roots.

---

## Reading guide for the implementer

You are working in a fresh Claude Code session against the rimsky submodule. The project root for this plan is `/Users/patrick/Documents/projects/research/verantel/submodules/rimsky`. Treat that as your working directory; all paths below are relative to it unless otherwise stated.

Before starting, do these things:

1. **Read the spec at `.ok-planner/specs/2026-05-06-fs-store-pick-policy-action-vocabulary-design.md`.** The plan implements the spec exactly. If a task seems to contradict the spec, the spec wins — pause and surface the contradiction in the implementation notes.

2. **Read `.claude/rules/rules.md` and `.claude/rules/cold-read-cheatsheet.md`.** The rules' "After Code Changes — Required Final Step" applies after every Go change in this plan; cold-read conventions govern the new code.

3. **Read `CLAUDE.md`** for the layer-crystallization layout (foundation / protocols / modeling / stores / executors) and the depguard import rules. Note: `stores/common/` is a new sub-tree this plan introduces. Both `stores/filesystem/` and `stores/postgres/` will import from it. No depguard rule prohibits this; verify by running `make lint` after the package is added.

4. **Read these existing source files** so you have grounding before edits:
   - `stores/filesystem/store/store.go` (PickPolicy struct, validator, Open/Commit/Abandon/Release dispatch)
   - `stores/filesystem/store/pick_policy.go` (runSync, openPickPolicy, applyPickAction)
   - `stores/filesystem/store/sweep.go` (RunSweep, sweepOnce — note `on_sweep` handling at ~line 43)
   - `stores/filesystem/cmd/main.go` (yamlPickPolicy struct, config loader)
   - `stores/filesystem/server/observability.go` (line ~306, references old field names)
   - `stores/postgres/store/store.go` (PickPolicy struct, applyPickAction, validPickAction)
   - `stores/postgres/cmd/main.go` (yamlPickPolicy struct)
   - `stores/postgres/server/observability.go` (lines ~306–317, references old field names)

After every Go change, run `go build ./...` and `go test ./...` from the repo root. After every change to a Go package that's part of the supervisor / scheduler / queue paths, the persistence layer, or one of the bundled stores, also run `make lint`.

### Three discoveries during plan-writing (kept here so they don't get lost)

These are surfaced for context; they are already accounted for in the tasks below.

- **`on_sweep` is an existing fs-store `sync_strategy` value.** The spec drops it (pre-v1 break-cleanly). The new validator's strategy enum (`on_open | on_drain | explicit | never`) rejects `on_sweep` at config-load. The `sweep.go` code path that runs sync inside the sweep loop when strategy was `on_sweep` is removed. No in-tree configs use `on_sweep` (verified by grep at plan-writing time).
- **The postgres store has no `SyncStrategy`.** Spec §4 and §5 are explicitly fs-store-scoped. The pg-store's PickPolicy struct stays without a sync-strategy field; the pg-store validator does NOT accept a `sync_strategy` key in YAML.
- **The `Store` struct (both fs and pg) has no injectable `*slog.Logger` field today.** Per spec §6.2a, the validator returns a `ValidationResult{Errors, Warnings}` struct; the `Store` constructor logs warnings via package-level `slog`. Tests inspect the returned struct directly. Do not add a logger field to the Store struct as part of this work.

### Postgres-store scope expansion (from the brainstorm)

The original brainstorm focused on the filesystem store; the postgres store was added during plan-writing for vocabulary parity. The spec covers both. The plan covers both, in one execution. Do not split it.

---

## Task 1: Verify clean baseline

**Files to touch:** none (verification only).

**Steps:**

1. From the repo root, run `go build ./...` and confirm it exits 0.
2. Run `go test ./...` (this includes scenario tests via testcontainers; Docker must be running). Confirm it exits 0. Note any flakes or pre-existing failures so you can distinguish them from regressions later.
3. Run `make lint`. Confirm it exits 0.
4. Run `cd executors/claude-agent && npm install && npm test && npm run build && cd -`. Confirm each step exits 0. (This dispatch doesn't touch the TS executor, but a clean baseline is the discipline.)

**Verification:**
```bash
go build ./... && go test ./... && make lint
```
All three must exit 0. If anything fails on a clean tree, stop and surface to the user — the plan assumes a clean baseline.

---

## Task 2: Create the shared `stores/common/action/` package — types and constants

**Files to touch:** `stores/common/action/action.go` (new).

**Steps:**

1. Create `stores/common/action/action.go` with the package declaration `package action` and the type/constant definitions below.

   ```go
   // Copyright © 2026 Fall Guy Consulting.
   // Licensed under the Apache License, Version 2.0.

   // Package action defines the v2 pick-policy action vocabulary shared
   // by every bundled claim-producer store. Each store imports this
   // package to resolve action names and (de)serialize Action values
   // from YAML.
   //
   // Per spec .ok-planner/specs/2026-05-06-fs-store-pick-policy-action-vocabulary-design.md §3.
   package action

   import (
       "errors"
       "fmt"
   )

   // Kind names the action's queue-entry-fate × resource-fate pair.
   //
   // Stores implement the subset of kinds that's meaningful for their
   // underlying mechanism (fs supports all four; pg supports Pop and
   // Recycle). Validators reject unsupported kinds at config-load.
   type Kind string

   const (
       // Pop: queue entry consumed; underlying resource kept in place.
       Pop Kind = "pop"
       // PopAndMove: queue entry consumed; resource renamed to the
       // configured target. Parameterized — Action.MoveTarget is non-empty.
       PopAndMove Kind = "pop_and_move"
       // PopAndDelete: queue entry consumed; resource destroyed.
       PopAndDelete Kind = "pop_and_delete"
       // Recycle: queue entry returned to queue tail; resource kept.
       Recycle Kind = "recycle"
   )

   // Action is the tagged-union result of YAML unmarshal for an
   // on_commit / on_give_up field. MoveTarget is populated only when
   // Kind == PopAndMove.
   type Action struct {
       Kind       Kind
       MoveTarget string
   }

   // ValidationResult is the shape both fs-store and pg-store validators
   // return. Errors fail config-load; Warnings are advisory and surfaced
   // via package-level slog by the constructor.
   type ValidationResult struct {
       Errors   []error
       Warnings []string
   }

   // OK reports whether the result has no errors.
   func (r ValidationResult) OK() bool { return len(r.Errors) == 0 }

   // AllKinds returns every Kind constant. Used for validator
   // error-message lists and the cross-store consistency test.
   func AllKinds() []Kind {
       return []Kind{Pop, PopAndMove, PopAndDelete, Recycle}
   }

   // ParseKind returns the Kind for s. Unknown strings produce an error
   // listing the legal kinds.
   func ParseKind(s string) (Kind, error) {
       for _, k := range AllKinds() {
           if string(k) == s {
               return k, nil
           }
       }
       return "", fmt.Errorf("unknown action %q (legal: pop, pop_and_move, pop_and_delete, recycle)", s)
   }

   // Validate checks intra-action consistency. Returns nil on success.
   func (a Action) Validate() error {
       if _, err := ParseKind(string(a.Kind)); err != nil {
           return err
       }
       if a.Kind == PopAndMove && a.MoveTarget == "" {
           return errors.New("pop_and_move requires a non-empty target path")
       }
       if a.Kind != PopAndMove && a.MoveTarget != "" {
           return fmt.Errorf("action %q does not take a target; got %q", a.Kind, a.MoveTarget)
       }
       return nil
   }
   ```

2. Confirm the file builds cleanly.

**Verification:**
```bash
cd stores/common/action && go build ./... && cd -
```
Must exit 0.

---

## Task 3: Add YAML unmarshal for Action (inline parameterized form)

**Files to touch:** `stores/common/action/yaml.go` (new), `stores/common/action/yaml_test.go` (new).

**Steps:**

1. Create `stores/common/action/yaml.go` with `UnmarshalYAML` on `*Action`:

   ```go
   // Copyright © 2026 Fall Guy Consulting.
   // Licensed under the Apache License, Version 2.0.

   package action

   import (
       "fmt"

       "gopkg.in/yaml.v3"
   )

   // UnmarshalYAML accepts two shapes per spec §3.5:
   //
   //   on_commit: pop                        # bare string for non-parameterized
   //   on_commit: { pop_and_move: target }   # one-key map for parameterized
   //
   // Anything else (null, number, sequence, multi-key map, empty map,
   // nested map) is rejected with a parse-level error.
   func (a *Action) UnmarshalYAML(node *yaml.Node) error {
       switch node.Kind {
       case yaml.ScalarNode:
           // Bare string: must be a non-parameterized action name.
           kind, err := ParseKind(node.Value)
           if err != nil {
               return fmt.Errorf("line %d: %w", node.Line, err)
           }
           if kind == PopAndMove {
               return fmt.Errorf("line %d: action %q requires an inline target (use { %s: <target_path> })",
                   node.Line, kind, kind)
           }
           a.Kind = kind
           return nil
       case yaml.MappingNode:
           // One-key map: must be { pop_and_move: <string> }.
           if len(node.Content) == 0 {
               return fmt.Errorf("line %d: empty action map", node.Line)
           }
           if len(node.Content) != 2 {
               return fmt.Errorf("line %d: action map must have exactly one key (got %d)", node.Line, len(node.Content)/2)
           }
           keyNode := node.Content[0]
           valueNode := node.Content[1]
           if keyNode.Kind != yaml.ScalarNode {
               return fmt.Errorf("line %d: action key must be a string", keyNode.Line)
           }
           kind, err := ParseKind(keyNode.Value)
           if err != nil {
               return fmt.Errorf("line %d: %w", keyNode.Line, err)
           }
           if kind != PopAndMove {
               return fmt.Errorf("line %d: action %q is not parameterized (use it as a bare string)", keyNode.Line, kind)
           }
           if valueNode.Kind != yaml.ScalarNode {
               return fmt.Errorf("line %d: pop_and_move target must be a string path", valueNode.Line)
           }
           if valueNode.Value == "" {
               return fmt.Errorf("line %d: pop_and_move target must be non-empty", valueNode.Line)
           }
           a.Kind = PopAndMove
           a.MoveTarget = valueNode.Value
           return nil
       default:
           return fmt.Errorf("line %d: action must be a string or one-key map (got YAML kind %d)", node.Line, node.Kind)
       }
   }
   ```

2. Create `stores/common/action/yaml_test.go` with table-driven tests covering:
   - Each bare-string action: `pop`, `pop_and_delete`, `recycle` — round-trips correctly.
   - `pop_and_move: <target>` — parses with MoveTarget set.
   - Bare `pop_and_move` (no target) — rejected with a clear error.
   - Unknown bare string `foo` — rejected.
   - Empty map `{}` — rejected.
   - Multi-key map `{ pop_and_move: a, pop: b }` — rejected.
   - Sequence `[pop, recycle]` — rejected.
   - Null `~` — rejected.
   - Number `42` — rejected.
   - `pop_and_move` with empty target `{ pop_and_move: "" }` — rejected.
   - Old action names `release_to_back`, `release_to_head`, `delete` — rejected (they're not in `AllKinds()`).

   Sketch:
   ```go
   func TestUnmarshalAction(t *testing.T) {
       cases := []struct {
           name   string
           yaml   string
           want   Action
           errSub string // non-empty means parse error expected; substring must appear
       }{
           {"bare pop", "pop", Action{Kind: Pop}, ""},
           {"bare recycle", "recycle", Action{Kind: Recycle}, ""},
           {"bare pop_and_delete", "pop_and_delete", Action{Kind: PopAndDelete}, ""},
           {"pop_and_move with target", "pop_and_move: guidance.failed", Action{Kind: PopAndMove, MoveTarget: "guidance.failed"}, ""},
           {"bare pop_and_move rejected", "pop_and_move", Action{}, "requires an inline target"},
           {"unknown action", "foo", Action{}, "unknown action"},
           {"old release_to_back", "release_to_back", Action{}, "unknown action"},
           {"old release_to_head", "release_to_head", Action{}, "unknown action"},
           {"old delete", "delete", Action{}, "unknown action"},
           {"empty map", "{}", Action{}, "empty action map"},
           {"multi-key", "{ pop_and_move: a, pop: b }", Action{}, "exactly one key"},
           {"sequence", "[pop]", Action{}, "must be a string or one-key map"},
           {"empty target", "{ pop_and_move: \"\" }", Action{}, "must be non-empty"},
       }
       // ... t.Run(c.name, func(t *testing.T) { ... yaml.Unmarshal ... })
   }
   ```

**Verification:**
```bash
go test ./stores/common/action/... -count=1
```
All tests must pass.

---

## Task 4: Update fs-store `PickPolicy` struct to use new field types

**Files to touch:** `stores/filesystem/store/store.go`.

**Steps:**

1. Open `stores/filesystem/store/store.go`. Locate `type PickPolicy struct` (around line 32).
2. Add an import for the shared package: `"github.com/fallguyconsulting/rimsky/stores/common/action"`.
3. Replace the struct definition. Old:
   ```go
   type PickPolicy struct {
       Root              string
       FolderPattern     *regexp.Regexp
       OnCommitDefault   string
       OnGiveUpDefault   string
       VisibilityTimeout time.Duration
       SyncStrategy      string

       syncMu sync.Mutex
   }
   ```
   New:
   ```go
   type PickPolicy struct {
       Root              string
       FolderPattern     *regexp.Regexp
       OnCommit          action.Action
       OnGiveUp          action.Action
       VisibilityTimeout time.Duration
       SyncStrategy      string // "on_open" | "on_drain" | "explicit" | "never"
       RefreshOnDrain    bool   // (deprecated; keep field absent — sync_strategy: on_drain replaces it)

       syncMu sync.Mutex
   }
   ```
   (Drop `RefreshOnDrain` from the struct entirely — the spec subsumes it into `sync_strategy`. Remove the line.)
4. Build to surface every caller that breaks.

**Verification:**
```bash
go build ./stores/filesystem/...
```
This will fail because callers of `pp.OnCommitDefault` / `pp.OnGiveUpDefault` exist. That's expected; subsequent tasks fix them. Note the failing line numbers; they form your worklist.

---

## Task 5: Replace fs-store `validatePickPolicy` with new validator returning `ValidationResult`

**Files to touch:** `stores/filesystem/store/store.go`.

**Steps:**

1. Locate `validatePickPolicy` (around line 310).
2. Replace its body with logic that builds and returns a `ValidationResult` instead of a single error. Today's signature returns `error`. Change it to return `action.ValidationResult`. Also rename the function to `validatePickPolicyV2` if needed to avoid conflicts during the transition; at the end, the only caller (the `New` constructor) is updated to consume the new shape.

   Sketch:
   ```go
   func validatePickPolicy(storeRoot, selector string, pp *PickPolicy) action.ValidationResult {
       var res action.ValidationResult
       addErr := func(err error) { res.Errors = append(res.Errors, err) }
       addWarn := func(w string)  { res.Warnings = append(res.Warnings, w) }

       if pp == nil {
           addErr(errors.New("policy is nil"))
           return res
       }

       // Existing root-validation logic (reuse verbatim) — converts each
       // existing `return err` to addErr(...) and continues so multiple
       // errors surface in one pass.
       if pp.Root == "" {
           addErr(errors.New("root: required"))
       }
       // ... (existing IsAbs / Clean / Stat / readability / writability checks) ...

       // Action validation (replaces today's switch on string vocab).
       if err := pp.OnCommit.Validate(); err != nil {
           addErr(fmt.Errorf("on_commit: %w", err))
       }
       if err := pp.OnGiveUp.Validate(); err != nil {
           addErr(fmt.Errorf("on_give_up: %w", err))
       }

       // pop_and_move target validation: cross-fs check.
       if pp.OnCommit.Kind == action.PopAndMove {
           if err := validateMoveTargetSameFS(storeRoot, pp.Root, pp.OnCommit.MoveTarget); err != nil {
               addErr(fmt.Errorf("on_commit: pop_and_move: %w", err))
           }
       }
       if pp.OnGiveUp.Kind == action.PopAndMove {
           if err := validateMoveTargetSameFS(storeRoot, pp.Root, pp.OnGiveUp.MoveTarget); err != nil {
               addErr(fmt.Errorf("on_give_up: pop_and_move: %w", err))
           }
       }

       // Visibility timeout (existing rule, kept).
       if pp.VisibilityTimeout <= 0 {
           addErr(errors.New("visibility_timeout_seconds: must be > 0"))
       }

       // sync_strategy enum (was on_open|on_sweep; now four values).
       switch pp.SyncStrategy {
       case "":
           pp.SyncStrategy = "on_open" // default
       case "on_open", "on_drain", "explicit", "never":
           // ok
       default:
           addErr(fmt.Errorf("sync_strategy: must be on_open|on_drain|explicit|never, got %q", pp.SyncStrategy))
       }

       // Validator rule §6.1a: pop + sync_strategy: on_open is rejected
       // (queue would never drain under fs-store discovery semantics).
       if pp.OnCommit.Kind == action.Pop && pp.SyncStrategy == "on_open" {
           addErr(errors.New("on_commit: pop is incompatible with sync_strategy: on_open (queue would never drain because runSync re-adds popped folders); use sync_strategy: on_drain"))
       }

       // Validator rule §6.2: warn on recycle + on_drain (queue never empties; on_drain never fires).
       if pp.OnCommit.Kind == action.Recycle && pp.SyncStrategy == "on_drain" {
           addWarn(fmt.Sprintf("filesystem store: pick_policies[%q]: recycle + sync_strategy: on_drain is inert (queue never empties; on_drain never fires)", selector))
       }

       _ = selector
       return res
   }
   ```

3. Add `validateMoveTargetSameFS` helper per Task 6 (next task).

**Verification:**
```bash
go build ./stores/filesystem/...
```
Will still fail because `New` calls the old shape and references `OnCommitDefault`. Fix in subsequent tasks.

---

## Task 6: Implement `validateMoveTargetSameFS` cross-fs check

**Files to touch:** `stores/filesystem/store/store.go` (or a new sibling file `validate.go`).

**Steps:**

1. Add the helper with the exact procedure from spec §6.3:

   ```go
   import "syscall"

   // validateMoveTargetSameFS checks that target (resolved relative to
   // storeRoot) is on the same filesystem as <storeRoot>/<policyRoot>.
   //
   // Per spec §6.3: resolve both paths to absolute, follow symlinks via
   // filepath.EvalSymlinks, then compare device IDs.
   //
   // The target must also exist (must be a directory). The validator
   // does NOT create missing target directories.
   func validateMoveTargetSameFS(storeRoot, policyRoot, target string) error {
       policyAbs, err := filepath.Abs(filepath.Join(storeRoot, policyRoot))
       if err != nil {
           return fmt.Errorf("resolve policy root: %w", err)
       }
       targetAbs, err := filepath.Abs(filepath.Join(storeRoot, target))
       if err != nil {
           return fmt.Errorf("resolve target %q: %w", target, err)
       }
       policyResolved, err := filepath.EvalSymlinks(policyAbs)
       if err != nil {
           return fmt.Errorf("resolve symlinks for policy root %q: %w", policyAbs, err)
       }
       targetResolved, err := filepath.EvalSymlinks(targetAbs)
       if err != nil {
           return fmt.Errorf("resolve symlinks for target %q: %w", targetAbs, err)
       }
       policyStat, err := os.Stat(policyResolved)
       if err != nil {
           return fmt.Errorf("stat policy root: %w", err)
       }
       targetStat, err := os.Stat(targetResolved)
       if err != nil {
           return fmt.Errorf("stat target: %w", err)
       }
       if !targetStat.IsDir() {
           return fmt.Errorf("target %q is not a directory", target)
       }
       policySys, ok1 := policyStat.Sys().(*syscall.Stat_t)
       targetSys, ok2 := targetStat.Sys().(*syscall.Stat_t)
       if !ok1 || !ok2 {
           return errors.New("filesystem device-id query unavailable on this platform")
       }
       if policySys.Dev != targetSys.Dev {
           return fmt.Errorf("target %q is on a different filesystem than the policy root %q; os.Rename across filesystems is not atomic, refusing to load", target, policyRoot)
       }
       return nil
   }
   ```

2. Build cleanly.

**Verification:**
```bash
go build ./stores/filesystem/...
```
Should now build past the validator (subsequent tasks update the caller). May still fail at `applyPickAction` etc. — the build error stack tells you which task is next.

---

## Task 7: Update fs-store `New` to consume `ValidationResult`

**Files to touch:** `stores/filesystem/store/store.go`.

**Steps:**

1. Locate the `New` function (around line 86). Where it calls `validatePickPolicy`, change the consumption:

   ```go
   for selector, pp := range cfg.PickPolicies {
       res := validatePickPolicy(cfg.Root, selector, pp)
       if !res.OK() {
           // Surface all errors at once. Wrap with selector context.
           msgs := make([]string, 0, len(res.Errors))
           for _, e := range res.Errors {
               msgs = append(msgs, e.Error())
           }
           return nil, fmt.Errorf("filesystem store: pick_policies[%q]: %s",
               selector, strings.Join(msgs, "; "))
       }
       for _, w := range res.Warnings {
           slog.Warn(w) // package-level slog; spec §6.2a
       }
       // Idempotent state-directory creation (existing logic).
       dir := filepath.Join(cfg.Root, ".fs-store", trimAtPrefix(selector))
       for _, sub := range []string{"available", "in_progress"} {
           if err := os.MkdirAll(filepath.Join(dir, sub), 0o755); err != nil {
               return nil, fmt.Errorf("filesystem store: mkdir %s: %w", filepath.Join(dir, sub), err)
           }
       }
   }
   ```

2. Add the `log/slog` and `strings` imports if not already present.

**Verification:**
```bash
go build ./stores/filesystem/store/...
```
Should now build (modulo callers of OnCommitDefault elsewhere — fixed below).

---

## Task 8: Update fs-store `applyPickAction` for the new vocabulary

**Files to touch:** `stores/filesystem/store/pick_policy.go`.

**Steps:**

1. Locate `applyPickAction` (around line 256). Change its signature so the action is passed as `action.Action` instead of `string`:

   ```go
   func (s *Store) applyPickAction(pp *PickPolicy, selector, entry, folder string, act action.Action) error {
       inProgDir := filepath.Join(policyStateDir(s.root, selector), "in_progress")
       availDir := filepath.Join(policyStateDir(s.root, selector), "available")
       src := filepath.Join(inProgDir, entry)
       switch act.Kind {
       case action.Pop:
           // Queue entry consumed (sentinel removed); folder stays on disk.
           if err := os.Remove(src); err != nil && !errors.Is(err, fs.ErrNotExist) {
               return fmt.Errorf("filesystem store: pop unlink in_progress: %w", err)
           }
           return nil
       case action.PopAndMove:
           folderAbs := filepath.Join(s.root, pp.Root, folder)
           targetAbs := filepath.Join(s.root, act.MoveTarget, folder)
           if err := os.Rename(folderAbs, targetAbs); err != nil {
               return fmt.Errorf("filesystem store: pop_and_move rename %q→%q: %w",
                   folderAbs, targetAbs, err)
           }
           if err := os.Remove(src); err != nil && !errors.Is(err, fs.ErrNotExist) {
               return fmt.Errorf("filesystem store: pop_and_move unlink in_progress: %w", err)
           }
           return nil
       case action.PopAndDelete:
           folderAbs := filepath.Join(s.root, pp.Root, folder)
           if err := os.RemoveAll(folderAbs); err != nil {
               return fmt.Errorf("filesystem store: pop_and_delete removeall %s: %w", folderAbs, err)
           }
           if err := os.Remove(src); err != nil && !errors.Is(err, fs.ErrNotExist) {
               return fmt.Errorf("filesystem store: pop_and_delete unlink in_progress: %w", err)
           }
           return nil
       case action.Recycle:
           // Equivalent to today's release_to_back.
           now := time.Now()
           if err := os.Chtimes(src, now, now); err != nil && !errors.Is(err, fs.ErrNotExist) {
               return fmt.Errorf("filesystem store: recycle chtimes: %w", err)
           }
           if err := os.Rename(src, filepath.Join(availDir, folder)); err != nil && !errors.Is(err, fs.ErrNotExist) {
               return fmt.Errorf("filesystem store: recycle rename: %w", err)
           }
           return nil
       default:
           return fmt.Errorf("filesystem store: unknown pick action %q", act.Kind)
       }
   }
   ```

2. Update the two callers of `applyPickAction` in `store.go` (`Commit` and `Abandon`, around lines 251 and 270) to pass the new `action.Action` value:
   - `Commit` calls `s.applyPickAction(pp, sel, entry, folder, pp.OnCommit)`.
   - `Abandon` calls `s.applyPickAction(pp, sel, entry, folder, pp.OnGiveUp)`.

**Verification:**
```bash
go build ./stores/filesystem/...
```
Should build (modulo openPickPolicy and sweep, fixed in subsequent tasks).

---

## Task 9: Update fs-store `openPickPolicy` to dispatch by `sync_strategy` and write `drained` on last claim

**Files to touch:** `stores/filesystem/store/pick_policy.go`.

**Steps:**

1. Locate `openPickPolicy` (around line 156). Replace it with a strategy-aware dispatch per spec §5.4–§5.6.

   ```go
   func (s *Store) openPickPolicy(claimID, selector string, pp *PickPolicy) (corestore.OpenOutcome, error) {
       state := policyStateDir(s.root, selector)
       availDir := filepath.Join(state, "available")
       inProgDir := filepath.Join(state, "in_progress")
       drainedPath := filepath.Join(state, "drained")

       switch pp.SyncStrategy {
       case "on_open":
           if err := s.runSync(selector, pp); err != nil {
               return corestore.OpenOutcome{}, fmt.Errorf("filesystem store: sync: %w", err)
           }
       case "on_drain":
           // Step 1: empty + drained-present → return Unavailable.
           empty, err := isDirEmpty(availDir)
           if err != nil {
               return corestore.OpenOutcome{}, fmt.Errorf("filesystem store: readdir available: %w", err)
           }
           if empty {
               if drainedFileExists(drainedPath) {
                   if err := os.Remove(drainedPath); err != nil && !errors.Is(err, fs.ErrNotExist) {
                       return corestore.OpenOutcome{}, fmt.Errorf("filesystem store: remove drained: %w", err)
                   }
                   return corestore.OpenOutcome{Available: false}, nil
               }
               // Step 2: drained absent → run sync.
               if err := s.runSync(selector, pp); err != nil {
                   return corestore.OpenOutcome{}, fmt.Errorf("filesystem store: sync: %w", err)
               }
           }
       case "explicit", "never":
           // No sync trigger.
       default:
           return corestore.OpenOutcome{}, fmt.Errorf("filesystem store: invalid sync_strategy %q", pp.SyncStrategy)
       }

       // Try the rename-as-claim against available/.
       outcome, lastItem, err := s.tryRenameClaim(claimID, selector, pp, availDir, inProgDir)
       if err != nil {
           return corestore.OpenOutcome{}, err
       }
       if outcome.Available {
           if pp.SyncStrategy == "on_drain" && lastItem {
               // Write drained sentinel atomically (O_EXCL).
               f, ferr := os.OpenFile(drainedPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
               if ferr == nil {
                   _ = f.Close()
               }
               // EEXIST is benign: a concurrent Open already wrote it.
           }
           return outcome, nil
       }
       // available/ ended up empty after sync. on_drain corpus-empty case:
       if pp.SyncStrategy == "on_drain" {
           f, ferr := os.OpenFile(drainedPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
           if ferr == nil {
               _ = f.Close()
           }
       }
       return corestore.OpenOutcome{Available: false}, nil
   }

   // tryRenameClaim is the existing rename-as-claim logic factored out
   // of the previous openPickPolicy. Returns (outcome, wasLastItem, err).
   // wasLastItem reports whether available/ became empty as a result of
   // the successful claim. It's always false on Unavailable outcomes.
   func (s *Store) tryRenameClaim(claimID, selector string, pp *PickPolicy, availDir, inProgDir string) (corestore.OpenOutcome, bool, error) {
       // ... (existing readdir+sort+rename loop from old openPickPolicy,
       //     adjusted to also count remaining entries on success) ...
   }

   func isDirEmpty(dir string) (bool, error) {
       entries, err := os.ReadDir(dir)
       if err != nil {
           return false, err
       }
       return len(entries) == 0, nil
   }

   func drainedFileExists(path string) bool {
       _, err := os.Stat(path)
       return err == nil
   }
   ```

2. The existing rename-loop in the old `openPickPolicy` becomes `tryRenameClaim`. Lift it verbatim, but after the successful rename, do a `len(remainingAfter) == 0` check by re-reading `available/` once (per spec §5.2 implementation note — preserves lockless rename-as-claim).

3. Confirm the file builds.

**Verification:**
```bash
go build ./stores/filesystem/store/...
```

---

## Task 10: Add `removeDrainedIfPresent` helper and wire into `runSync`

**Files to touch:** `stores/filesystem/store/pick_policy.go`.

**Steps:**

1. Add a small helper near `runSync`:

   ```go
   func removeDrainedIfPresent(state string) {
       _ = os.Remove(filepath.Join(state, "drained"))
       // Best-effort; ENOENT is benign.
   }
   ```

2. In `runSync`, locate the "Add brand-new folders" loop (around line 126). After any new folder is added to `available/`, call `removeDrainedIfPresent(state)`. Implementation: track an "added something" boolean; call the helper once at the end if it's true.

   ```go
   addedAny := false
   for folder := range extant {
       if _, ok := tracked[folder]; ok {
           continue
       }
       f, err := os.OpenFile(filepath.Join(availDir, folder), ...)
       if err == nil {
           _ = f.Close()
           addedAny = true
           continue
       }
       // ... (existing EEXIST handling) ...
   }
   // ... existing "Remove stale" loop ...
   if addedAny {
       removeDrainedIfPresent(state)
   }
   return nil
   ```

**Verification:**
```bash
go build ./stores/filesystem/store/...
```

---

## Task 11: Update fs-store `sweepOnce` — drop `on_sweep`, add `drained` removal

**Files to touch:** `stores/filesystem/store/sweep.go`.

**Steps:**

1. Locate `sweepOnce` (around line 41). Remove the `if pp.SyncStrategy == "on_sweep" { runSync(...) }` block at line 43–47 entirely. The new strategy enum has no `on_sweep`.

2. After the rename loop that returns items from `in_progress/` to `available/`, add a `removeDrainedIfPresent(state)` call if any items were returned:

   ```go
   reclaimed := false
   for _, e := range entries {
       // ... existing rename logic ...
       if err := os.Rename(src, dst); err != nil && !errors.Is(err, fs.ErrNotExist) {
           slog.Warn(...)
       } else {
           reclaimed = true
       }
   }
   if reclaimed {
       removeDrainedIfPresent(policyStateDir(s.root, selector))
   }
   ```

3. Update the doc comment on `RunSweep` (around line 17) to remove the "+ (when SyncStrategy is on_sweep) the auto-discovery sync" phrase. The function now does only visibility-timeout reaping.

**Verification:**
```bash
go build ./stores/filesystem/...
```

---

## Task 12: Update fs-store `cmd/main.go` — yamlPickPolicy struct and doc comment

**Files to touch:** `stores/filesystem/cmd/main.go`.

**Steps:**

1. Locate `type yamlPickPolicy struct` (line ~58). Replace `OnCommitDefault` and `OnGiveUpDefault` with new fields:

   ```go
   type yamlPickPolicy struct {
       Root                     string         `yaml:"root"`
       FolderPattern            string         `yaml:"folder_pattern"`
       OnCommit                 action.Action  `yaml:"on_commit"`
       OnGiveUp                 action.Action  `yaml:"on_give_up"`
       VisibilityTimeoutSeconds int            `yaml:"visibility_timeout_seconds"`
       SyncStrategy             string         `yaml:"sync_strategy"`
   }
   ```

2. Add the import for `"github.com/fallguyconsulting/rimsky/stores/common/action"`.

3. Update the conversion to `fsstore.PickPolicy` (line ~100):

   ```go
   policies[selector] = &fsstore.PickPolicy{
       Root:              pp.Root,
       FolderPattern:     pat,
       OnCommit:          pp.OnCommit,
       OnGiveUp:          pp.OnGiveUp,
       VisibilityTimeout: time.Duration(pp.VisibilityTimeoutSeconds) * time.Second,
       SyncStrategy:      pp.SyncStrategy,
   }
   ```

4. Update the doc comment at line ~22–25 (the YAML example) to use the new vocabulary:

   ```
   //	pick_policies:
   //	  "@docs-ring":
   //	    root: documents
   //	    folder_pattern: "^[a-z][a-z0-9-]*$"
   //	    on_commit: recycle
   //	    on_give_up: recycle
   //	    visibility_timeout_seconds: 1800
   //	    sync_strategy: on_open
   ```

**Verification:**
```bash
go build ./stores/filesystem/cmd/...
```

---

## Task 13: Update fs-store `server/observability.go` references

**Files to touch:** `stores/filesystem/server/observability.go`.

**Steps:**

1. Locate the line referencing `pp.SyncStrategy` (line ~306) and any other references to `OnCommitDefault` / `OnGiveUpDefault`. Update to use `pp.OnCommit.Kind`, `pp.OnGiveUp.Kind` and (for `pop_and_move`) include the move target.
2. The observability output is a string-keyed JSON map; pick stable key names. Suggested:
   ```go
   "on_commit":             string(pp.OnCommit.Kind),
   "on_commit_move_target": pp.OnCommit.MoveTarget, // empty unless pop_and_move
   "on_give_up":            string(pp.OnGiveUp.Kind),
   "on_give_up_move_target": pp.OnGiveUp.MoveTarget,
   ```
3. Build cleanly.

**Verification:**
```bash
go build ./stores/filesystem/...
```

---

## Task 14: Update existing fs-store tests in `stores/filesystem/store/`

**Files to touch:** `stores/filesystem/store/admin_test.go`, `pick_policy_test.go`, `store_test.go`.

**Steps:**

1. Search-and-replace the old field-name+value pairs throughout each file:
   - `OnCommitDefault: "release_to_back"` → `OnCommit: action.Action{Kind: action.Recycle}`
   - `OnGiveUpDefault: "release_to_back"` → `OnGiveUp: action.Action{Kind: action.Recycle}`
   - `OnCommitDefault: "delete"` → `OnCommit: action.Action{Kind: action.PopAndDelete}`
   - `OnGiveUpDefault: "delete"` → `OnGiveUp: action.Action{Kind: action.PopAndDelete}`
   - `OnCommitDefault: "release_to_head"` → (no longer supported; pick `Recycle` as the closest analog and add a comment, or update the test's intent)
2. Add the import for the shared package: `"github.com/fallguyconsulting/rimsky/stores/common/action"`.
3. The helper functions (`newRingStore`) likely take `onCommit, onGiveUp string` parameters; update them to take `action.Action` instead, or build the Actions inside.
4. Confirm tests build and pass.

**Verification:**
```bash
go test ./stores/filesystem/store/... -count=1
```
All existing tests should pass with the new vocabulary.

---

## Task 15: New fs-store action-vocabulary tests

**Files to touch:** `stores/filesystem/store/action_vocab_test.go` (new).

**Steps:**

1. Create unit tests per spec §10.1:
   - `TestAction_Pop_FolderStays` — Open → Commit with `OnCommit: action.Action{Kind: action.Pop}`. Assert: in_progress sentinel gone; folder still on disk; available sentinel for that folder NOT re-created (queue truly drained for this item).
   - `TestAction_PopAndMove_FolderRenamed` — Configure `OnCommit: action.Action{Kind: action.PopAndMove, MoveTarget: "archive"}`. Open → Commit. Assert: folder moved to `<root>/archive/<folder>`; in_progress sentinel gone.
   - `TestAction_PopAndMove_GiveUpUsesGiveUpTarget` — Configure `OnCommit: pop_and_move(target=ok)`, `OnGiveUp: pop_and_move(target=failed)`. Open → Abandon. Assert: folder moved to `<root>/failed/<folder>`.
   - `TestAction_PopAndDelete_FolderGone` — Open → Commit with `OnCommit: action.Action{Kind: action.PopAndDelete}`. Assert: folder removed from disk via `os.RemoveAll`.
   - `TestAction_Recycle_QueueCycles` — Open → Commit with `OnCommit: action.Action{Kind: action.Recycle}`. Assert: available sentinel re-created with fresh mtime; folder still on disk; reclaim succeeds.

2. Each test scaffolds a temp dir with N folders, builds a Store with the appropriate PickPolicy, runs Open → terminal → assertions. Mirror the existing `pick_policy_test.go` test fixture pattern.

**Verification:**
```bash
go test ./stores/filesystem/store/ -run TestAction_ -count=1
```

---

## Task 16: New fs-store `drained` mechanism tests

**Files to touch:** `stores/filesystem/store/drained_test.go` (new).

**Steps:**

1. Create per spec §10.2:
   - `TestOnDrain_SinglePass` — N=3 folders, `OnCommit: pop`, `SyncStrategy: on_drain`. Loop: Open → Commit → Open → Commit → Open → Commit. Then Open should return Unavailable (drained sentinel created and consumed). Then Open again should run sync; assert any new folders added in the meantime are picked up; if none added, returns Unavailable again with drained re-written.
   - `TestOnDrain_EmptyCorpus` — 0 folders, `pop + on_drain`. First Open: writes drained, returns Unavailable. Second Open: removes drained, returns Unavailable. Third Open: writes drained, returns Unavailable. Verify the sentinel oscillates and Unavailable is consistent.
   - `TestOnDrain_SweepClearsDrained` — Drain the queue, `drained` is written. Manually create an `in_progress/` sentinel for a folder that doesn't exist on disk (simulating a stale claim) and advance the visibility-timeout cutoff (use a tiny VisibilityTimeout). Run the sweep; the sweep moves the sentinel to `available/` AND removes `drained`. Next Open returns Acquired (not Unavailable).
   - `TestOnDrain_RaceUnderConcurrentOpens` — `t.Parallel`, M=20 concurrent `Open` calls against an N=10 folder corpus with `pop + on_drain`. Assert: total Acquired count == 10, total Unavailable count == M-10, and exactly one drained sentinel exists at the end.

**Verification:**
```bash
go test ./stores/filesystem/store/ -run TestOnDrain_ -count=20 -race
```
All must pass under -race.

---

## Task 17: New fs-store validator tests

**Files to touch:** `stores/filesystem/store/validator_test.go` (new).

**Steps:**

1. Create per spec §10.3:
   - `TestValidator_RejectsOldNames` — config-load with `OnCommit: action.Action{Kind: "release_to_back"}` (constructed manually as a string-Kind to bypass UnmarshalYAML; or test via YAML round-trip with `release_to_back` in the YAML — the YAML test belongs in `stores/common/action/`). Verify the New constructor returns an error; assert the error message contains "unknown action".
   - `TestValidator_RejectsMissingFields` — PickPolicy with `OnCommit: action.Action{}` (zero-value Kind). New returns error.
   - `TestValidator_RejectsPopOnOpen` — `OnCommit: pop`, `SyncStrategy: on_open`. New returns error mentioning "pop is incompatible with sync_strategy: on_open".
   - `TestValidator_WarnsRecycleOnDrain` — `OnCommit: recycle`, `SyncStrategy: on_drain`. New succeeds. Inspect the validator's returned `ValidationResult.Warnings` directly (call `validatePickPolicy` from the test). Verify Warnings contains the expected substring "inert".
   - `TestValidator_RejectsUnknownAction` — `OnCommit: action.Action{Kind: "nonsense"}`. Error.
   - `TestValidator_RejectsMalformedParameterizedAction` — uses `stores/common/action/yaml_test.go`'s shared helper or a dedicated YAML round-trip case. (Most malformed-shape rejection happens at the YAML level, not the validator level; this test ensures the validator doesn't accidentally accept malformed Actions that slip through.)
   - `TestValidator_RejectsCrossFilesystemTarget` — only runs on Linux/macOS where two filesystems can be assembled in a temp directory. Use `os.MkdirTemp` to produce a tmpfs+disk pair if the CI environment supports it; otherwise `t.Skip`. Verify cross-fs target produces "different filesystem" error.
   - `TestValidator_RejectsMissingTargetDirectory` — `OnCommit: pop_and_move(target=does/not/exist)`. Error.

2. Validator-direct tests call `validatePickPolicy` directly (same package; lower-cased function is reachable from `*_test.go` in the same package).

**Verification:**
```bash
go test ./stores/filesystem/store/ -run TestValidator_ -count=1
```

---

## Task 18: New fs-store common-pattern integration tests

**Files to touch:** `stores/filesystem/store/patterns_test.go` (new).

**Steps:**

1. Create per spec §10.5 (one end-to-end test per pattern in §8.1–§8.5):
   - `TestPattern_RingMode_LiveDiscovery` — `recycle + on_open`. Drive 3 cycles through 2 folders; midway add a 3rd folder externally; assert it gets picked up.
   - `TestPattern_QueueMode_AutoRefresh` — `pop + on_drain`. Drain N folders; assert exactly one Unavailable; verify next Open after that re-runs sync and re-picks.
   - `TestPattern_StagePromote` — `pop_and_move(target=promoted) + on_open`. Drive 3 commits; assert each folder moved to `promoted/`.
   - `TestPattern_OneShotIngest` — `pop_and_delete + on_drain`. Drain; verify folders gone.
   - `TestPattern_StaticQueue_ExplicitRefresh` — `pop + explicit`. Drain; verify sticky Unavailable. Trigger admin sync (existing endpoint at `/admin/...`); verify next Open picks up.

2. These exercise full Open→Commit cycles. Reuse the test scaffolding from existing tests.

**Verification:**
```bash
go test ./stores/filesystem/store/ -run TestPattern_ -count=1 -race
```

---

## Task 19: Update pg-store `PickPolicy` struct

**Files to touch:** `stores/postgres/store/store.go`.

**Steps:**

1. Add import: `"github.com/fallguyconsulting/rimsky/stores/common/action"`.
2. Locate `type PickPolicy struct` (line ~74). Replace fields:
   ```go
   type PickPolicy struct {
       ItemsTable        string
       OnCommit          action.Action
       OnGiveUp          action.Action
       VisibilityTimeout time.Duration
   }
   ```
3. Build to surface broken callers (applyPickAction etc.).

**Verification:**
```bash
go build ./stores/postgres/...
```
Will fail at applyPickAction; that's expected.

---

## Task 20: Add pg-store validator returning `ValidationResult`

**Files to touch:** `stores/postgres/store/store.go` (or new `validate.go`).

**Steps:**

1. Add validator with the rules from spec §6.1, §6.1b:

   ```go
   func validatePickPolicy(selector string, pp *PickPolicy) action.ValidationResult {
       var res action.ValidationResult
       addErr := func(err error) { res.Errors = append(res.Errors, err) }

       if pp == nil {
           addErr(errors.New("policy is nil"))
           return res
       }

       if !validIdent(pp.ItemsTable) {
           addErr(fmt.Errorf("items_table %q is not a valid identifier (lowercase letters/digits/underscore; not starting with a digit)", pp.ItemsTable))
       }

       if err := pp.OnCommit.Validate(); err != nil {
           addErr(fmt.Errorf("on_commit: %w", err))
       }
       if err := pp.OnGiveUp.Validate(); err != nil {
           addErr(fmt.Errorf("on_give_up: %w", err))
       }

       // pg-store-specific rejections per §6.1b.
       for _, slot := range []struct {
           name string
           a    action.Action
       }{
           {"on_commit", pp.OnCommit},
           {"on_give_up", pp.OnGiveUp},
       } {
           switch slot.a.Kind {
           case action.PopAndMove:
               addErr(fmt.Errorf("%s: action %q not supported by postgres store; supported actions are pop and recycle", slot.name, slot.a.Kind))
           case action.PopAndDelete:
               addErr(fmt.Errorf("%s: action %q not supported by postgres store (semantically equivalent to pop; use pop)", slot.name, slot.a.Kind))
           }
       }

       if pp.VisibilityTimeout <= 0 {
           addErr(errors.New("visibility_timeout_seconds: must be > 0"))
       }

       _ = selector
       return res
   }
   ```

2. Update the `New` constructor (line ~91) to consume `ValidationResult` per the same pattern as the fs-store's New (Task 7):

   ```go
   for selector, pp := range cfg.PickPolicies {
       res := validatePickPolicy(selector, pp)
       if !res.OK() {
           pool.Close()
           // Build error string.
           msgs := make([]string, 0, len(res.Errors))
           for _, e := range res.Errors {
               msgs = append(msgs, e.Error())
           }
           return nil, fmt.Errorf("postgres store: pick_policies[%q]: %s",
               selector, strings.Join(msgs, "; "))
       }
       for _, w := range res.Warnings {
           slog.Warn(w)
       }
       if err := verifyItemsTable(ctx, pool, pp.ItemsTable); err != nil {
           pool.Close()
           return nil, fmt.Errorf("postgres store: pick_policies[%q]: items table %q: %w",
               selector, pp.ItemsTable, err)
       }
   }
   ```

**Verification:**
```bash
go build ./stores/postgres/...
```

---

## Task 21: Update pg-store `applyPickAction` for new vocabulary

**Files to touch:** `stores/postgres/store/store.go`.

**Steps:**

1. Locate `applyPickAction` (line ~308) and the `validPickAction` helper (line ~423).

2. Update `applyPickAction` to consume `action.Action` rather than the legacy string. Today's code derives the action from `pp.OnCommitDefault` / `pp.OnGiveUpDefault` and switches on the string. Now derive from `pp.OnCommit` / `pp.OnGiveUp` and switch on `act.Kind`:

   ```go
   func (s *Store) applyPickAction(ctx context.Context, claimID string, successPath bool) error {
       if claimID == "" {
           return nil
       }
       pp, found := s.findPolicyForClaim(ctx, claimID)
       if !found {
           return nil
       }
       var act action.Action
       if successPath {
           act = pp.OnCommit
       } else {
           act = pp.OnGiveUp
       }

       tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
       if err != nil {
           return fmt.Errorf("postgres store: begin action tx: %w", err)
       }
       committed := false
       defer func() {
           if !committed {
               _ = tx.Rollback(ctx)
           }
       }()

       switch act.Kind {
       case action.Pop:
           // Replaces today's "delete" branch — same SQL.
           // <existing delete SQL preserved verbatim, with claim_token = $1 filter>
       case action.Recycle:
           // Replaces today's "release_to_back" branch — same SQL.
           // <existing release_to_back SQL preserved verbatim>
       case action.PopAndMove, action.PopAndDelete:
           // Defensive: validator should have rejected at config-load.
           return fmt.Errorf("postgres store: applyPickAction: action %q not supported by postgres store", act.Kind)
       default:
           return fmt.Errorf("postgres store: applyPickAction: invalid action %q", act.Kind)
       }

       if err := tx.Commit(ctx); err != nil {
           return fmt.Errorf("postgres store: commit action tx: %w", err)
       }
       committed = true
       return nil
   }
   ```

3. Read the existing SQL in the `case "delete":` and `case "release_to_back":` branches verbatim and lift each into the new `case action.Pop:` and `case action.Recycle:` blocks. Do NOT alter the SQL — only the case label changes.

4. Delete the `case "release_to_head":` branch entirely.

5. Delete or update `validPickAction` (line ~423):
   ```go
   func validPickAction(k action.Kind) bool {
       return k == action.Pop || k == action.Recycle
   }
   ```
   The validator at config-load is the primary gate; this helper becomes a defensive check, but the spec does not require it. Either keep as a sanity check or remove. Recommendation: keep, but call it from `applyPickAction` for defense-in-depth.

**Verification:**
```bash
go build ./stores/postgres/store/...
```

---

## Task 22: Update pg-store `cmd/main.go` — yamlPickPolicy struct and doc comment

**Files to touch:** `stores/postgres/cmd/main.go`.

**Steps:**

1. Locate `type yamlPickPolicy struct` (line ~71). Replace:
   ```go
   type yamlPickPolicy struct {
       ItemsTable               string         `yaml:"items_table"`
       OnCommit                 action.Action  `yaml:"on_commit"`
       OnGiveUp                 action.Action  `yaml:"on_give_up"`
       VisibilityTimeoutSeconds int            `yaml:"visibility_timeout_seconds"`
   }
   ```

2. Add the import: `"github.com/fallguyconsulting/rimsky/stores/common/action"`.

3. Update the conversion to `pgstore.PickPolicy` (line ~111):
   ```go
   policies[selector] = &pgstore.PickPolicy{
       ItemsTable:        pp.ItemsTable,
       OnCommit:          pp.OnCommit,
       OnGiveUp:          pp.OnGiveUp,
       VisibilityTimeout: time.Duration(pp.VisibilityTimeoutSeconds) * time.Second,
   }
   ```

4. Update the doc comment near the top of the file (around line 18) to use the new vocabulary:
   ```
   //	pick_policies:
   //	  "@docs-queue":
   //	    items_table: docs_queue
   //	    on_commit: pop
   //	    on_give_up: recycle
   //	    visibility_timeout_seconds: 1800
   ```

**Verification:**
```bash
go build ./stores/postgres/cmd/...
```

---

## Task 23: Update pg-store `server/observability.go` references

**Files to touch:** `stores/postgres/server/observability.go`.

**Steps:**

1. Lines 306–307 reference `pp.OnCommitDefault` and `pp.OnGiveUpDefault`. Update:
   ```go
   "on_commit":  string(pp.OnCommit.Kind),
   "on_give_up": string(pp.OnGiveUp.Kind),
   ```
2. Lines 316–317 reference the field names in a schema descriptor. Update to match the new keys.

**Verification:**
```bash
go build ./stores/postgres/...
```

---

## Task 24: Update existing pg-store tests in `stores/postgres/store/`

**Files to touch:** `stores/postgres/store/store_test.go`.

**Steps:**

1. Search-and-replace per the same pattern as Task 14:
   - `OnCommitDefault: "release_to_back"` → `OnCommit: action.Action{Kind: action.Recycle}`
   - `OnCommitDefault: "delete"` → `OnCommit: action.Action{Kind: action.Pop}`
   - `OnCommitDefault: "release_to_head"` → (no longer supported; pick `Recycle`)
   - same for `OnGiveUp*`

2. Add the import.

**Verification:**
```bash
go test ./stores/postgres/store/... -count=1
```

---

## Task 25: New pg-store action and validator tests

**Files to touch:** `stores/postgres/store/action_vocab_test.go` (new), `stores/postgres/store/validator_test.go` (new).

**Steps:**

1. Per spec §10.7. Use `pgtest` (testcontainers-backed real postgres). The existing `store_test.go` patterns show how to spin up an items table.

   Action tests:
   - `TestPGAction_Pop_RowDeleted` — Open → Commit with Pop. Assert: row gone from items table; subsequent claims don't pick the same row.
   - `TestPGAction_Recycle_RowReturnsToQueue` — Open → Commit with Recycle. Assert: row's `claim_token` cleared; next claim re-picks it.

   Validator tests:
   - `TestPGValidator_RejectsPopAndMove` — `OnCommit: action.Action{Kind: action.PopAndMove, MoveTarget: "x"}`. New returns error mentioning "not supported by postgres store".
   - `TestPGValidator_RejectsPopAndDelete` — same.
   - `TestPGValidator_RejectsOldNames` — `OnCommit: action.Action{Kind: "release_to_back"}` (constructed manually). Error.
   - `TestPGValidator_RejectsMissingFields` — zero-value Action.
   - `TestPGMigration_OldFieldNames` — YAML round-trip with `OnCommitDefault: release_to_back` produces a parser error pointing at `on_commit`. (Test via the cmd's loadYAML or by directly unmarshaling the yamlPickPolicy struct.)

**Verification:**
```bash
go test ./stores/postgres/store/ -run "TestPGAction_|TestPGValidator_|TestPGMigration_" -count=1
```

---

## Task 26: Cross-store consistency test

**Files to touch:** `stores/common/action/parity_test.go` (new).

**Steps:**

1. Create per spec §10.8:

   ```go
   package action

   import "testing"

   // TestSharedVocab_FsAndPgUseSameNames is a single-source-of-truth
   // assertion: the fs-store and pg-store both import action.Pop,
   // action.Recycle, etc. and rely on the constants here. This test
   // pins the string values so a rename here can't silently diverge
   // from existing test fixtures.
   func TestSharedVocab_FsAndPgUseSameNames(t *testing.T) {
       cases := []struct {
           kind Kind
           want string
       }{
           {Pop, "pop"},
           {PopAndMove, "pop_and_move"},
           {PopAndDelete, "pop_and_delete"},
           {Recycle, "recycle"},
       }
       for _, c := range cases {
           if string(c.kind) != c.want {
               t.Errorf("Kind %v: string value %q, want %q", c.kind, c.kind, c.want)
           }
       }
   }
   ```

**Verification:**
```bash
go test ./stores/common/action/... -count=1
```

---

## Task 27: Migrate `test/smoke/setup.go`

**Files to touch:** `test/smoke/setup.go`.

**Steps:**

1. Locate lines 102–103 with `OnCommitDefault: "release_to_back"` / `OnGiveUpDefault: "release_to_back"`. Replace with:
   ```go
   OnCommit: action.Action{Kind: action.Recycle},
   OnGiveUp: action.Action{Kind: action.Recycle},
   ```
2. Add import.

**Verification:**
```bash
go build ./test/smoke/...
go test ./test/smoke/... -count=1
```

---

## Task 28: Migrate `test/scenarios/*.go` test fixtures (top-level scenarios)

**Files to touch:** all files under `test/scenarios/` that reference `OnCommitDefault` or `OnGiveUpDefault`. Likely:

- `acquire_unavailable_pass_test.go`
- `acquire_unavailable_error_routing_test.go`
- `held_claim_acquirer_blocked_pass_test.go`
- `acquire_unavailable_retry_default_test.go`
- `reactive_loop_self_invalidate_in_frame_test.go`
- `held_claim_mixed_upstream_test.go`
- `reactive_loop_self_invalidate_next_frame_test.go`
- `held_claim_acquirer_passes_test.go`
- `acquire_pass_invalidate_emit_test.go`

**Steps:**

1. For each file, mechanically rewrite the legacy lines:
   - `OnCommitDefault: "delete"` → `OnCommit: action.Action{Kind: action.PopAndDelete}`
   - `OnGiveUpDefault: "release_to_back"` → `OnGiveUp: action.Action{Kind: action.Recycle}`
   (Other variants if present.)

2. Add the action import where needed.

3. Run `grep -rn "OnCommitDefault\|OnGiveUpDefault" test/scenarios/` to confirm zero remaining references.

**Verification:**
```bash
go test ./test/scenarios/... -count=1
```

---

## Task 29: Migrate `test/scenarios/stores/*.go` tests

**Files to touch:** all files under `test/scenarios/stores/` referencing the old vocabulary. Likely:

- `fs_pick_policy_basic_test.go`
- `fs_cross_queue_concurrency_test.go`
- `fs_pick_vs_scope_concurrency_test.go`

**Steps:**

1. Same mechanical rewrite as Task 28.
2. Update any prose comments mentioning `release_to_back` to reference `recycle`.

**Verification:**
```bash
go test ./test/scenarios/stores/... -count=1
```

---

## Task 30: Migrate in-tree YAML configs (filesystem-store)

**Files to touch:**
- `deploy/store-filesystem.yml`
- `modeling/cli/embedded/deploy/store-filesystem.yml`
- `stores/filesystem/config-example.yml`

**Steps:**

1. For each file, replace the legacy fields:
   - `on_commit_default: release_to_back` → `on_commit: recycle`
   - `on_commit_default: delete` → `on_commit: pop_and_delete`
   - `on_give_up_default: release_to_back` → `on_give_up: recycle`
   - `on_give_up_default: delete` → `on_give_up: pop_and_delete`
2. Remove any `on_commit_default: release_to_head` / `on_give_up_default: release_to_head` lines (no longer supported).

**Verification:**
Manual: read each updated file and confirm syntactic correctness. Also run `make build-all` to confirm any embedded-config tests pass:
```bash
make build-all
```

---

## Task 31: Migrate in-tree YAML configs (postgres-store)

**Files to touch:**
- `deploy/store-postgres.yml`
- `stores/postgres/config-example.yml`

**Steps:**

1. Same migration as Task 30, with the pg-specific mappings:
   - `on_commit_default: release_to_back` → `on_commit: recycle`
   - `on_commit_default: delete` → `on_commit: pop` (NOT `pop_and_delete`; pg uses `pop` per spec §7.2a)
   - `on_give_up_default: release_to_back` → `on_give_up: recycle`
   - `on_give_up_default: delete` → `on_give_up: pop`

**Verification:**
Manual review of each file.

---

## Task 32: Update `docs/concepts/` for the action vocabulary and patterns

**Files to touch:**
- `docs/concepts/claim-producer-fs-store.md` (existing or new)
- `docs/concepts/claim-producer-pg-store.md` (existing or new)
- Any other `docs/concepts/` page that references the old vocabulary

**Steps:**

1. Find existing claim-producer pages: `ls docs/concepts/`. If `claim-producer-fs-store.md` exists, update it; else create.
2. Document:
   - The four named actions and their semantics (pull from spec §3.1).
   - The per-store support matrix (spec §3.2).
   - For fs-store: the `sync_strategy` enum and the `drained` mechanism (spec §4–§5), plus the §8.1–§8.5 patterns.
   - For pg-store: the §8.6–§8.7 patterns.
3. Remove any references to `release_to_back` / `release_to_head` / `delete` from these pages.
4. Run `grep -rn "release_to_back\|release_to_head" docs/` and update any other pages.

**Verification:**
Manual review. No automated check.

---

## Task 33: Update CHANGELOG.md

**Files to touch:** `CHANGELOG.md`.

**Steps:**

1. Append a bullet under `## Unreleased`:

   ```markdown
   ### Pick-policy action vocabulary v2 (filesystem + postgres stores)

   Per `.ok-planner/specs/2026-05-06-fs-store-pick-policy-action-vocabulary-design.md`. Replaces the legacy `release_to_back | release_to_head | delete` vocabulary with the v2 named-action set across both bundled stores.

   - **New shared package `stores/common/action/`.** Defines the `Action` tagged-union type, the four action-name constants (`pop`, `pop_and_move`, `pop_and_delete`, `recycle`), the `ValidationResult` struct, and YAML unmarshal for the inline parameterized form. Both `stores/filesystem/` and `stores/postgres/` import this package.
   - **Filesystem store supports all four actions.** Plus a new `sync_strategy: on_drain` value (and a `drained` sentinel mechanism) that produces single-pass-then-refresh queue mode. The legacy `sync_strategy: on_sweep` is dropped (configs using it fail at config-load with the "must be on_open|on_drain|explicit|never" error).
   - **Postgres store supports `pop` and `recycle`.** `pop_and_move` and `pop_and_delete` rejected at config-load with "not supported by postgres store"; the items-table mechanism has no separate folder concept. Old `delete` migrates to `pop`; old `release_to_back` migrates to `recycle`.
   - **Migration:** pre-v1 break-cleanly. Old field names (`OnCommitDefault`, `OnGiveUpDefault`) and old action values (`release_to_back`, `release_to_head`, bare `delete`) are rejected at config-load with errors pointing at the new vocabulary. In-tree configs and tests have been updated.
   - **YAML shape:** inline parameterized action; `on_commit: pop` is a bare string, `on_commit: { pop_and_move: target }` is a one-key map. Parser rejects null, number, sequence, multi-key map, or empty-map shapes.
   - **Validator:** rejects bad combinations at config-load (e.g., `pop + sync_strategy: on_open` for fs-store; `pop_and_move` for pg-store) and warns on inert pairings (`recycle + sync_strategy: on_drain`). Returns a `ValidationResult{Errors, Warnings}` struct; warnings logged via package-level slog by the constructor.
   - **No proto change.** Strictly store-side. The reactive-loops + lifecycle-handlers work shipped 2026-05-05 stays the same.
   ```

**Verification:**
None automated; review for clarity.

---

## Task 34: Run full Go test suite + race-mode

**Files to touch:** none (verification only).

**Steps:**

1. From the repo root:
   ```bash
   go test ./... -count=1
   ```
2. Race-mode on the store and scenario paths:
   ```bash
   go test ./stores/... ./test/scenarios/... -race -count=3
   ```
3. The new `drained` race tests in particular:
   ```bash
   go test ./stores/filesystem/store/ -run TestOnDrain_RaceUnderConcurrentOpens -race -count=20
   ```

**Verification:**
All commands exit 0.

---

## Task 35: Run `make lint`

**Files to touch:** none (verification only).

**Steps:**

1. From repo root: `make lint`.
2. Fix any golangci-lint findings (gofmt, goimports, govet, staticcheck, unused, ineffassign, errcheck, revive).

**Verification:**
Exits 0.

---

## Task 36: Verify the docker-compose stack still reaches `/health`

**Files to touch:** none (verification only).

**Steps:**

1. Build the affected images:
   ```bash
   ./deploy/build-images.sh store-filesystem store-postgres
   ```
2. Bring up the stack:
   ```bash
   docker compose -f deploy/docker-compose.yml up -d
   ```
3. Wait for `/health` to return 200:
   ```bash
   curl -fsS http://localhost:8080/health
   ```
4. Tear down:
   ```bash
   docker compose -f deploy/docker-compose.yml down -v
   ```

**Verification:**
The curl exits 0. If it doesn't, the YAML config migration in Tasks 30–31 has a typo or the validator rejects something; debug.

---

## Task 37: Implementation notes file

**Files to touch:** `.ok-planner/plans/2026-05-06-stores-pick-policy-action-vocabulary-notes.md` (new; the execute-plan-complete skill creates this for you on first dispatch — but record any deviations as you go).

**Steps:**

1. Create the notes file (if not already created by the execute skill).
2. As you work through the plan, append entries for:
   - Any deviation from the plan (and why).
   - Any pre-existing bugs you discovered and fixed (per the project's "Fix Every Bug You Find" rule).
   - Any test flakes or `-race` findings.
   - Any docker-compose surprises.
   - Any cross-package import conflicts surfaced by depguard.

**Verification:** none — this is a working artifact, not a code change.

---

## Manual checks after completion

These items require human review and are not part of the automated run.

- **Operator-runbook spot-check.** Read the updated `docs/concepts/claim-producer-{fs,pg}-store.md` pages. The §8 patterns table (per spec) should be visible to an operator looking for "how do I configure queue mode."
- **CLAUDE.md spot-check.** If any non-obvious decision in this plan would trip up a future session (e.g., the `stores/common/action/` package as a new sub-tree, or the `drained` sentinel as a new state file), add a one-line note to CLAUDE.md's "Non-obvious gotchas" section.
- **Postgres-store SQL parity confirmation.** The plan says "lift the existing SQL verbatim into the new case labels." Confirm during code review that the SQL is byte-equivalent — no accidental rewrite of the WHERE clause, no parameter-numbering drift.
- **Visual review of any docker-compose / quickstart paths the operator would touch.** A blank `make quickstart` run end-to-end is the operator's smoke test.
