package claimproducer

import (
	"encoding/json"

	"github.com/google/uuid"
)

// WriteSemantics declares how a claim handle's writes coexist with
// concurrent claims on byte-equal scopes. See spec §2.4.
//
// Per Phase 4 of the layer-crystallization design (2026-05-04), the
// previously single-valued capability is replaced by an envelope: a
// ClaimProducer advertises a SET of permissible WriteSemantics values
// via Capabilities, and Open returns the realized value per claim. The
// producer is required to honor the uniformity invariant — two claims
// with byte-equal Scope MUST yield identical RealizedWriteSemantics.
type WriteSemantics int

const (
	// WriteSemanticsUnknown is the proto-default zero value; a producer
	// that returns Unknown is malformed and the supervisor must reject
	// the claim result.
	WriteSemanticsUnknown WriteSemantics = iota
	// WriteSemanticsSync — synchronous in-place writes; r×rw on
	// byte-equal scopes block.
	WriteSemanticsSync
	// WriteSemanticsStagedAsync — writes go to a staging area; reads
	// see a stable snapshot during writes; r×rw on byte-equal scopes
	// does NOT block.
	WriteSemanticsStagedAsync
	// WriteSemanticsBlockingAsync — writes go to a staging area;
	// r×rw on byte-equal scopes block until commit/abandon.
	WriteSemanticsBlockingAsync
	// WriteSemanticsReadOnly — claim cannot mutate; useful for
	// pure-read producers.
	WriteSemanticsReadOnly
)

// String returns a stable lowercase spelling for logs/config.
func (w WriteSemantics) String() string {
	switch w {
	case WriteSemanticsSync:
		return "sync"
	case WriteSemanticsStagedAsync:
		return "staged_async"
	case WriteSemanticsBlockingAsync:
		return "blocking_async"
	case WriteSemanticsReadOnly:
		return "read_only"
	default:
		return "unknown"
	}
}

// ParseWriteSemantics maps the YAML/JSON spelling back to the constant.
// Unknown spellings return WriteSemanticsUnknown and ok=false.
func ParseWriteSemantics(s string) (WriteSemantics, bool) {
	switch s {
	case "sync":
		return WriteSemanticsSync, true
	case "staged_async":
		return WriteSemanticsStagedAsync, true
	case "blocking_async":
		return WriteSemanticsBlockingAsync, true
	case "read_only":
		return WriteSemanticsReadOnly, true
	default:
		return WriteSemanticsUnknown, false
	}
}

// OpenRequest is the request for the Open verb.
type OpenRequest struct {
	ClaimID uuid.UUID
	Spec    json.RawMessage // opaque to Rimsky; producer-defined shape
}

// ClaimResult is the response from Open.
//
// Address, Payload, and Scope are inert in Rimsky (foundation invariant 20):
// Rimsky reads them only at substitution-leaf extraction. RealizedWriteSemantics
// declares the per-claim semantics; must be a member of the producer's
// CapabilitiesResult.WriteSemanticsEnvelope; must be uniform across
// byte-equal-scope claims (uniformity invariant in spec §2.5).
type ClaimResult struct {
	Address                json.RawMessage
	Payload                json.RawMessage
	Scope                  json.RawMessage
	RealizedWriteSemantics WriteSemantics
}

// CapabilitiesResult is the response from Capabilities.
type CapabilitiesResult struct {
	WriteSemanticsEnvelope []WriteSemantics // permissible values; singleton common
}
