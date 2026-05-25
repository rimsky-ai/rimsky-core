# FS-Store Pick-Policies Implementation Plan

**Goal:** Add pick-policy support to the standard filesystem store-service (`stores/filesystem/`), with auto-discovery of folders under a configured sub-root, three actions (`release_to_back`, `release_to_head`, `delete`), a visibility-timeout sweep, and a `bump-to-head` admin HTTP endpoint. No rimsky-side code changes.

**Architecture:** The fs store gains the same dual-mode shape the pg store already has. `Open` dispatches on selector: `@policy-name` keys hit the new pick-policy path; everything else falls through to the existing regional path. Pick-policy state lives at `<store-root>/.fs-store/<policy>/{available,in_progress}/` as sentinel files. Atomic claim is `rename(2)` between the two directories. Auto-discovery reconciles `available/` against `readdir(<sub-root>)` on every `Open` (or every sweep tick, configurable).

**Tech Stack:** Go 1.22+, `gopkg.in/yaml.v3` (existing), stdlib only for the new logic. Tests use stdlib `testing` + `t.TempDir()`. Integration tests use the existing `core/internal/pgtest` testcontainers fixture for the rimsky-side Postgres.

**Spec reference:** `docs/specs/2026-05-03-fs-store-pick-policies-design.md`. The plan implements the spec in full; sections of the spec are referenced by name in the relevant tasks.

**Project rules in effect:**
- Pre-v1: schema/wire breakage allowed; no compat shims.
- Cold-read conventions (`.claude/rules/cold-read-cheatsheet.md`): one feature per file, `~500` line file / `~100` line function guideline, max 3 levels of nesting via early returns.
- After-code-changes verification: `go build ./...`, `go test ./...`, `make lint`.
- All new Go code uses stdlib `log/slog` for logging.

---

## File map

**Modified:**

| Path | Change |
|---|---|
| `stores/filesystem/store/store.go` | Add `PickPolicy` type, `Config` struct, dispatch in `Open`/`Commit`/`Abandon`, bootstrap `MkdirAll`, regex compilation. |
| `stores/filesystem/server/server.go` | Take `Config` by value (instead of `Root` only), wire admin listener (5th arg to `Run`), thread sweep interval into `st.RunSweep`. |
| `stores/filesystem/cmd/main.go` | Extend `yamlConfig` with `pick_policies`, `admin_port`, `sweep_interval_seconds`. Build `Config`, open admin listener. |
| `stores/filesystem/testfixture/testfixture.go` | Take an optional `Config` (or builder), return `(grpcEndpoint, adminEndpoint, teardown)`. |
| `stores/filesystem/store/store_test.go` | Add unit tests for sync, pick, actions, sweep, multi-policy, folder-pattern filter, terminal idempotency, region-byte equality, selector dispatch. |
| `docs/operator-guide.md` | Add fs pick-policies subsection paralleling the existing pg one. |
| `docs/glossary.md` | Extend pick-policy + action entries to note fs as supporting store. |
| `CHANGELOG.md` | Entry under `## Unreleased`. |

**New:**

| Path | Purpose |
|---|---|
| `stores/filesystem/store/pick_policy.go` | All pick-policy logic: `openPickPolicy`, `runSync`, `findByClaimID`, `applyPickAction`, `parseFromRight`, the three action handlers. Splits the file so `store.go` stays focused on the regional path + dispatch. |
| `stores/filesystem/store/sweep.go` | `RunSweep` + `sweepOnce`. Mirrors the pg store's `sweep.go` shape. |
| `stores/filesystem/store/admin.go` | `AdminHandler` with the `POST /admin/bump-to-head/{selector}` route. |
| `stores/filesystem/store/pick_policy_test.go` | Concurrent-pick test, sync round-trip, action handlers, sweep, folder-pattern, multi-policy, terminal idempotency. (Unit tests that lean heavily on pick-policy internals; the existing `store_test.go` keeps the regional-path tests it has today.) |
| `test/scenarios/stores/fs_pick_policy_basic_test.go` | End-to-end via gRPC: ring cycles through three folders. |
| `test/scenarios/stores/fs_cross_queue_concurrency_test.go` | Two policies overlapping; loser conflicts via region match and recycles. |
| `test/scenarios/stores/fs_pick_vs_regional_concurrency_test.go` | Pick-policy claim holds; subsequent regional claim on same folder blocks until commit. |

---

## Tasks

### Task 1: Add `PickPolicy`, `Config`, `Selector` types and threaded config plumbing

**Files:** `stores/filesystem/store/store.go`

**Steps:**

1. At the top of `stores/filesystem/store/store.go`, after the existing imports, add the new types:

   ```go
   // PickPolicy is one configured pick policy. Store-internal.
   //
   // Auto-discovery is the only insertion mechanism: the sync step
   // reconciles <store-root>/<Root>/* against the available/ sentinel
   // set. Queue-vs-ring vs single-shot drain is emergent from
   // OnCommitDefault / OnGiveUpDefault.
   type PickPolicy struct {
       Root              string         // relative path under store root
       FolderPattern     *regexp.Regexp // nil means "no extra filter beyond skip-leading-dot"
       OnCommitDefault   string         // "release_to_back" | "release_to_head" | "delete"
       OnGiveUpDefault   string         // same vocabulary
       VisibilityTimeout time.Duration
       SyncStrategy      string // "on_open" | "on_sweep"
   }

   // Config is the store-internal config struct. cmd/main.go and
   // testfixture/ both build it. SweepInterval is intentionally NOT a
   // field here — sweep cadence is owned by the server package and
   // passed to RunSweep directly. Keeping a single source of truth.
   type Config struct {
       Root         string
       PickPolicies map[string]*PickPolicy
   }
   ```

2. Add the new imports (`os`, `regexp`, `time`) to the import block in `store.go`. (`os` is required for `os.MkdirAll` in step 4.)

3. Modify the `Store` struct to carry pick-policy state:

   ```go
   type Store struct {
       root         string
       pickPolicies map[string]*PickPolicy
       mu           sync.Mutex
       claims       map[string]string
   }
   ```

4. Replace the existing `New(root string) (*Store, error)` with `New(cfg Config) (*Store, error)`:

   ```go
   func New(cfg Config) (*Store, error) {
       if cfg.Root == "" {
           return nil, errors.New("filesystem store: root must not be empty")
       }
       for selector, pp := range cfg.PickPolicies {
           if err := validatePickPolicy(cfg.Root, selector, pp); err != nil {
               return nil, fmt.Errorf("filesystem store: pick_policies[%q]: %w", selector, err)
           }
           // Idempotent state-directory creation.
           dir := filepath.Join(cfg.Root, ".fs-store", trimAtPrefix(selector))
           for _, sub := range []string{"available", "in_progress"} {
               if err := os.MkdirAll(filepath.Join(dir, sub), 0o755); err != nil {
                   return nil, fmt.Errorf("filesystem store: mkdir %s: %w", filepath.Join(dir, sub), err)
               }
           }
       }
       return &Store{
           root:         cfg.Root,
           pickPolicies: cfg.PickPolicies,
           claims:       make(map[string]string),
       }, nil
   }

   // trimAtPrefix returns the policy directory name corresponding to a selector.
   // "@docs-ring" → "docs-ring"; selectors without a leading "@" are used verbatim.
   func trimAtPrefix(selector string) string {
       if strings.HasPrefix(selector, "@") {
           return selector[1:]
       }
       return selector
   }
   ```

   `validatePickPolicy` is defined in Task 2.

5. Update every existing `New(...)` call site in `stores/filesystem/store/store_test.go` from the old single-arg form to the new `Config`-based form. The existing file has ten call sites (use `grep -n "New(" stores/filesystem/store/store_test.go` to locate). Rewrite each:

   - `New("")` → `New(Config{Root: ""})`
   - `New(t.TempDir())` → `New(Config{Root: t.TempDir()})`
   - `New(root)` (where `root` is a string variable) → `New(Config{Root: root})`

   This is a mechanical rewrite; no test logic changes. Without this step, every subsequent `go test ./stores/filesystem/store/...` verification command fails on test-file compile, even when `-run` filters out the affected tests (Go compiles all `*_test.go` in a package before applying `-run`).

**Verification:**

```sh
go build ./stores/filesystem/...
go vet ./stores/filesystem/store/
```

`go build` will error from `validatePickPolicy` not yet existing — that's expected; Task 2 adds it. `go vet` is harmless to run; if it complains about unused imports, remove any that turn out unused after the refactor (likely the case if `New`'s body no longer uses some helper).

To confirm test-file compile: after Task 2 lands, run `go test -count=1 -run NONE ./stores/filesystem/store/...` — this compiles every test file without running any test, and is the cheapest way to verify the test-file rewrites in step 5 are syntactically correct.

---

### Task 2: Add `validatePickPolicy` plus YAML config schema in `cmd/main.go`

**Files:** `stores/filesystem/store/store.go`, `stores/filesystem/cmd/main.go`

**Steps:**

1. In `stores/filesystem/store/store.go`, add `validatePickPolicy` (place it near `validIdent` if that helper still exists, or near the bottom of the file):

   ```go
   func validatePickPolicy(storeRoot, selector string, pp *PickPolicy) error {
       if pp == nil {
           return errors.New("policy is nil")
       }
       if pp.Root == "" {
           return errors.New("root: required")
       }
       if filepath.IsAbs(pp.Root) {
           return fmt.Errorf("root: %q is absolute; must be relative to store root", pp.Root)
       }
       cleaned := filepath.Clean(pp.Root)
       if cleaned == ".." || strings.HasPrefix(cleaned, ".."+string(filepath.Separator)) {
           return fmt.Errorf("root: %q escapes the store root", pp.Root)
       }
       absPath := filepath.Join(storeRoot, cleaned)
       // Defense-in-depth containment check: ensure the joined path
       // actually lives under storeRoot, catching symlink edge cases
       // (mirrors openRegional's existing filepath.Rel check).
       rel, relErr := filepath.Rel(storeRoot, absPath)
       if relErr != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
           return fmt.Errorf("root: %q resolves to %q which escapes the store root", pp.Root, absPath)
       }
       info, err := os.Stat(absPath)
       if err != nil {
           return fmt.Errorf("root: stat %s: %w", absPath, err)
       }
       if !info.IsDir() {
           return fmt.Errorf("root: %s is not a directory", absPath)
       }
       // Readability probe.
       if _, err := os.ReadDir(absPath); err != nil {
           return fmt.Errorf("root: %s not readable: %w", absPath, err)
       }
       // Writability probe via temp-file create + remove.
       probe, err := os.CreateTemp(absPath, ".rimsky-fs-store-probe-*")
       if err != nil {
           return fmt.Errorf("root: %s not writable: %w", absPath, err)
       }
       probeName := probe.Name()
       _ = probe.Close()
       _ = os.Remove(probeName)
       switch pp.OnCommitDefault {
       case "release_to_back", "release_to_head", "delete":
       default:
           return fmt.Errorf("on_commit_default: must be release_to_back|release_to_head|delete, got %q", pp.OnCommitDefault)
       }
       switch pp.OnGiveUpDefault {
       case "release_to_back", "release_to_head", "delete":
       default:
           return fmt.Errorf("on_give_up_default: must be release_to_back|release_to_head|delete, got %q", pp.OnGiveUpDefault)
       }
       if pp.VisibilityTimeout <= 0 {
           return errors.New("visibility_timeout_seconds: must be > 0")
       }
       switch pp.SyncStrategy {
       case "", "on_open", "on_sweep":
           if pp.SyncStrategy == "" {
               pp.SyncStrategy = "on_open"
           }
       default:
           return fmt.Errorf("sync_strategy: must be on_open|on_sweep, got %q", pp.SyncStrategy)
       }
       return nil
   }
   ```

2. In `stores/filesystem/cmd/main.go`, replace the existing `yamlConfig` and `loadYAML` and the `main` body to thread pick policies through:

   ```go
   type yamlConfig struct {
       Root                 string                    `yaml:"root"`
       Host                 string                    `yaml:"host"`
       GRPCPort             int                       `yaml:"grpc_port"`
       HTTPPort             int                       `yaml:"http_port"`
       AdminPort            int                       `yaml:"admin_port"`
       PickPolicies         map[string]yamlPickPolicy `yaml:"pick_policies"`
       SweepIntervalSeconds int                       `yaml:"sweep_interval_seconds"`
   }

   type yamlPickPolicy struct {
       Root                     string `yaml:"root"`
       FolderPattern            string `yaml:"folder_pattern"`
       OnCommitDefault          string `yaml:"on_commit_default"`
       OnGiveUpDefault          string `yaml:"on_give_up_default"`
       VisibilityTimeoutSeconds int    `yaml:"visibility_timeout_seconds"`
       SyncStrategy             string `yaml:"sync_strategy"`
   }
   ```

3. In the same file, modify `main` to build a `fsstore.Config` and pass it to `server.Run`:

   ```go
   policies := make(map[string]*fsstore.PickPolicy, len(cfg.PickPolicies))
   for selector, pp := range cfg.PickPolicies {
       var pat *regexp.Regexp
       if pp.FolderPattern != "" {
           p, err := regexp.Compile(pp.FolderPattern)
           if err != nil {
               fmt.Fprintf(os.Stderr, "store-filesystem: pick_policies[%q].folder_pattern: %v\n", selector, err)
               os.Exit(1)
           }
           pat = p
       }
       policies[selector] = &fsstore.PickPolicy{
           Root:              pp.Root,
           FolderPattern:     pat,
           OnCommitDefault:   pp.OnCommitDefault,
           OnGiveUpDefault:   pp.OnGiveUpDefault,
           VisibilityTimeout: time.Duration(pp.VisibilityTimeoutSeconds) * time.Second,
           SyncStrategy:      pp.SyncStrategy,
       }
   }
   sweepInterval := time.Duration(cfg.SweepIntervalSeconds) * time.Second
   if sweepInterval == 0 {
       sweepInterval = 60 * time.Second
   }
   ```

   Then enforce the spec rule "admin_port required when any pick policy is configured" and open the optional admin listener:

   ```go
   if len(policies) > 0 && cfg.AdminPort == 0 {
       fmt.Fprintf(os.Stderr, "store-filesystem: admin_port is required when pick_policies is configured\n")
       os.Exit(1)
   }
   var adminLis net.Listener
   if cfg.AdminPort > 0 {
       adminLis, err = net.Listen("tcp", fmt.Sprintf("%s:%d", host, cfg.AdminPort))
       if err != nil {
           fmt.Fprintf(os.Stderr, "store-filesystem: admin listen: %v\n", err)
           os.Exit(1)
       }
   }
   ```

   And replace the `server.Run` call to pass `Config{...}`, `grpcLis`, `httpLis`, `adminLis`. The signature change to `server.Run` happens in Task 8; until that lands the build will fail here — that's expected.

4. Add the new imports (`regexp`, `time`, `fsstore "github.com/fallguyconsulting/rimsky/stores/filesystem/store"`) to `main.go`'s import block.

**Verification:**

```sh
go build ./stores/filesystem/store/...
```

Expect `cmd/main.go` to fail to build (`server.Run` signature mismatch); the store package itself should build cleanly. Validate by running:

```sh
go vet ./stores/filesystem/store/...
```

Expect zero output (clean) for the store package.

---

### Task 3: Sentinel filename grammar — `parseFromRight` helper + tests

**Files:** `stores/filesystem/store/pick_policy.go` (new), `stores/filesystem/store/pick_policy_test.go` (new)

**Steps:**

1. Create `stores/filesystem/store/pick_policy.go` with package preamble and the parser:

   ```go
   // Package store: pick-policy logic. Per docs/specs/2026-05-03-
   // fs-store-pick-policies-design.md. Auto-discovery + rename-based
   // atomic claim. Sentinels live at <store-root>/.fs-store/<policy>/
   // {available,in_progress}/.
   //
   // In-progress sentinel filename: <folder>.<claim_id>.<claimed_nanos>.
   // Parsed from the right because folder names may contain dots
   // (e.g., "my.docs"); claim_id (UUID) and claimed_nanos (digits-only)
   // contain no dots.

   package store

   import (
       "errors"
       "strconv"
       "strings"
   )

   // parseFromRight splits an in-progress sentinel filename into
   // (folder, claim_id, claimed_nanos). The two rightmost dot-separators
   // are claim_id and claimed_nanos; everything before is the folder.
   //
   // claimed_nanos must parse as a non-negative int64 (the typical
   // time.Now().UnixNano() value). If it doesn't, the entry is treated
   // as malformed and parseFromRight returns an error so callers can
   // skip it without misinterpreting it.
   func parseFromRight(name string) (folder, claimID string, claimedNanos int64, err error) {
       lastDot := strings.LastIndexByte(name, '.')
       if lastDot < 0 {
           return "", "", 0, errors.New("missing claimed_nanos suffix")
       }
       nanosStr := name[lastDot+1:]
       prev := strings.LastIndexByte(name[:lastDot], '.')
       if prev < 0 {
           return "", "", 0, errors.New("missing claim_id suffix")
       }
       n, parseErr := strconv.ParseInt(nanosStr, 10, 64)
       if parseErr != nil {
           return "", "", 0, errors.New("claimed_nanos is not an integer")
       }
       if n < 0 {
           return "", "", 0, errors.New("claimed_nanos is negative")
       }
       return name[:prev], name[prev+1 : lastDot], n, nil
   }
   ```

2. Create `stores/filesystem/store/pick_policy_test.go` with parser tests:

   ```go
   package store

   import "testing"

   func TestParseFromRight(t *testing.T) {
       cases := []struct {
           name        string
           input       string
           wantFolder  string
           wantClaim   string
           wantNanos   int64
           wantErr     bool
       }{
           {"simple", "area-a.uuid-1.1730000000000000000",
               "area-a", "uuid-1", 1730000000000000000, false},
           {"dotted_folder", "my.docs.uuid-2.1730000000000000001",
               "my.docs", "uuid-2", 1730000000000000001, false},
           {"deep_dotted_folder", "a.b.c.uuid-3.1730000000000000002",
               "a.b.c", "uuid-3", 1730000000000000002, false},
           {"missing_nanos", "area-a-uuid",
               "", "", 0, true},
           {"only_one_dot", "area-a.uuid",
               "", "", 0, true},
           {"non_numeric_nanos", "area-a.uuid.abc",
               "", "", 0, true},
       }
       for _, c := range cases {
           t.Run(c.name, func(t *testing.T) {
               folder, claim, nanos, err := parseFromRight(c.input)
               if c.wantErr {
                   if err == nil {
                       t.Fatalf("expected error for %q, got nil", c.input)
                   }
                   return
               }
               if err != nil {
                   t.Fatalf("unexpected error: %v", err)
               }
               if folder != c.wantFolder || claim != c.wantClaim || nanos != c.wantNanos {
                   t.Errorf("got (%q,%q,%d), want (%q,%q,%d)",
                       folder, claim, nanos, c.wantFolder, c.wantClaim, c.wantNanos)
               }
           })
       }
   }
   ```

**Verification:**

```sh
go test ./stores/filesystem/store/ -run TestParseFromRight -count=1
```

Expect: PASS.

---

### Task 4: Sync step (`runSync`) + unit tests

**Files:** `stores/filesystem/store/pick_policy.go`, `stores/filesystem/store/pick_policy_test.go`

**Steps:**

1. Append to `stores/filesystem/store/pick_policy.go`:

   ```go
   // policyStateDir returns the absolute path to .fs-store/<policy>/.
   // selector is the configured selector key (e.g. "@docs-ring"); the
   // directory name strips the leading "@".
   func policyStateDir(storeRoot, selector string) string {
       return filepath.Join(storeRoot, ".fs-store", trimAtPrefix(selector))
   }

   // runSync reconciles available/ against readdir(<store-root>/<policy.Root>/).
   // Idempotent and concurrency-safe (O_CREAT|O_EXCL for inserts;
   // ENOENT-tolerant for deletes).
   func (s *Store) runSync(selector string, pp *PickPolicy) error {
       subRoot := filepath.Join(s.root, pp.Root)
       state := policyStateDir(s.root, selector)
       availDir := filepath.Join(state, "available")
       inProgDir := filepath.Join(state, "in_progress")

       extantEntries, err := os.ReadDir(subRoot)
       if err != nil {
           return fmt.Errorf("readdir %s: %w", subRoot, err)
       }
       extant := make(map[string]struct{}, len(extantEntries))
       for _, e := range extantEntries {
           name := e.Name()
           if !e.IsDir() {
               continue
           }
           if strings.HasPrefix(name, ".") {
               continue
           }
           if pp.FolderPattern != nil && !pp.FolderPattern.MatchString(name) {
               continue
           }
           extant[name] = struct{}{}
       }

       availEntries, err := os.ReadDir(availDir)
       if err != nil {
           return fmt.Errorf("readdir %s: %w", availDir, err)
       }
       avail := make(map[string]struct{}, len(availEntries))
       for _, e := range availEntries {
           avail[e.Name()] = struct{}{}
       }

       inProgEntries, err := os.ReadDir(inProgDir)
       if err != nil {
           return fmt.Errorf("readdir %s: %w", inProgDir, err)
       }
       tracked := make(map[string]struct{}, len(avail)+len(inProgEntries))
       for k := range avail {
           tracked[k] = struct{}{}
       }
       for _, e := range inProgEntries {
           folder, _, _, perr := parseFromRight(e.Name())
           if perr != nil {
               continue
           }
           tracked[folder] = struct{}{}
       }

       // Add brand-new folders.
       for folder := range extant {
           if _, ok := tracked[folder]; ok {
               continue
           }
           f, err := os.OpenFile(filepath.Join(availDir, folder),
               os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
           if err == nil {
               _ = f.Close()
               continue
           }
           if !errors.Is(err, fs.ErrExist) {
               return fmt.Errorf("create available sentinel %s: %w", folder, err)
           }
           // EEXIST: concurrent sync added it. Ignore.
       }

       // Remove stale: folder gone from disk but still has an available sentinel.
       for folder := range avail {
           if _, ok := extant[folder]; ok {
               continue
           }
           if err := os.Remove(filepath.Join(availDir, folder)); err != nil && !errors.Is(err, fs.ErrNotExist) {
               return fmt.Errorf("unlink stale available sentinel %s: %w", folder, err)
           }
       }
       return nil
   }
   ```

2. Add `os`, `io/fs`, `path/filepath`, `errors`, `fmt`, `strings` to the import block of `pick_policy.go`.

3. Append a unit test in `pick_policy_test.go`:

   ```go
   func TestRunSyncReconciles(t *testing.T) {
       root := t.TempDir()
       sub := "documents"
       if err := os.MkdirAll(filepath.Join(root, sub), 0o755); err != nil {
           t.Fatal(err)
       }
       for _, name := range []string{"area-a", "area-b", ".hidden"} {
           if err := os.MkdirAll(filepath.Join(root, sub, name), 0o755); err != nil {
               t.Fatal(err)
           }
       }
       pp := &PickPolicy{
           Root:              sub,
           OnCommitDefault:   "release_to_back",
           OnGiveUpDefault:   "release_to_back",
           VisibilityTimeout: time.Minute,
           SyncStrategy:      "on_open",
       }
       st, err := New(Config{
           Root:         root,
           PickPolicies: map[string]*PickPolicy{"@ring": pp},
       })
       if err != nil {
           t.Fatalf("New: %v", err)
       }
       if err := st.runSync("@ring", pp); err != nil {
           t.Fatalf("runSync: %v", err)
       }
       availDir := filepath.Join(root, ".fs-store", "ring", "available")
       entries, _ := os.ReadDir(availDir)
       got := make(map[string]bool)
       for _, e := range entries {
           got[e.Name()] = true
       }
       if !got["area-a"] || !got["area-b"] {
           t.Errorf("expected available/{area-a,area-b}, got %v", got)
       }
       if got[".hidden"] {
           t.Errorf("leading-dot folder should be filtered, got sentinel for it")
       }
       if len(entries) != 2 {
           t.Errorf("expected exactly 2 sentinels (area-a, area-b), got %d: %v", len(entries), got)
       }

       // Remove area-a and re-sync; expect its sentinel to be unlinked.
       if err := os.RemoveAll(filepath.Join(root, sub, "area-a")); err != nil {
           t.Fatal(err)
       }
       if err := st.runSync("@ring", pp); err != nil {
           t.Fatalf("runSync (after rm): %v", err)
       }
       entries, _ = os.ReadDir(availDir)
       got = make(map[string]bool)
       for _, e := range entries {
           got[e.Name()] = true
       }
       if got["area-a"] {
           t.Errorf("removed folder still has a sentinel: %v", got)
       }
       if !got["area-b"] {
           t.Errorf("untouched sentinel disappeared: %v", got)
       }
   }
   ```

   Add `time` and `path/filepath` to test-file imports as needed.

**Verification:**

```sh
go test ./stores/filesystem/store/ -run TestRunSyncReconciles -count=1
```

Expect: PASS.

---

### Task 5: `openPickPolicy`, `Open` dispatch, region/address pinning

**Files:** `stores/filesystem/store/pick_policy.go`, `stores/filesystem/store/store.go`, `stores/filesystem/store/pick_policy_test.go`

**Steps:**

1. Append to `pick_policy.go`:

   ```go
   // openPickPolicy runs sync (per policy.SyncStrategy) and attempts the
   // rename-as-claim. Returns OpenOutcome{Available: false} on empty queue.
   func (s *Store) openPickPolicy(claimID, selector string, pp *PickPolicy) (corestore.OpenOutcome, error) {
       if pp.SyncStrategy == "" || pp.SyncStrategy == "on_open" {
           if err := s.runSync(selector, pp); err != nil {
               return corestore.OpenOutcome{}, fmt.Errorf("filesystem store: sync: %w", err)
           }
       }
       state := policyStateDir(s.root, selector)
       availDir := filepath.Join(state, "available")
       inProgDir := filepath.Join(state, "in_progress")

       entries, err := os.ReadDir(availDir)
       if err != nil {
           return corestore.OpenOutcome{}, fmt.Errorf("filesystem store: readdir available: %w", err)
       }
       // Sort by mtime ascending; lexical tiebreaker.
       sort.Slice(entries, func(i, j int) bool {
           ii, _ := entries[i].Info()
           jj, _ := entries[j].Info()
           if ii != nil && jj != nil && !ii.ModTime().Equal(jj.ModTime()) {
               return ii.ModTime().Before(jj.ModTime())
           }
           return entries[i].Name() < entries[j].Name()
       })

       nowNanos := time.Now().UnixNano()
       for _, entry := range entries {
           folder := entry.Name()
           src := filepath.Join(availDir, folder)
           dst := filepath.Join(inProgDir, fmt.Sprintf("%s.%s.%d", folder, claimID, nowNanos))
           if err := os.Rename(src, dst); err != nil {
               if errors.Is(err, fs.ErrNotExist) {
                   continue // raced; try next
               }
               return corestore.OpenOutcome{}, fmt.Errorf("filesystem store: claim rename: %w", err)
           }
           subPath := filepath.Join(pp.Root, folder)
           absPath := filepath.Join(s.root, subPath)
           addr, err := json.Marshal(absPath)
           if err != nil {
               return corestore.OpenOutcome{}, err
           }
           region, err := json.Marshal(subPath)
           if err != nil {
               return corestore.OpenOutcome{}, err
           }
           payload, err := json.Marshal(map[string]string{"folder": folder})
           if err != nil {
               return corestore.OpenOutcome{}, err
           }
           s.mu.Lock()
           s.claims[claimID] = absPath
           s.mu.Unlock()
           return corestore.OpenOutcome{
               Available: true,
               Result: corestore.ClaimResult{
                   Address: json.RawMessage(addr),
                   Payload: json.RawMessage(payload),
                   Region:  json.RawMessage(region),
               },
           }, nil
       }
       return corestore.OpenOutcome{Available: false}, nil
   }
   ```

   Add `sort`, `time`, `encoding/json`, plus `corestore "github.com/fallguyconsulting/rimsky/core/store"` to `pick_policy.go` imports.

2. In `stores/filesystem/store/store.go`, modify `Open` to dispatch:

   ```go
   func (s *Store) Open(_ context.Context, claimID, selector string) (corestore.OpenOutcome, error) {
       if pp, ok := s.pickPolicies[selector]; ok {
           // Pick-policy selectors are a configured map-key match — they
           // intentionally bypass openRegional's glob-metacharacter
           // rejection. Operators choose the selector key (convention:
           // `@policy-name`); a key containing `*`/`?`/`[` is operator
           // misconfiguration but doesn't violate v3's "concrete paths
           // only" rule, which governs the selector-as-path
           // interpretation that openRegional implements.
           return s.openPickPolicy(claimID, selector, pp)
       }
       return s.openRegional(claimID, selector)
   }
   ```

   Rename the existing body of `Open` (the regional path) to `openRegional(claimID, selector)`. The signature changes to drop the `ctx` parameter (since the existing body never used it; `Open` keeps `ctx` for the interface).

   The existing `Open` returns `(store.OpenOutcome, error)` referencing the local-package alias `store`; rename to `corestore` consistently across the file (the package was previously imported as `"github.com/fallguyconsulting/rimsky/core/store"` aliased as `store`; rename the alias to `corestore` to match `pick_policy.go`).

3. Append unit tests in `pick_policy_test.go`:

   ```go
   func TestOpenPickPolicy_Basic(t *testing.T) {
       root := t.TempDir()
       sub := "docs"
       must(t, os.MkdirAll(filepath.Join(root, sub, "alpha"), 0o755))
       must(t, os.MkdirAll(filepath.Join(root, sub, "beta"), 0o755))
       pp := &PickPolicy{
           Root:              sub,
           OnCommitDefault:   "release_to_back",
           OnGiveUpDefault:   "release_to_back",
           VisibilityTimeout: time.Minute,
           SyncStrategy:      "on_open",
       }
       st, err := New(Config{
           Root:         root,
           PickPolicies: map[string]*PickPolicy{"@docs-ring": pp},
       })
       if err != nil {
           t.Fatalf("New: %v", err)
       }
       outcome, err := st.Open(context.Background(), "claim-1", "@docs-ring")
       if err != nil {
           t.Fatalf("Open: %v", err)
       }
       if !outcome.Available {
           t.Fatal("expected Available, got Unavailable")
       }
       var addr, region string
       must(t, json.Unmarshal(outcome.Result.Address, &addr))
       must(t, json.Unmarshal(outcome.Result.Region, &region))
       wantAddr := filepath.Join(root, sub, "alpha")
       wantRegion := filepath.Join(sub, "alpha")
       if addr != wantAddr {
           t.Errorf("address = %q, want %q", addr, wantAddr)
       }
       if region != wantRegion {
           t.Errorf("region = %q, want %q", region, wantRegion)
       }
   }

   func TestOpenPickPolicy_EmptyQueueReturnsUnavailable(t *testing.T) {
       root := t.TempDir()
       must(t, os.MkdirAll(filepath.Join(root, "docs"), 0o755))
       pp := &PickPolicy{
           Root: "docs", OnCommitDefault: "release_to_back",
           OnGiveUpDefault: "release_to_back", VisibilityTimeout: time.Minute,
       }
       st, err := New(Config{Root: root, PickPolicies: map[string]*PickPolicy{"@r": pp}})
       must(t, err)
       outcome, err := st.Open(context.Background(), "c", "@r")
       must(t, err)
       if outcome.Available {
           t.Fatal("expected Unavailable on empty queue")
       }
   }

   func TestOpenSelectorDispatch(t *testing.T) {
       root := t.TempDir()
       sub := "docs"
       must(t, os.MkdirAll(filepath.Join(root, sub, "alpha"), 0o755))
       pp := &PickPolicy{
           Root: sub, OnCommitDefault: "release_to_back",
           OnGiveUpDefault: "release_to_back", VisibilityTimeout: time.Minute,
       }
       st, err := New(Config{Root: root, PickPolicies: map[string]*PickPolicy{"@docs-ring": pp}})
       must(t, err)
       // Pick-policy path
       o1, _ := st.Open(context.Background(), "c1", "@docs-ring")
       if !o1.Available {
           t.Fatal("pick-policy selector should be Available")
       }
       // Regional path
       o2, _ := st.Open(context.Background(), "c2", "docs/alpha")
       if !o2.Available {
           t.Fatal("regional selector should be Available")
       }
       // Region bytes must be byte-equal.
       if string(o1.Result.Region) != string(o2.Result.Region) {
           t.Errorf("pick-policy region (%s) != regional region (%s) for same logical folder",
               o1.Result.Region, o2.Result.Region)
       }
   }

   func TestOpenPickPolicy_ConcurrentPicksAreUnique(t *testing.T) {
       root := t.TempDir()
       sub := "docs"
       const N = 8
       for i := 0; i < N; i++ {
           must(t, os.MkdirAll(filepath.Join(root, sub, fmt.Sprintf("f-%02d", i)), 0o755))
       }
       pp := &PickPolicy{
           Root: sub, OnCommitDefault: "release_to_back",
           OnGiveUpDefault: "release_to_back", VisibilityTimeout: time.Minute,
       }
       st, err := New(Config{Root: root, PickPolicies: map[string]*PickPolicy{"@r": pp}})
       must(t, err)

       const M = 32
       var wg sync.WaitGroup
       var mu sync.Mutex
       availableCount := 0
       seenFolders := make(map[string]int)
       for i := 0; i < M; i++ {
           wg.Add(1)
           go func(i int) {
               defer wg.Done()
               outcome, err := st.Open(context.Background(), fmt.Sprintf("claim-%d", i), "@r")
               if err != nil {
                   t.Errorf("goroutine %d: Open: %v", i, err)
                   return
               }
               if !outcome.Available {
                   return
               }
               var folderObj struct {
                   Folder string `json:"folder"`
               }
               if err := json.Unmarshal(outcome.Result.Payload, &folderObj); err != nil {
                   t.Errorf("goroutine %d: payload: %v", i, err)
                   return
               }
               mu.Lock()
               availableCount++
               seenFolders[folderObj.Folder]++
               mu.Unlock()
           }(i)
       }
       wg.Wait()
       if availableCount != N {
           t.Errorf("got %d picks, want %d (M=%d goroutines, N=%d folders)", availableCount, N, M, N)
       }
       for f, c := range seenFolders {
           if c != 1 {
               t.Errorf("folder %s picked %d times; want 1", f, c)
           }
       }
   }

   func must(t *testing.T, err error) {
       t.Helper()
       if err != nil {
           t.Fatalf("unexpected error: %v", err)
       }
   }
   ```

   Add `context`, `encoding/json`, `sync` to test imports as needed.

**Verification:**

```sh
go test ./stores/filesystem/store/ -run "TestOpenPickPolicy|TestOpenSelectorDispatch" -count=1 -race
```

Run again with multiple iterations to catch any rare race:

```sh
go test ./stores/filesystem/store/ -run TestOpenPickPolicy_ConcurrentPicksAreUnique -count=10 -race
```

Expect: PASS for both.

---

### Task 6: Action handlers — `findByClaimID`, `applyPickAction`, `Commit`/`Abandon` dispatch

**Files:** `stores/filesystem/store/pick_policy.go`, `stores/filesystem/store/store.go`, `stores/filesystem/store/pick_policy_test.go`

**Steps:**

1. Append to `pick_policy.go`:

   ```go
   // findByClaimID linearly scans every configured policy's in_progress/
   // for a sentinel matching *.<claimID>.*. Returns the first match (claim
   // IDs are rimsky-supplied UUIDs unique across all acquisitions; mirrors
   // pg's findPolicyForClaim behavior). Returns (nil, "", "") if no match.
   func (s *Store) findByClaimID(claimID string) (pp *PickPolicy, selector, entry, folder string) {
       for sel, candidate := range s.pickPolicies {
           inProg := filepath.Join(policyStateDir(s.root, sel), "in_progress")
           entries, err := os.ReadDir(inProg)
           if err != nil {
               continue
           }
           for _, e := range entries {
               f, c, _, perr := parseFromRight(e.Name())
               if perr != nil || c != claimID {
                   continue
               }
               return candidate, sel, e.Name(), f
           }
       }
       return nil, "", "", ""
   }

   // applyPickAction runs the configured action for a Commit/Abandon path.
   // Treats ENOENT on the primary mutation as success (idempotent terminal).
   //
   // Takes the precomputed find-result so the dispatch in Commit/Abandon
   // doesn't scan twice.
   func (s *Store) applyPickAction(pp *PickPolicy, selector, entry, folder, action string) error {
       inProgDir := filepath.Join(policyStateDir(s.root, selector), "in_progress")
       availDir := filepath.Join(policyStateDir(s.root, selector), "available")
       src := filepath.Join(inProgDir, entry)
       switch action {
       case "release_to_back":
           now := time.Now()
           if err := os.Chtimes(src, now, now); err != nil && !errors.Is(err, fs.ErrNotExist) {
               return fmt.Errorf("filesystem store: chtimes: %w", err)
           }
           if err := os.Rename(src, filepath.Join(availDir, folder)); err != nil && !errors.Is(err, fs.ErrNotExist) {
               return fmt.Errorf("filesystem store: release_to_back rename: %w", err)
           }
           return nil
       case "release_to_head":
           epoch := time.Unix(0, 0)
           if err := os.Chtimes(src, epoch, epoch); err != nil && !errors.Is(err, fs.ErrNotExist) {
               return fmt.Errorf("filesystem store: chtimes (head): %w", err)
           }
           if err := os.Rename(src, filepath.Join(availDir, folder)); err != nil && !errors.Is(err, fs.ErrNotExist) {
               return fmt.Errorf("filesystem store: release_to_head rename: %w", err)
           }
           return nil
       case "delete":
           folderAbs := filepath.Join(s.root, pp.Root, folder)
           if err := os.RemoveAll(folderAbs); err != nil {
               return fmt.Errorf("filesystem store: removeall %s: %w", folderAbs, err)
           }
           if err := os.Remove(src); err != nil && !errors.Is(err, fs.ErrNotExist) {
               return fmt.Errorf("filesystem store: unlink in_progress: %w", err)
           }
           return nil
       default:
           return fmt.Errorf("filesystem store: unknown pick action %q", action)
       }
   }
   ```

2. Modify `Commit`/`Abandon`/`Release` in `store.go` to dispatch through pick-policy when the claim_id is in pick-policy state. Single `findByClaimID` scan per call; result threaded into `applyPickAction`:

   ```go
   func (s *Store) Commit(_ context.Context, claimID string, _ []byte, _ []byte) error {
       if pp, sel, entry, folder := s.findByClaimID(claimID); pp != nil {
           return s.applyPickAction(pp, sel, entry, folder, pp.OnCommitDefault)
       }
       s.mu.Lock()
       delete(s.claims, claimID)
       s.mu.Unlock()
       return nil
   }

   func (s *Store) Abandon(_ context.Context, claimID string, _ []byte, _ []byte) error {
       if pp, sel, entry, folder := s.findByClaimID(claimID); pp != nil {
           return s.applyPickAction(pp, sel, entry, folder, pp.OnGiveUpDefault)
       }
       s.mu.Lock()
       delete(s.claims, claimID)
       s.mu.Unlock()
       return nil
   }
   ```

   `Release` stays unchanged — direct-mode fs registers no read state at Open in either path.

3. Append tests to `pick_policy_test.go`:

   ```go
   func TestCommit_ReleaseToBack(t *testing.T) {
       st, root, sub := newRingStore(t, "release_to_back", "release_to_back")
       must(t, os.MkdirAll(filepath.Join(root, sub, "alpha"), 0o755))
       must(t, os.MkdirAll(filepath.Join(root, sub, "beta"), 0o755))
       o, _ := st.Open(context.Background(), "c-alpha", "@r")
       if !o.Available {
           t.Fatal("first pick should be Available")
       }
       must(t, st.Commit(context.Background(), "c-alpha", o.Result.Region, o.Result.Address))
       // After release_to_back, alpha sits at tail; beta picked next.
       o2, _ := st.Open(context.Background(), "c-beta", "@r")
       var p struct{ Folder string }
       must(t, json.Unmarshal(o2.Result.Payload, &p))
       if p.Folder != "beta" {
           t.Errorf("expected beta picked next, got %s", p.Folder)
       }
   }

   func TestCommit_Delete_RemovesFolder(t *testing.T) {
       st, root, sub := newRingStore(t, "delete", "release_to_back")
       must(t, os.MkdirAll(filepath.Join(root, sub, "doomed"), 0o755))
       o, _ := st.Open(context.Background(), "c", "@r")
       if !o.Available {
           t.Fatal("pick should be Available")
       }
       must(t, st.Commit(context.Background(), "c", o.Result.Region, o.Result.Address))
       if _, err := os.Stat(filepath.Join(root, sub, "doomed")); !errors.Is(err, fs.ErrNotExist) {
           t.Errorf("folder should be removed after delete commit; stat err = %v", err)
       }
   }

   func TestAbandon_ReleaseToHead(t *testing.T) {
       st, root, sub := newRingStore(t, "release_to_back", "release_to_head")
       must(t, os.MkdirAll(filepath.Join(root, sub, "alpha"), 0o755))
       must(t, os.MkdirAll(filepath.Join(root, sub, "beta"), 0o755))
       o1, _ := st.Open(context.Background(), "c1", "@r")
       must(t, st.Commit(context.Background(), "c1", o1.Result.Region, o1.Result.Address))
       o2, _ := st.Open(context.Background(), "c2", "@r")
       must(t, st.Abandon(context.Background(), "c2", o2.Result.Region, o2.Result.Address))
       // After abandon (release_to_head), o2.folder sorts to head.
       o3, _ := st.Open(context.Background(), "c3", "@r")
       if string(o3.Result.Region) != string(o2.Result.Region) {
           t.Errorf("expected re-pick of head-bumped folder; got region %s vs %s",
               o3.Result.Region, o2.Result.Region)
       }
   }

   func TestCommit_Idempotent(t *testing.T) {
       st, root, sub := newRingStore(t, "release_to_back", "release_to_back")
       must(t, os.MkdirAll(filepath.Join(root, sub, "alpha"), 0o755))
       o, _ := st.Open(context.Background(), "c", "@r")
       must(t, st.Commit(context.Background(), "c", o.Result.Region, o.Result.Address))
       // Second commit must be a no-op (no error).
       must(t, st.Commit(context.Background(), "c", o.Result.Region, o.Result.Address))
   }

   func newRingStore(t *testing.T, onCommit, onGiveUp string) (*Store, string, string) {
       t.Helper()
       root := t.TempDir()
       sub := "docs"
       must(t, os.MkdirAll(filepath.Join(root, sub), 0o755))
       pp := &PickPolicy{
           Root: sub, OnCommitDefault: onCommit, OnGiveUpDefault: onGiveUp,
           VisibilityTimeout: time.Minute, SyncStrategy: "on_open",
       }
       st, err := New(Config{Root: root, PickPolicies: map[string]*PickPolicy{"@r": pp}})
       must(t, err)
       return st, root, sub
   }
   ```

**Verification:**

```sh
go test ./stores/filesystem/store/ -run "TestCommit|TestAbandon" -count=1 -race
```

Expect: PASS.

---

### Task 7: Sweep loop (`RunSweep`, `sweepOnce`) + tests

**Files:** `stores/filesystem/store/sweep.go` (new), `stores/filesystem/store/pick_policy_test.go`

**Steps:**

1. Create `stores/filesystem/store/sweep.go`:

   ```go
   package store

   import (
       "context"
       "errors"
       "io/fs"
       "log/slog"
       "os"
       "path/filepath"
       "time"
   )

   // RunSweep runs the visibility-timeout sweep + (when SyncStrategy is
   // on_sweep) the auto-discovery sync. Returns when ctx is cancelled.
   //
   // Per spec §7.5 / 2026-05-03-fs-store-pick-policies-design.md
   // "Sweep loop": purely store-internal; does not consult
   // rimsky_lock_holders.
   func (s *Store) RunSweep(ctx context.Context, interval time.Duration) {
       if interval <= 0 {
           interval = 60 * time.Second
       }
       t := time.NewTicker(interval)
       defer t.Stop()
       for {
           select {
           case <-ctx.Done():
               return
           case <-t.C:
           }
           if err := s.sweepOnce(); err != nil {
               slog.Warn("filesystem store: sweep", "error", err.Error())
           }
       }
   }

   func (s *Store) sweepOnce() error {
       for selector, pp := range s.pickPolicies {
           if pp.SyncStrategy == "on_sweep" {
               if err := s.runSync(selector, pp); err != nil {
                   slog.Warn("filesystem store: sweep sync", "selector", selector, "error", err.Error())
               }
           }
           if pp.VisibilityTimeout <= 0 {
               continue
           }
           inProg := filepath.Join(policyStateDir(s.root, selector), "in_progress")
           avail := filepath.Join(policyStateDir(s.root, selector), "available")
           entries, err := os.ReadDir(inProg)
           if err != nil {
               slog.Warn("filesystem store: sweep readdir", "selector", selector, "error", err.Error())
               continue
           }
           cutoff := time.Now().Add(-pp.VisibilityTimeout).UnixNano()
           for _, e := range entries {
               folder, _, claimedNanos, perr := parseFromRight(e.Name())
               if perr != nil {
                   continue
               }
               if claimedNanos > cutoff {
                   continue
               }
               src := filepath.Join(inProg, e.Name())
               dst := filepath.Join(avail, folder)
               if err := os.Rename(src, dst); err != nil && !errors.Is(err, fs.ErrNotExist) {
                   slog.Warn("filesystem store: sweep reclaim", "selector", selector, "folder", folder, "error", err.Error())
               }
           }
       }
       return nil
   }
   ```

2. Append to `pick_policy_test.go`:

   ```go
   func TestSweep_ReclaimsExpired(t *testing.T) {
       root := t.TempDir()
       sub := "docs"
       must(t, os.MkdirAll(filepath.Join(root, sub, "alpha"), 0o755))
       pp := &PickPolicy{
           Root: sub, OnCommitDefault: "release_to_back",
           OnGiveUpDefault: "release_to_back",
           VisibilityTimeout: 50 * time.Millisecond, // tight for test
           SyncStrategy:      "on_open",
       }
       st, err := New(Config{Root: root, PickPolicies: map[string]*PickPolicy{"@r": pp}})
       must(t, err)
       o, _ := st.Open(context.Background(), "c", "@r")
       if !o.Available {
           t.Fatal("pick should be Available")
       }
       // Wait past visibility timeout, then sweep.
       time.Sleep(100 * time.Millisecond)
       must(t, st.sweepOnce())
       availDir := filepath.Join(root, ".fs-store", "r", "available")
       entries, _ := os.ReadDir(availDir)
       found := false
       for _, e := range entries {
           if e.Name() == "alpha" {
               found = true
           }
       }
       if !found {
           t.Errorf("expected alpha back in available/ after sweep, got %v", entries)
       }
   }

   func TestSweep_OnSweepStrategyRunsSync(t *testing.T) {
       root := t.TempDir()
       sub := "docs"
       must(t, os.MkdirAll(filepath.Join(root, sub), 0o755))
       pp := &PickPolicy{
           Root: sub, OnCommitDefault: "release_to_back",
           OnGiveUpDefault: "release_to_back",
           VisibilityTimeout: time.Minute,
           SyncStrategy:      "on_sweep",
       }
       st, err := New(Config{Root: root, PickPolicies: map[string]*PickPolicy{"@r": pp}})
       must(t, err)
       // Folder added AFTER store creation; on_sweep should pick it up via sweepOnce.
       must(t, os.MkdirAll(filepath.Join(root, sub, "late-arrival"), 0o755))
       must(t, st.sweepOnce())
       availDir := filepath.Join(root, ".fs-store", "r", "available")
       entries, _ := os.ReadDir(availDir)
       found := false
       for _, e := range entries {
           if e.Name() == "late-arrival" {
               found = true
           }
       }
       if !found {
           t.Errorf("on_sweep sync should have added late-arrival sentinel; got %v", entries)
       }
   }
   ```

**Verification:**

```sh
go test ./stores/filesystem/store/ -run TestSweep -count=1
```

Expect: PASS.

---

### Task 8: Wire admin endpoint + update server.Run signature

**Files:** `stores/filesystem/store/admin.go` (new), `stores/filesystem/server/server.go`, `stores/filesystem/store/pick_policy_test.go`

**Steps:**

1. Create `stores/filesystem/store/admin.go`:

   ```go
   package store

   import (
       "encoding/json"
       "errors"
       "fmt"
       "io/fs"
       "net/http"
       "net/url"
       "os"
       "path/filepath"
       "strings"
       "time"
   )

   // AdminHandler returns an http.Handler for the fs store's admin
   // surface. v1 ships a single endpoint:
   //
   //   POST /admin/bump-to-head/{selector}
   //     body: {"folder": "<folder-name>"}
   //     responses: 204 | 400 | 404 | 409 | 500
   //
   // Selector path-param accepts the raw "@policy-name" form or its
   // percent-encoded "%40policy-name" form. Mirrors pg's URL-shape
   // convention (stores/postgres/store/admin.go).
   func (s *Store) AdminHandler() http.Handler {
       mux := http.NewServeMux()
       mux.HandleFunc("/admin/bump-to-head/", func(w http.ResponseWriter, r *http.Request) {
           if r.Method != http.MethodPost {
               http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
               return
           }
           rawSelector := strings.TrimPrefix(r.URL.Path, "/admin/bump-to-head/")
           selector, err := url.PathUnescape(rawSelector)
           if err != nil {
               http.Error(w, "selector not valid percent-encoding: "+err.Error(), http.StatusBadRequest)
               return
           }
           if selector == "" {
               http.Error(w, "selector is required", http.StatusBadRequest)
               return
           }
           pp, ok := s.pickPolicies[selector]
           if !ok {
               http.Error(w, fmt.Sprintf("unknown selector %q", selector), http.StatusBadRequest)
               return
           }
           var body struct {
               Folder string `json:"folder"`
           }
           if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
               http.Error(w, "invalid JSON body: "+err.Error(), http.StatusBadRequest)
               return
           }
           folder := body.Folder
           if folder == "" {
               http.Error(w, "folder is required", http.StatusBadRequest)
               return
           }
           if strings.HasPrefix(folder, ".") {
               http.Error(w, "folder must not start with '.'", http.StatusBadRequest)
               return
           }
           if pp.FolderPattern != nil && !pp.FolderPattern.MatchString(folder) {
               http.Error(w, "folder violates configured pattern", http.StatusBadRequest)
               return
           }
           folderAbs := filepath.Join(s.root, pp.Root, folder)
           info, err := os.Stat(folderAbs)
           if err != nil || !info.IsDir() {
               http.Error(w, "folder not found", http.StatusNotFound)
               return
           }
           availSentinel := filepath.Join(policyStateDir(s.root, selector), "available", folder)
           epoch := time.Unix(0, 0)
           if err := os.Chtimes(availSentinel, epoch, epoch); err != nil {
               if errors.Is(err, fs.ErrNotExist) {
                   // sentinel missing — distinguish "raced into in_progress" from "not enqueued yet"
                   if folderInProgress(s.root, selector, folder) {
                       http.Error(w, "folder is in_progress", http.StatusConflict)
                       return
                   }
                   http.Error(w, "folder not in queue (sync may not have enqueued it yet)", http.StatusNotFound)
                   return
               }
               http.Error(w, err.Error(), http.StatusInternalServerError)
               return
           }
           w.WriteHeader(http.StatusNoContent)
       })
       return mux
   }

   // folderInProgress returns true if any sentinel under .fs-store/<sel>/in_progress/
   // parses to <folder>. Used by bump-to-head to distinguish 409 from 404.
   func folderInProgress(storeRoot, selector, folder string) bool {
       dir := filepath.Join(policyStateDir(storeRoot, selector), "in_progress")
       entries, err := os.ReadDir(dir)
       if err != nil {
           return false
       }
       for _, e := range entries {
           f, _, _, perr := parseFromRight(e.Name())
           if perr == nil && f == folder {
               return true
           }
       }
       return false
   }
   ```

2. Update `stores/filesystem/server/server.go`. Replace the `Config` struct and `Run` signature:

   ```go
   type Config struct {
       Root          string
       PickPolicies  map[string]*fsstore.PickPolicy
       SweepInterval time.Duration
   }

   func Run(ctx context.Context, cfg Config, grpcLis, httpLis, adminLis net.Listener) error {
       st, err := fsstore.New(fsstore.Config{
           Root:         cfg.Root,
           PickPolicies: cfg.PickPolicies,
       })
       if err != nil {
           return err
       }
       srv := &Server{store: st}
       grpcSrv := grpc.NewServer()
       genv1.RegisterStoreServiceServer(grpcSrv, srv)
       go func() {
           if err := grpcSrv.Serve(grpcLis); err != nil {
               slog.Warn("filesystem store: grpc serve", "error", err.Error())
           }
       }()
       mux := http.NewServeMux()
       bridge.Mount(mux, srv)
       httpSrv := &http.Server{Handler: mux}
       go func() {
           if err := httpSrv.Serve(httpLis); err != nil && err != http.ErrServerClosed {
               slog.Warn("filesystem store: http serve", "error", err.Error())
           }
       }()
       var adminSrv *http.Server
       if adminLis != nil {
           adminSrv = &http.Server{Handler: st.AdminHandler()}
           go func() {
               if err := adminSrv.Serve(adminLis); err != nil && err != http.ErrServerClosed {
                   slog.Warn("filesystem store: admin serve", "error", err.Error())
               }
           }()
       }
       go st.RunSweep(ctx, cfg.SweepInterval)
       <-ctx.Done()
       stopTimer := time.AfterFunc(gracefulStopBudget, grpcSrv.Stop)
       grpcSrv.GracefulStop()
       stopTimer.Stop()
       _ = httpSrv.Close()
       if adminSrv != nil {
           _ = adminSrv.Close()
       }
       return nil
   }
   ```

   `time` is already imported by the existing `server.go` (used by `gracefulStopBudget`); no new imports needed for this rewrite.

3. Update `stores/filesystem/cmd/main.go` from Task 2 to call `server.Run(ctx, server.Config{Root, PickPolicies, SweepInterval}, grpcLis, httpLis, adminLis)`.

4. Add unit tests for the admin handler in `pick_policy_test.go`:

   ```go
   func TestAdminBumpToHead_204(t *testing.T) {
       root := t.TempDir()
       sub := "docs"
       must(t, os.MkdirAll(filepath.Join(root, sub, "alpha"), 0o755))
       must(t, os.MkdirAll(filepath.Join(root, sub, "beta"), 0o755))
       pp := &PickPolicy{
           Root: sub, OnCommitDefault: "release_to_back",
           OnGiveUpDefault: "release_to_back", VisibilityTimeout: time.Minute,
       }
       st, err := New(Config{Root: root, PickPolicies: map[string]*PickPolicy{"@r": pp}})
       must(t, err)
       must(t, st.runSync("@r", pp))

       handler := st.AdminHandler()
       req := httptest.NewRequest(http.MethodPost, "/admin/bump-to-head/%40r",
           strings.NewReader(`{"folder":"beta"}`))
       w := httptest.NewRecorder()
       handler.ServeHTTP(w, req)
       if w.Code != http.StatusNoContent {
           t.Fatalf("got %d, want 204; body=%s", w.Code, w.Body.String())
       }
       o, _ := st.Open(context.Background(), "c", "@r")
       var p struct{ Folder string }
       must(t, json.Unmarshal(o.Result.Payload, &p))
       if p.Folder != "beta" {
           t.Errorf("after bump, expected beta picked first; got %s", p.Folder)
       }
   }

   func TestAdminBumpToHead_404FolderMissing(t *testing.T) {
       st, _, _ := newRingStore(t, "release_to_back", "release_to_back")
       handler := st.AdminHandler()
       req := httptest.NewRequest(http.MethodPost, "/admin/bump-to-head/%40r",
           strings.NewReader(`{"folder":"nonexistent"}`))
       w := httptest.NewRecorder()
       handler.ServeHTTP(w, req)
       if w.Code != http.StatusNotFound {
           t.Fatalf("got %d, want 404; body=%s", w.Code, w.Body.String())
       }
   }

   func TestAdminBumpToHead_409InProgress(t *testing.T) {
       st, root, sub := newRingStore(t, "release_to_back", "release_to_back")
       must(t, os.MkdirAll(filepath.Join(root, sub, "alpha"), 0o755))
       _, _ = st.Open(context.Background(), "c", "@r") // claims alpha
       handler := st.AdminHandler()
       req := httptest.NewRequest(http.MethodPost, "/admin/bump-to-head/%40r",
           strings.NewReader(`{"folder":"alpha"}`))
       w := httptest.NewRecorder()
       handler.ServeHTTP(w, req)
       if w.Code != http.StatusConflict {
           t.Fatalf("got %d, want 409; body=%s", w.Code, w.Body.String())
       }
   }

   func TestAdminBumpToHead_400UnknownSelector(t *testing.T) {
       st, _, _ := newRingStore(t, "release_to_back", "release_to_back")
       handler := st.AdminHandler()
       req := httptest.NewRequest(http.MethodPost, "/admin/bump-to-head/%40nope",
           strings.NewReader(`{"folder":"x"}`))
       w := httptest.NewRecorder()
       handler.ServeHTTP(w, req)
       if w.Code != http.StatusBadRequest {
           t.Fatalf("got %d, want 400", w.Code)
       }
   }
   ```

   Add `net/http`, `net/http/httptest`, `strings` to test imports as needed.

**Verification:**

```sh
go build ./stores/filesystem/...
go test ./stores/filesystem/store/ -run TestAdminBumpToHead -count=1
```

Expect: build clean (cmd/main.go now matches the new server.Run signature) and tests PASS.

---

### Task 9: Folder-pattern filter test + multi-policy isolation tests

**Files:** `stores/filesystem/store/pick_policy_test.go`

**Steps:**

1. Append:

   ```go
   func TestFolderPattern_FiltersNonMatching(t *testing.T) {
       root := t.TempDir()
       sub := "docs"
       must(t, os.MkdirAll(filepath.Join(root, sub, "area-a"), 0o755))
       must(t, os.MkdirAll(filepath.Join(root, sub, "skip-me"), 0o755))
       must(t, os.MkdirAll(filepath.Join(root, sub, "area-b"), 0o755))
       pp := &PickPolicy{
           Root:              sub,
           FolderPattern:     regexp.MustCompile(`^area-.*$`),
           OnCommitDefault:   "release_to_back",
           OnGiveUpDefault:   "release_to_back",
           VisibilityTimeout: time.Minute,
           SyncStrategy:      "on_open",
       }
       st, err := New(Config{Root: root, PickPolicies: map[string]*PickPolicy{"@r": pp}})
       must(t, err)
       must(t, st.runSync("@r", pp))
       availDir := filepath.Join(root, ".fs-store", "r", "available")
       entries, _ := os.ReadDir(availDir)
       got := make(map[string]bool)
       for _, e := range entries {
           got[e.Name()] = true
       }
       if got["skip-me"] {
           t.Errorf("skip-me should be filtered: %v", got)
       }
       if !got["area-a"] || !got["area-b"] {
           t.Errorf("matching folders should be enqueued: %v", got)
       }
   }

   func TestMultiPolicy_NoCrossTalk(t *testing.T) {
       root := t.TempDir()
       must(t, os.MkdirAll(filepath.Join(root, "docs", "alpha"), 0o755))
       must(t, os.MkdirAll(filepath.Join(root, "reports", "beta"), 0o755))
       p1 := &PickPolicy{
           Root: "docs", OnCommitDefault: "release_to_back",
           OnGiveUpDefault: "release_to_back", VisibilityTimeout: time.Minute,
       }
       p2 := &PickPolicy{
           Root: "reports", OnCommitDefault: "release_to_back",
           OnGiveUpDefault: "release_to_back", VisibilityTimeout: time.Minute,
       }
       st, err := New(Config{Root: root, PickPolicies: map[string]*PickPolicy{
           "@docs": p1, "@reports": p2,
       }})
       must(t, err)
       o1, _ := st.Open(context.Background(), "c1", "@docs")
       o2, _ := st.Open(context.Background(), "c2", "@reports")
       var f1, f2 struct{ Folder string }
       must(t, json.Unmarshal(o1.Result.Payload, &f1))
       must(t, json.Unmarshal(o2.Result.Payload, &f2))
       if f1.Folder != "alpha" || f2.Folder != "beta" {
           t.Errorf("expected (alpha, beta); got (%s, %s)", f1.Folder, f2.Folder)
       }
   }
   ```

   Add `regexp` to test imports.

**Verification:**

```sh
go test ./stores/filesystem/store/ -run "TestFolderPattern|TestMultiPolicy" -count=1
```

Expect: PASS.

---

### Task 10: Update testfixture to support pick-policies

**Files:** `stores/filesystem/testfixture/testfixture.go`

**Steps:**

1. Replace the existing `Start` with a richer signature:

   ```go
   package testfixture

   import (
       "context"
       "net"
       "testing"

       "github.com/fallguyconsulting/rimsky/stores/filesystem/server"
       fsstore "github.com/fallguyconsulting/rimsky/stores/filesystem/store"
   )

   // Config configures the loopback store-service. Only Root is required;
   // omit PickPolicies for a regional-only store-service.
   type Config struct {
       Root          string
       PickPolicies  map[string]*fsstore.PickPolicy
       SweepInterval time.Duration
       WithAdmin     bool
   }

   // Start spawns the filesystem store-service on ephemeral listeners.
   // Returns (grpcEndpoint, adminEndpoint, teardown). adminEndpoint is
   // empty when WithAdmin is false.
   func Start(t *testing.T, cfg Config) (grpcEndpoint, adminEndpoint string, teardown func()) {
       t.Helper()
       grpcLis, err := net.Listen("tcp", "127.0.0.1:0")
       if err != nil {
           t.Fatalf("filesystem testfixture: grpc listen: %v", err)
       }
       httpLis, err := net.Listen("tcp", "127.0.0.1:0")
       if err != nil {
           _ = grpcLis.Close()
           t.Fatalf("filesystem testfixture: http listen: %v", err)
       }
       var adminLis net.Listener
       if cfg.WithAdmin {
           adminLis, err = net.Listen("tcp", "127.0.0.1:0")
           if err != nil {
               _ = grpcLis.Close()
               _ = httpLis.Close()
               t.Fatalf("filesystem testfixture: admin listen: %v", err)
           }
       }
       ctx, cancel := context.WithCancel(context.Background())
       done := make(chan struct{})
       go func() {
           _ = server.Run(ctx, server.Config{
               Root:          cfg.Root,
               PickPolicies:  cfg.PickPolicies,
               SweepInterval: cfg.SweepInterval,
           }, grpcLis, httpLis, adminLis)
           close(done)
       }()
       grpcEndpoint = grpcLis.Addr().String()
       if adminLis != nil {
           adminEndpoint = adminLis.Addr().String()
       }
       return grpcEndpoint, adminEndpoint, func() {
           cancel()
           <-done
       }
   }
   ```

   Add `time` to imports.

2. Update any existing callers. Search for `testfixture.Start` and update:

   ```sh
   grep -rn "filesystem/testfixture" --include="*.go" .
   ```

   Likely callers: `test/smoke/setup.go` and any `test/scenarios/...` files. For each caller, change BOTH the argument shape AND the assignment tuple:

   - Old: `endpoint, teardown := fsfixture.Start(t, root)`
   - New: `grpcEndpoint, _, teardown := fsfixture.Start(t, fsfixture.Config{Root: root})`

   The new return signature is `(grpcEndpoint, adminEndpoint, teardown)` — three values. Existing callers don't use the admin endpoint, so discard it with `_`.

**Verification:**

```sh
go build ./...
```

Expect: clean build.

---

### Task 11: Integration test — basic ring cycle through gRPC

**Files:** `test/scenarios/stores/fs_pick_policy_basic_test.go` (new)

**Steps:**

1. Create the test file. Pattern follows existing scenario tests under `test/scenarios/stores/`:

   ```go
   package stores

   import (
       "context"
       "encoding/json"
       "fmt"
       "os"
       "path/filepath"
       "testing"
       "time"

       "google.golang.org/grpc"
       "google.golang.org/grpc/credentials/insecure"

       genv1 "github.com/fallguyconsulting/rimsky/proto/v1/gen"
       fsstore "github.com/fallguyconsulting/rimsky/stores/filesystem/store"
       fsfixture "github.com/fallguyconsulting/rimsky/stores/filesystem/testfixture"
   )

   // Verifies a full ring cycle through the gRPC wire surface:
   // pick → commit (release_to_back) → pick (different folder) → ...
   func TestFsPickPolicy_BasicRingCycle(t *testing.T) {
       root := t.TempDir()
       sub := "docs"
       for _, name := range []string{"alpha", "beta", "gamma"} {
           if err := os.MkdirAll(filepath.Join(root, sub, name), 0o755); err != nil {
               t.Fatal(err)
           }
       }
       pp := &fsstore.PickPolicy{
           Root: sub, OnCommitDefault: "release_to_back",
           OnGiveUpDefault: "release_to_back",
           VisibilityTimeout: time.Minute, SyncStrategy: "on_open",
       }
       grpcAddr, _, teardown := fsfixture.Start(t, fsfixture.Config{
           Root: root,
           PickPolicies: map[string]*fsstore.PickPolicy{"@r": pp},
       })
       t.Cleanup(teardown)

       conn, err := grpc.NewClient(grpcAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
       if err != nil {
           t.Fatalf("grpc dial: %v", err)
       }
       defer conn.Close()
       client := genv1.NewStoreServiceClient(conn)

       picked := make([]string, 0, 3)
       for i := 0; i < 3; i++ {
           claimID := fmt.Sprintf("c-%d", i)
           o, err := client.Open(context.Background(), &genv1.OpenRequest{
               ClaimId: claimID, Selector: "@r", Intent: "rw",
           })
           if err != nil {
               t.Fatalf("Open[%d]: %v", i, err)
           }
           acq := o.GetAcquired()
           if acq == nil {
               t.Fatalf("Open[%d]: expected Acquired, got Unavailable", i)
           }
           var p struct{ Folder string }
           if err := json.Unmarshal(acq.Payload, &p); err != nil {
               t.Fatalf("payload: %v", err)
           }
           picked = append(picked, p.Folder)
           if _, err := client.Commit(context.Background(), &genv1.CommitRequest{
               ClaimId: claimID, Region: acq.Region, Address: acq.Address,
           }); err != nil {
               t.Fatalf("Commit[%d]: %v", i, err)
           }
       }
       seen := make(map[string]bool)
       for _, f := range picked {
           if seen[f] {
               t.Errorf("folder %s picked twice in 3 cycles; ring should rotate", f)
           }
           seen[f] = true
       }
       if len(seen) != 3 {
           t.Errorf("expected 3 unique folders picked, got %d: %v", len(seen), picked)
       }
   }
   ```

**Verification:**

```sh
go test ./test/scenarios/stores/ -run TestFsPickPolicy_BasicRingCycle -count=1
```

Expect: PASS. Note this scenario tests rely on `core/internal/pgtest` indirectly via shared scenario infrastructure — Docker must be running for the testcontainers Postgres backing rimsky's lock-holder state.

---

### Task 12: Integration test — cross-queue concurrency on overlapping folder

**Files:** `test/scenarios/stores/fs_cross_queue_concurrency_test.go` (new)

**Steps:**

1. The scenario: two pick policies sharing a sub-root, both auto-discover the same folder. Two acquirer nodes (one per policy) attempt to acquire. Both store-side `Open` calls succeed (each policy's `available/` rename works independently); the rimsky-side lock-holder INSERTs conflict byte-equal because both produce `region = json("docs/alpha")`. The losing supervisor's tx rolls back; the supervisor calls `Abandon`, which routes through `on_give_up_default = release_to_back`, cycling the loser's sentinel back. Eventually both nodes reach `fresh` in some serial order.

2. Reference test for the harness API: `test/scenarios/stores/regional_claim_test.go`. The shape below mirrors that file. Concrete sketch:

   ```go
   // Cross-queue concurrency through the loopback gRPC fixture.
   // Two pick policies share the same sub-root; both auto-discover
   // folder "alpha". Both acquirer nodes produce byte-equal regions
   // (json("docs/alpha")), so rimsky's conflict predicate serializes
   // them. Eventually both reach `fresh` in some order — losing
   // acquirer recycles via on_give_up_default → release_to_back.
   package stores

   import (
       "os"
       "path/filepath"
       "testing"
       "time"

       "github.com/stretchr/testify/require"

       "github.com/fallguyconsulting/rimsky/core/config"
       "github.com/fallguyconsulting/rimsky/core/node"
       "github.com/fallguyconsulting/rimsky/core/scenario"
       "github.com/fallguyconsulting/rimsky/core/shared"
       "github.com/fallguyconsulting/rimsky/core/store"
       fsstore "github.com/fallguyconsulting/rimsky/stores/filesystem/store"
       fsfixture "github.com/fallguyconsulting/rimsky/stores/filesystem/testfixture"
   )

   func TestFsCrossQueueConcurrency(t *testing.T) {
       t.Parallel()
       root := t.TempDir()
       require.NoError(t, os.MkdirAll(filepath.Join(root, "docs", "alpha"), 0o755))

       p1 := &fsstore.PickPolicy{
           Root: "docs", OnCommitDefault: "release_to_back",
           OnGiveUpDefault: "release_to_back",
           VisibilityTimeout: time.Minute, SyncStrategy: "on_open",
       }
       p2 := &fsstore.PickPolicy{
           Root: "docs", OnCommitDefault: "release_to_back",
           OnGiveUpDefault: "release_to_back",
           VisibilityTimeout: time.Minute, SyncStrategy: "on_open",
       }
       grpcEndpoint, _, teardown := fsfixture.Start(t, fsfixture.Config{
           Root:         root,
           PickPolicies: map[string]*fsstore.PickPolicy{"@r1": p1, "@r2": p2},
       })
       t.Cleanup(teardown)

       h := scenario.Start(t, scenario.HarnessOpts{
           Stores: config.RemoteStoresConfig{
               Stores: map[string]config.StoreEntry{
                   "docs": {
                       Endpoint:     "grpc://" + grpcEndpoint,
                       Capabilities: store.Capabilities{WriteSemantics: store.WriteSemanticsDirect},
                   },
               },
           },
       })
       // The stub executor's WhenType map is exact-match — one entry
       // per distinct node Type, otherwise the executor errors out.
       h.Stub.WhenType("worker-r1").Complete(map[string]any{}, true, "scenario")
       h.Stub.WhenType("worker-r2").Complete(map[string]any{}, true, "scenario")

       tid := h.DeployTemplate(node.TemplateSpec{
           Name: "fs-cross-queue", Version: "1",
           Nodes: []node.TemplateNodeDef{
               scenario.MakeNode(
                   node.TemplateNodeDef{Type: "worker-r1", Executor: "stub"},
                   scenario.WithStores(scenario.WriteClaimRef("docs", "@r1")),
               ),
               scenario.MakeNode(
                   node.TemplateNodeDef{Type: "worker-r2", Executor: "stub"},
                   scenario.WithStores(scenario.WriteClaimRef("docs", "@r2")),
               ),
           },
       })
       iid := h.CreateInstance(tid, "ck-fs-xqueue", map[string]any{})

       n1 := h.FindNode(iid, "worker-r1")
       n2 := h.FindNode(iid, "worker-r2")
       require.NotNil(t, n1)
       require.NotNil(t, n2)

       // Both nodes must eventually reach fresh — the conflict only
       // delays the loser, doesn't break it. 30s is generous for
       // visibility-timeout / scheduler-tick combinations.
       require.True(t, h.WaitForNodeState(n1.ID, shared.NodeStateFresh, 30*time.Second),
           "worker-r1 did not reach fresh")
       require.True(t, h.WaitForNodeState(n2.ID, shared.NodeStateFresh, 30*time.Second),
           "worker-r2 did not reach fresh")
   }
   ```

3. Implementer note: if `scenario.WriteClaimRef` doesn't exist with the exact name, grep `core/scenario/` for the helper that produces a write-intent claim ref against a store — recent specs may have renamed it. The intent is "rw claim on store=docs, selector=@r1".

**Verification:**

```sh
go test ./test/scenarios/stores/ -run TestFsCrossQueueConcurrency -count=1 -timeout 5m
```

Expect: PASS. (5m is generous; testcontainers boot dominates.)

---

### Task 13: Integration test — pick-vs-regional concurrency

**Files:** `test/scenarios/stores/fs_pick_vs_regional_concurrency_test.go` (new)

**Steps:**

1. Scenario: a pick-policy claim and a regional claim both target the same folder. Region bytes match byte-equal; rimsky's conflict predicate serializes. Both nodes eventually reach `fresh` in some order.

2. Concrete sketch — same harness shape as Task 12, just with one pick-policy claim and one regional claim instead of two pick-policy claims:

   ```go
   package stores

   import (
       "os"
       "path/filepath"
       "testing"
       "time"

       "github.com/stretchr/testify/require"

       "github.com/fallguyconsulting/rimsky/core/config"
       "github.com/fallguyconsulting/rimsky/core/node"
       "github.com/fallguyconsulting/rimsky/core/scenario"
       "github.com/fallguyconsulting/rimsky/core/shared"
       "github.com/fallguyconsulting/rimsky/core/store"
       fsstore "github.com/fallguyconsulting/rimsky/stores/filesystem/store"
       fsfixture "github.com/fallguyconsulting/rimsky/stores/filesystem/testfixture"
   )

   func TestFsPickVsRegionalConcurrency(t *testing.T) {
       t.Parallel()
       root := t.TempDir()
       require.NoError(t, os.MkdirAll(filepath.Join(root, "docs", "alpha"), 0o755))

       pp := &fsstore.PickPolicy{
           Root: "docs", OnCommitDefault: "release_to_back",
           OnGiveUpDefault: "release_to_back",
           VisibilityTimeout: time.Minute, SyncStrategy: "on_open",
       }
       grpcEndpoint, _, teardown := fsfixture.Start(t, fsfixture.Config{
           Root:         root,
           PickPolicies: map[string]*fsstore.PickPolicy{"@r": pp},
       })
       t.Cleanup(teardown)

       h := scenario.Start(t, scenario.HarnessOpts{
           Stores: config.RemoteStoresConfig{
               Stores: map[string]config.StoreEntry{
                   "docs": {
                       Endpoint:     "grpc://" + grpcEndpoint,
                       Capabilities: store.Capabilities{WriteSemantics: store.WriteSemanticsDirect},
                   },
               },
           },
       })
       h.Stub.WhenType("pick-worker").Complete(map[string]any{}, true, "scenario")
       h.Stub.WhenType("regional-worker").Complete(map[string]any{}, true, "scenario")

       tid := h.DeployTemplate(node.TemplateSpec{
           Name: "fs-pick-vs-regional", Version: "1",
           Nodes: []node.TemplateNodeDef{
               scenario.MakeNode(
                   node.TemplateNodeDef{Type: "pick-worker", Executor: "stub"},
                   scenario.WithStores(scenario.WriteClaimRef("docs", "@r")),
               ),
               scenario.MakeNode(
                   node.TemplateNodeDef{Type: "regional-worker", Executor: "stub"},
                   scenario.WithStores(scenario.WriteClaimRef("docs", "docs/alpha")),
               ),
           },
       })
       iid := h.CreateInstance(tid, "ck-fs-pick-vs-reg", map[string]any{})

       np := h.FindNode(iid, "pick-worker")
       nr := h.FindNode(iid, "regional-worker")
       require.NotNil(t, np)
       require.NotNil(t, nr)

       require.True(t, h.WaitForNodeState(np.ID, shared.NodeStateFresh, 30*time.Second),
           "pick-worker did not reach fresh")
       require.True(t, h.WaitForNodeState(nr.ID, shared.NodeStateFresh, 30*time.Second),
           "regional-worker did not reach fresh")
   }
   ```

**Verification:**

```sh
go test ./test/scenarios/stores/ -run TestFsPickVsRegionalConcurrency -count=1 -timeout 5m
```

Expect: PASS.

---

### Task 14: Documentation updates

**Files:** `docs/operator-guide.md`, `docs/glossary.md`, `CHANGELOG.md`

**Steps:**

1. **`docs/operator-guide.md`:** locate the existing pg pick-policy subsection by searching for the literal string `"@review-queue":` or `stores/postgres` followed by a `pick_policies:` example. Insert the fs subsection immediately after it (line numbers will drift; locate by content):

   ```markdown
   - **`stores/filesystem` with pick policies** loads the same `STORE_FILESYSTEM_CONFIG`
     and grows a `pick_policies:` block when configured for queue/ring workloads:
     ```yaml
     root: /workspace
     pick_policies:
       "@docs-ring":
         root: documents
         folder_pattern: "^[a-z][a-z0-9-]*$"
         on_commit_default: release_to_back
         on_give_up_default: release_to_back
         visibility_timeout_seconds: 1800
         sync_strategy: on_open
     host: 0.0.0.0
     grpc_port: 9100
     http_port: 9110
     admin_port: 9120
     sweep_interval_seconds: 60
     ```
     Folders under `<root>/<pick_policies.<sel>.root>/` are auto-discovered as
     queue items. Adding/removing a folder is `mkdir`/`rm -rf` under the sub-root;
     the next `Open` (or sweep tick under `sync_strategy: on_sweep`) reconciles.
     Actions: `release_to_back` cycles to the tail; `release_to_head` (mtime
     epoch) sorts strictly to the head — note this is *stronger* than pg's
     priority-bump `release_to_head`; `delete` runs `os.RemoveAll` on the
     underlying folder. Per `docs/specs/2026-05-03-fs-store-pick-policies-design.md`.
   ```

2. **`docs/glossary.md`:** make two edits.

   **Edit 2a:** in the section-header paragraph just below `## Store-internal vocabulary (not part of rimsky's protocol surface)`, change the existing parenthetical:

   - From: `(e.g. the postgres reference store-service)`
   - To: `(the postgres and filesystem reference store-services)`

   **Edit 2b:** in the `**Pick policy**` entry's last sentence, change:

   - From: `The postgres reference store-service exposes per-policy `on_commit_default` / `on_give_up_default` config in its own `config.yml`. See `docs/store-author-guide.md` and `deploy/store-postgres.yml`.`
   - To: `The postgres and filesystem reference store-services both expose per-policy `on_commit_default` / `on_give_up_default` config in their own `config.yml`. The filesystem store-service additionally auto-discovers folder items by reading the configured sub-root, so `mkdir`/`rm -rf` is the insertion/removal mechanism (no items-insertion admin endpoint). See `docs/store-author-guide.md`, `deploy/store-postgres.yml`, and `docs/specs/2026-05-03-fs-store-pick-policies-design.md`.`

   **Edit 2c:** in the `**`release_to_back` / `release_to_head`**` entry, replace the existing parenthetical and append a sentence:

   - From: `Per-policy disposition actions in pick-policy store-services' configs (e.g. the postgres reference store-service). Store-internal; not visible to rimsky.`
   - To: `Per-policy disposition actions in pick-policy store-services' configs (the postgres and filesystem reference store-services). Store-internal; not visible to rimsky. The filesystem store implements `release_to_head` as an absolute mtime-zero bump (strictly stronger than pg's relative priority increment); see `docs/specs/2026-05-03-fs-store-pick-policies-design.md`.`

3. **`CHANGELOG.md`:** add under `## Unreleased`:

   ```markdown
   - **Filesystem store: pick-policy support.** The standard `stores/filesystem/`
     store-service grows a `pick_policies` config block paralleling the pg
     store's. Auto-discovery: folders under each policy's configured sub-root
     are queue items; `mkdir`/`rm -rf` is the insertion/removal mechanism
     (no admin items endpoint). Three actions ship: `release_to_back`,
     `release_to_head` (absolute mtime-zero bump — stronger than pg's relative
     priority increment), and `delete` (`os.RemoveAll`). Atomic claim is
     `rename(2)` between `<root>/.fs-store/<policy>/{available,in_progress}/`.
     Bump-to-head admin endpoint at `POST /admin/bump-to-head/{selector}`.
     `sync_strategy: on_open` (default) or `on_sweep` per policy.
     Per `docs/specs/2026-05-03-fs-store-pick-policies-design.md`.
   ```

**Verification:**

```sh
# Markdown lint isn't part of the project's standard pipeline; just confirm
# the files parse as valid markdown via a render or by inspection.
git diff --stat docs/operator-guide.md docs/glossary.md CHANGELOG.md
```

Expect: three files modified with the additions above.

---

### Task 15: Final full-tree build, test, lint

**Files:** none (verification-only)

**Steps:**

1. Run the full Go build and test suite:

   ```sh
   go build ./...
   go test ./... -count=1
   make lint
   ```

2. Run race-mode on the concurrency-sensitive paths:

   ```sh
   go test ./stores/filesystem/store/... -race -count=3
   go test ./test/scenarios/stores/... -race -count=1 -timeout 10m
   ```

3. If the testcontainers-backed scenario tests flake on the first run, retry up to 2 times — testcontainers occasionally races on Docker socket setup. Persistent failures must be diagnosed, not ignored.

**Verification:**

All three commands clean. `make lint` outputs nothing (or "0 issues"). All test packages report `ok`.

---

## Manual checks after completion

None. Every step in this plan is automatable; all verification is via test commands or static checks.
