package store

import "time"

// LockHandle is the runtime descriptor for an acquired lock-holder row in
// rimsky_lock_holders. ID is the FK target (UUID stringified). HolderNodeID
// and SupervisorID are convenience fields populated from the inserted row;
// the supervisor uses them for log/event annotation. They are not load-
// bearing for correctness — the SQL row is the authoritative source.
type LockHandle struct {
	ID           string // FK target == rimsky_lock_holders.id (UUID stringified)
	Kind         string // "named" | "region" | "claim"
	StoreName    string // empty for named locks
	HolderNodeID string
	SupervisorID string // matches rimsky_supervisors.id (TEXT)
	AcquiredAt   time.Time
	ExpiresAt    time.Time
}

// ClaimResult is returned alongside LockHandle from Store.AcquireLock. For
// non-claim acquisitions all fields are zero values. For claim acquisitions:
//   - ResolvedRegion is the store-kind-specific region the store picked.
//   - Payload is the user-data payload from the claimed item.
//   - ClaimID is the store-assigned item identifier; FK target for
//     rimsky_claim_holders.claim_id.
type ClaimResult struct {
	ResolvedRegion any    // store-kind-specific; nil for non-claim acquisitions
	Payload        any    // user-data payload from the claimed item; nil for non-claim acquisitions
	ClaimID        string // store-assigned item identifier; FK target for rimsky_claim_holders.claim_id
}

// ReleaseAction discriminates the policy branch the store should take inside
// ReleaseLock. Claim stores map this to on_commit / on_give_up; sidecar/
// versioned stores map it to discard-vs-preserve. Direct-mode stores ignore
// it (no-op for all actions).
type ReleaseAction string

const (
	ReleaseCommit         ReleaseAction = "commit"
	ReleaseDiscard        ReleaseAction = "discard"
	ReleaseGiveUp         ReleaseAction = "give_up"
	ReleasePreserveResume ReleaseAction = "preserve_for_resume"
)

// CommitResult is returned by Store.Commit. Changed=false stops cascade
// propagation downstream of the committing node (producer-declared, not
// content-hashed). ChangeSummary is a free-form human description for the
// event log.
type CommitResult struct {
	Changed       bool
	ChangeSummary string
}

// NativeHandle is the sealed interface implemented by per-store-kind handle
// types. The executor protocol serialises concrete NativeHandle values into
// the `handle` field of ExecuteRequest.stores[<name>] (spec §12.1) as
// google.protobuf.Struct; the executor unmarshals into kind-specific shapes
// per its own concerns.
//
// Sealed via the unexported nativeHandleMarker method: only types declared
// in this package can satisfy NativeHandle.
type NativeHandle interface {
	nativeHandleMarker()
}

// FilesystemDirectHandle is the NativeHandle for direct-mode filesystem
// stores. Path is an absolute directory path; POSIX ops work unmodified.
// WriteRegions and ReadRegions echo the resolved globs the lock covers.
type FilesystemDirectHandle struct {
	Path         string
	WriteRegions []string
	ReadRegions  []string
}

func (FilesystemDirectHandle) nativeHandleMarker() {}

// ClaimStoreHandle is the NativeHandle for claim_store kinds. Payload is the
// claimed item's user-data payload (read-only). ClaimID identifies the row
// in the store's items table; StoreName names the originating store.
type ClaimStoreHandle struct {
	Payload   any
	ClaimID   string
	StoreName string
}

func (ClaimStoreHandle) nativeHandleMarker() {}
