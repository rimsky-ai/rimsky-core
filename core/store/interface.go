package store

import "context"

// Capabilities describes what a Store implementation supports. The supervisor
// reads this at dispatch time to decide whether a node's required operations
// are serviceable by the configured store.
//
// SupportsAtomicMulti and KeepVersionsMax are deliberately absent in v1; both
// belong to deferred features and are not declared until those land.
type Capabilities struct {
	SupportsRegionLock bool
	SupportsClaim      bool
	SupportsDiscard    bool
	SupportsResume     bool
	SupportsRestore    bool
}

// LockMode discriminates named-lock semantics. RegionLockSpec and
// ClaimLockSpec do not use LockMode (their semantics are exclusive on the
// resolved region).
type LockMode string

const (
	LockModeMutex    LockMode = "mutex"
	LockModeCounting LockMode = "counting"
)

// LockSpec is the sealed set of lock-acquisition requests a node may issue.
// Concrete kinds: NamedLockSpec, RegionLockSpec, ClaimLockSpec.
type LockSpec interface {
	// Kind returns one of "named", "region", "claim".
	Kind() string
}

// NamedLockSpec is a process-wide named lock — mutex (limit=1) or counting
// semaphore (limit=N). Replaces the pre-redesign "concurrency tag" mechanism.
type NamedLockSpec struct {
	Name  string
	Mode  LockMode
	Limit int // for counting; >=1; ignored for mutex
}

// Kind returns "named".
func (NamedLockSpec) Kind() string { return "named" }

// RegionLockSpec is a caller-specified region lock against a named store.
// The Region value is store-kind-specific (e.g. []string of globs for
// filesystem stores). Region carries the *write* region: the portion the
// node intends to mutate, on which the supervisor takes an exclusive lock
// via rimsky_lock_holders. ReadRegion carries the *read* region: an
// advisory descriptor passed through to the executor's NativeHandle so it
// can scope reads. Read regions are not lock-protected — they exist purely
// so the store can echo back resolved globs/paths to the executor.
type RegionLockSpec struct {
	StoreName  string
	Region     any
	ReadRegion any
	Resumable  bool
}

// Kind returns "region".
func (RegionLockSpec) Kind() string { return "region" }

// ClaimLockSpec is a store-picks-region acquisition. The store selects an
// eligible item, locks it, and reports the choice.
type ClaimLockSpec struct {
	StoreName string
	Criteria  map[string]any // optional filters; nil for any item
	Hold      bool
	OnCommit  string // overrides store default
	OnGiveUp  string // overrides store default
	Resumable bool
}

// Kind returns "claim".
func (ClaimLockSpec) Kind() string { return "claim" }

// Store is the universal interface every store implementation must satisfy.
//
// @blessed-invariant: Lock state lives only in postgres.
//
//	No Store implementation persists lock state. Stores may persist *data*
//	state (e.g. claim-store-postgres flips an items-table row to
//	'in_progress' when claiming), but the question "is anyone holding lock
//	X" is answered exclusively by rimsky_lock_holders. (Spec §18 invariant
//	9; spec §5.3.) A scenario test exercises this invariant; do not add
//	store-side lock-state caches that would violate it.
type Store interface {
	// Kind returns the canonical store kind, e.g. "filesystem" | "claim_store".
	Kind() string

	// Name returns the operator-configured store name; matches stores.<name>
	// in YAML.
	Name() string

	// Capabilities reports what this store supports. The supervisor reads
	// this at dispatch time.
	Capabilities() Capabilities

	// LockEligible is the eligibility check used by the dispatch eligibility
	// evaluator. For region locks, the supervisor calls this after pre-loading
	// existing holders for the store; the implementation can rely on the
	// caller having already screened against existing holders via
	// RegionsConflict.
	LockEligible(ctx context.Context, spec LockSpec) (bool, error)

	// RegionsConflict is the region-overlap predicate; called by the
	// supervisor when comparing a candidate region acquisition against an
	// existing holder for this store. Returns true if the two regions
	// conflict (cannot both be held at once).
	//
	// @blessed-invariant: RegionsConflict and UnmarshalRegion are pure.
	//
	//	No side effects, no external state read; deterministic on inputs.
	//	(Spec §18 invariant 14.) The supervisor calls these inside the
	//	atomic acquisition transaction (§13.3) and inside hot eligibility
	//	loops; impurity here would corrupt acquisition correctness.
	RegionsConflict(a, b any) bool

	// UnmarshalRegion deserialises region_data JSONB into the store's in-Go
	// region type. The supervisor calls this on each existing-holder row
	// before passing to RegionsConflict.
	//
	// @blessed-invariant: see RegionsConflict above; the same purity contract
	// applies. UnmarshalRegion must not read external state and must produce
	// the same value for the same input bytes.
	UnmarshalRegion(raw []byte) (any, error)

	// AcquireLock is called inside the supervisor's atomic acquisition
	// transaction (spec §13.3). For direct-mode filesystem this is a no-op
	// and returns ClaimResult{} unchanged. For claim_store this performs the
	// atomic items-table flip (state='in_progress') and returns the picked
	// item's payload + ID. The store is given the open *pgx.Tx via ctx
	// (key store.txKey, accessed through TxFromContext) so its writes
	// participate in the same transaction.
	AcquireLock(ctx context.Context, spec LockSpec) (LockHandle, ClaimResult, error)

	// OpenHandle constructs the executor-facing handle. For resumable
	// acquisitions the supervisor passes resumed=true; the store may surface
	// prior in-progress state (e.g. an open sidecar workspace). For
	// direct-mode filesystem, resumed is ignored and the live path is
	// returned in either case.
	OpenHandle(ctx context.Context, lh LockHandle, resumed bool) (NativeHandle, error)

	// Commit is called after the executor signals Complete{changed: true}.
	// For direct-mode filesystem this is a no-op (writes already on disk;
	// returns CommitResult{Changed: true}). For sidecar/versioned modes
	// (post-v1) this applies the sidecar to live atomically.
	Commit(ctx context.Context, lh LockHandle) (CommitResult, error)

	// ReleaseLock honours the action: claim stores invoke their on_commit /
	// on_give_up policy; sidecar/versioned stores discard the sidecar (for
	// give_up/discard) or keep it (for preserve_for_resume); direct-mode
	// filesystem is a no-op for all actions.
	ReleaseLock(ctx context.Context, lh LockHandle, action ReleaseAction) error
}

// ClaimableStore is the optional sub-interface a Store with
// Capabilities.SupportsClaim=true MUST also satisfy. The supervisor type-
// asserts at dispatch time.
type ClaimableStore interface {
	Store

	// HasClaimableItem reports whether at least one item matching the
	// criteria is currently claimable. Used by the dispatch eligibility
	// evaluator to short-circuit when the claim pool is empty.
	HasClaimableItem(ctx context.Context, criteria map[string]any) (bool, error)

	// ReleaseClaimItem performs the items-table reposition for the given
	// claim ID, per the supplied action ('release_to_back' | 'release_to_head').
	// Called by the spec §5.6.4 last-released-wins branch; the lock-holder
	// row may already be deleted at this point, so the call takes claimID
	// directly rather than a LockHandle. Runs inside the caller-provided tx
	// via TxFromContext(ctx).
	ReleaseClaimItem(ctx context.Context, claimID string, action string) error
}

// ResumableStore is the optional sub-interface a Store with
// Capabilities.SupportsResume=true MUST also satisfy.
type ResumableStore interface {
	Store

	// HasPriorWork reports whether the store retains in-progress state for
	// the given lock spec from a prior dispatch attempt. Used by the
	// supervisor to decide whether to pass resumed=true to OpenHandle.
	HasPriorWork(ctx context.Context, spec LockSpec) (bool, error)
}
