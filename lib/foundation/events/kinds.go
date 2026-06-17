// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// Package events owns the typed-Kind discriminator API for the
// rimsky_events log. The persistence column shape stays TEXT, but
// rimsky's app logic (scheduler, supervisor, breakpoint evaluator,
// audit handler, read-API kind filters) consumes Kind values
// exclusively — raw strings cross only the persistence marshal/
// unmarshal boundary.
//
// Two kind families share one Kind value:
//
//   - Operational kinds (auth.*, state_transition, lock_acquired,
//     work_started, attributes_substituted, breakpoint.hit, etc.) —
//     declared as the OperationalKind enum in proto/v1/events.proto.
//     The proto enum IS the catalog; adding a new operational kind
//     means adding an enum value and regenerating Go bindings, no
//     schema migration required.
//
//   - Signal-class kinds (terminal/..., transient/..., attribute/...)
//     — carry the parsed signal type-path under the canonical taxonomy.
//     The signal package owns type-path validation; this package treats
//     the path as opaque and exposes it through SignalPath() / String().
//
// At the persistence boundary, Kind.String() produces the canonical
// wire form (the snake_case operational name OR the slash-delimited
// signal type-path) for the TEXT column. ParseKindString consumes
// the column value on read; an unknown string is a defensive error
// (per decision:event-log-kind-enum), never silently coerced to a
// synthetic "unknown" kind.
//
//	@concept: event-log
//	@decision: event-log-kind-enum
package events

import (
	"errors"
	"fmt"
	"strings"

	genv1 "github.com/rimsky-ai/rimsky-core/lib/protocols/proto/v1/gen"
)

// Family discriminates between operational and signal-class kinds.
type Family int

const (
	// FamilyUnknown is the zero value. A zero Kind is invalid for use
	// in a persistence write — constructors below produce non-zero
	// values; ParseKindString rejects empty input rather than emit a
	// zero Kind.
	FamilyUnknown Family = iota
	// @constraint: FamilyOperational designates an OperationalKind-backed kind.
	FamilyOperational
	// @constraint: FamilySignal designates a signal-type-path-backed kind.
	FamilySignal
)

// Kind is the typed discriminator carried by rimsky's app logic. It
// wraps either an OperationalKind enum value (operational family) or
// a canonical signal type-path string (signal family).
//
// Zero value is invalid — callers obtain Kind values through the
// constructors below or ParseKindString.
type Kind struct {
	family Family
	op     genv1.OperationalKind
	// signalPath is the slash-delimited canonical type-path for
	// signal-class kinds (e.g. "terminal/success",
	// "attribute/budget_cents/changed"). Empty for operational
	// kinds. Treated as opaque here — taxonomy validation is the
	// signal package's responsibility.
	signalPath string
}

// OperationalKindFromProto wraps an OperationalKind enum value as a
// typed Kind. The OPERATIONAL_KIND_UNSPECIFIED zero value is rejected
// at the persistence boundary by String() (returns empty) and is
// never produced by an honest emit site — callers pass a real enum
// value.
func OperationalKindFromProto(op genv1.OperationalKind) Kind {
	return Kind{family: FamilyOperational, op: op}
}

// SignalKind wraps a parsed signal type-path as a typed Kind. The
// path is taken verbatim — taxonomy validation has happened at the
// signal emit site (via signal.ValidateTypePath). Empty paths panic;
// an empty signal-class kind would round-trip through an empty wire
// string indistinguishable from "no kind", which the spec's typed
// boundary exists to prevent.
func SignalKind(path string) Kind {
	if path == "" {
		panic("events.SignalKind: empty signal type-path")
	}
	return Kind{family: FamilySignal, signalPath: path}
}

// Family returns the kind's family discriminator. FamilyUnknown means
// the value is the zero Kind and must not be persisted.
func (k Kind) Family() Family { return k.family }

// OperationalKind returns the wrapped proto enum value when the kind
// is operational; for signal-class kinds it returns
// OPERATIONAL_KIND_UNSPECIFIED.
func (k Kind) OperationalKind() genv1.OperationalKind { return k.op }

// SignalPath returns the wrapped signal type-path when the kind is
// signal-class; for operational kinds it returns the empty string.
func (k Kind) SignalPath() string { return k.signalPath }

// String produces the canonical wire form rimsky persists in
// rimsky_events.kind: snake_case operational name (e.g.
// "state_transition", "auth.access_attempted") for operational
// kinds; verbatim slash-delimited type-path for signal-class kinds;
// empty string for the zero Kind (a caller-side bug — Append paths
// log the empty value).
func (k Kind) String() string {
	switch k.family {
	case FamilyOperational:
		s, ok := operationalKindWireForm[k.op]
		if !ok {
			return ""
		}
		return s
	case FamilySignal:
		return k.signalPath
	default:
		return ""
	}
}

// IsZero reports whether k is the unset zero value.
func (k Kind) IsZero() bool { return k.family == FamilyUnknown }

// ErrUnknownKind is returned by ParseKindString when the string does
// not match a canonical operational name and is not shaped like a
// signal type-path. Persistence read paths surface this error rather
// than silently coerce to a synthetic "unknown" kind (per
// decision:event-log-kind-enum: defensive error at the unmarshal
// boundary).
var ErrUnknownKind = errors.New("events.ParseKindString: unknown kind")

// ParseKindString parses the canonical wire form back to a typed
// Kind. The rules:
//
//  1. Empty input is an error (a zero-Kind value is not a valid
//     parse — callers gate empties separately if they want a no-op).
//  2. If s matches a known operational name (snake_case from the
//     proto enum), return that operational Kind.
//  3. Otherwise, if s carries a slash (canonical signal type-paths
//     are slash-delimited), treat it as a signal-class kind. The
//     persistence-layer caller is encouraged to validate against the
//     signal taxonomy if it has the signal package in its dependency
//     budget; here we accept any non-empty slash-bearing string as
//     opaque.
//  4. Otherwise, return ErrUnknownKind wrapped with the offending
//     value.
//
// Note: the audit log historically uses dot-prefixed operational
// names ("auth.access_attempted", "breakpoint.hit"); those land in
// the operational name table above and parse as operational. So a
// slash in the string is the only honest disambiguator from
// operational vs. signal at parse time.
func ParseKindString(s string) (Kind, error) {
	if s == "" {
		return Kind{}, fmt.Errorf("%w: empty string", ErrUnknownKind)
	}
	if op, ok := operationalKindFromWire[s]; ok {
		return OperationalKindFromProto(op), nil
	}
	if strings.Contains(s, "/") {
		return SignalKind(s), nil
	}
	return Kind{}, fmt.Errorf("%w: %q", ErrUnknownKind, s)
}

// operationalKindWireForm maps the proto enum value to the canonical
// wire string rimsky persists. The strings here ARE the durable
// catalog: emit sites used to construct these as literals; now they
// thread through the typed Kind, with the literal mapping
// centralized so a single grep over this file enumerates every
// canonical operational kind.
//
// Keys must stay in sync with the OperationalKind enum in
// proto/v1/events.proto. New entries land in BOTH places.
var operationalKindWireForm = map[genv1.OperationalKind]string{
	genv1.OperationalKind_OPERATIONAL_KIND_AUTH_ACCESS_ATTEMPTED:           "auth.access_attempted",
	genv1.OperationalKind_OPERATIONAL_KIND_AUTH_ACCESS_DENIED:              "auth.access_denied",
	genv1.OperationalKind_OPERATIONAL_KIND_AUTH_KEY_CREATED:                "auth.key_created",
	genv1.OperationalKind_OPERATIONAL_KIND_AUTH_KEY_REVOKED:                "auth.key_revoked",
	genv1.OperationalKind_OPERATIONAL_KIND_AUTH_KEY_ROTATED:                "auth.key_rotated",
	genv1.OperationalKind_OPERATIONAL_KIND_STATE_TRANSITION:                "state_transition",
	genv1.OperationalKind_OPERATIONAL_KIND_WORK_STARTED:                    "work_started",
	genv1.OperationalKind_OPERATIONAL_KIND_WORK_COMPLETED:                  "work_completed",
	genv1.OperationalKind_OPERATIONAL_KIND_WORK_REJECTED:                   "work_rejected",
	genv1.OperationalKind_OPERATIONAL_KIND_HEARTBEAT_LOST:                  "heartbeat_lost",
	genv1.OperationalKind_OPERATIONAL_KIND_NO_OP_COMMIT:                    "no_op_commit",
	genv1.OperationalKind_OPERATIONAL_KIND_OPERATOR_OVERRIDE:               "operator_override",
	genv1.OperationalKind_OPERATIONAL_KIND_UNRESOLVED_EXECUTOR:             "unresolved_executor",
	genv1.OperationalKind_OPERATIONAL_KIND_INSTANCE_TERMINATED:             "instance_terminated",
	genv1.OperationalKind_OPERATIONAL_KIND_ERROR:                           "error",
	genv1.OperationalKind_OPERATIONAL_KIND_LOCK_ACQUIRED:                   "lock_acquired",
	genv1.OperationalKind_OPERATIONAL_KIND_LOCK_RELEASED:                   "lock_released",
	genv1.OperationalKind_OPERATIONAL_KIND_LOCK_ORPHAN_REAPED:              "lock_orphan_reaped",
	genv1.OperationalKind_OPERATIONAL_KIND_CLAIM_ACQUIRED:                  "claim_acquired",
	genv1.OperationalKind_OPERATIONAL_KIND_CLAIM_HELD:                      "claim_held",
	genv1.OperationalKind_OPERATIONAL_KIND_CLAIM_RESOLVED:                  "claim_resolved",
	genv1.OperationalKind_OPERATIONAL_KIND_ORPHANED_CLAIM_RELEASED:         "orphaned_claim_released",
	genv1.OperationalKind_OPERATIONAL_KIND_ORPHANED_CLAIM_LOST_RACE:        "orphaned_claim_lost_race",
	genv1.OperationalKind_OPERATIONAL_KIND_CLAIM_RESOLUTION_COMMIT:         "claim_resolution.commit",
	genv1.OperationalKind_OPERATIONAL_KIND_CLAIM_RESOLUTION_ABANDON:        "claim_resolution.abandon",
	genv1.OperationalKind_OPERATIONAL_KIND_ATTRIBUTES_SUBSTITUTED:          "attributes_substituted",
	genv1.OperationalKind_OPERATIONAL_KIND_ATTRIBUTES_COMMITTED:            "attributes_committed",
	genv1.OperationalKind_OPERATIONAL_KIND_ATTRIBUTES_VALIDATION_FAILED:    "attributes_validation_failed",
	genv1.OperationalKind_OPERATIONAL_KIND_ATTRIBUTES_SCHEMA_FAILED:        "attributes_schema_failed",
	genv1.OperationalKind_OPERATIONAL_KIND_TEMPLATE_RESOLUTION_FAILED:      "template_resolution_failed",
	genv1.OperationalKind_OPERATIONAL_KIND_TEMPLATE_VALIDATION_FAILED:      "template_validation_failed",
	genv1.OperationalKind_OPERATIONAL_KIND_EXECUTOR_SCHEMA_UNAVAILABLE:     "executor_schema_unavailable",
	genv1.OperationalKind_OPERATIONAL_KIND_BREAKPOINT_HIT:                  "breakpoint.hit",
	genv1.OperationalKind_OPERATIONAL_KIND_MESSAGE_EMITTED:                 "message_emitted",
	genv1.OperationalKind_OPERATIONAL_KIND_MESSAGE_RECEIVED:                "message_received",
	genv1.OperationalKind_OPERATIONAL_KIND_FAN_OUT_DISPATCHED:              "fan_out_dispatched",
	genv1.OperationalKind_OPERATIONAL_KIND_FANOUT_CHILDREN_CREATED:         "fanout.children_created",
	genv1.OperationalKind_OPERATIONAL_KIND_SUBCLAIM_BEGIN_CANDIDATE:        "subclaim.begin_candidate",
	genv1.OperationalKind_OPERATIONAL_KIND_SUBCLAIM_ACQUIRED:               "subclaim.acquired",
	genv1.OperationalKind_OPERATIONAL_KIND_SUBGRAPH_INTERNAL_CASCADE_FIRED: "subgraph_internal_cascade_fired",
	genv1.OperationalKind_OPERATIONAL_KIND_SUBGRAPH_DISPATCHED:             "subgraph.dispatched",
	genv1.OperationalKind_OPERATIONAL_KIND_SUBGRAPH_EXIT_CARRY:             "subgraph.exit_carry",
	genv1.OperationalKind_OPERATIONAL_KIND_PARK_TIMEOUT:                    "park_timeout",
	genv1.OperationalKind_OPERATIONAL_KIND_PARKED_RESUME_STARTED:           "parked_resume_started",
	genv1.OperationalKind_OPERATIONAL_KIND_DEBUG_OVERRIDE_APPLIED:          "debug.override.applied",
}

// operationalKindFromWire is the reverse index of
// operationalKindWireForm. Built once at package init from the
// canonical map above so the two stay in sync by construction.
var operationalKindFromWire = func() map[string]genv1.OperationalKind {
	m := make(map[string]genv1.OperationalKind, len(operationalKindWireForm))
	for op, s := range operationalKindWireForm {
		m[s] = op
	}
	return m
}()

// AllOperationalKinds returns the canonical wire-form names of every
// operational kind. Used by the audit-read handler to declare its
// allowed set and by tests / read-API diagnostics that want to
// enumerate the catalog. Order is unspecified.
func AllOperationalKinds() []string {
	out := make([]string, 0, len(operationalKindWireForm))
	for _, s := range operationalKindWireForm {
		out = append(out, s)
	}
	return out
}

// KindAuthAccessAttempted constructors for the operational kinds most often
// emitted from runtime/control. These let call sites avoid the
// verbose genv1 spelling without re-introducing string literals.
// Add more as emit sites grow.
//
// Each constructor's body is the typed equivalent of one of the
// pre-Pass-2 literal kind strings; collectively they enumerate the
// operational catalog above.
func KindAuthAccessAttempted() Kind {
	return OperationalKindFromProto(genv1.OperationalKind_OPERATIONAL_KIND_AUTH_ACCESS_ATTEMPTED)
}
func KindAuthAccessDenied() Kind {
	return OperationalKindFromProto(genv1.OperationalKind_OPERATIONAL_KIND_AUTH_ACCESS_DENIED)
}
func KindAuthKeyCreated() Kind {
	return OperationalKindFromProto(genv1.OperationalKind_OPERATIONAL_KIND_AUTH_KEY_CREATED)
}
func KindAuthKeyRevoked() Kind {
	return OperationalKindFromProto(genv1.OperationalKind_OPERATIONAL_KIND_AUTH_KEY_REVOKED)
}
func KindAuthKeyRotated() Kind {
	return OperationalKindFromProto(genv1.OperationalKind_OPERATIONAL_KIND_AUTH_KEY_ROTATED)
}
func KindStateTransition() Kind {
	return OperationalKindFromProto(genv1.OperationalKind_OPERATIONAL_KIND_STATE_TRANSITION)
}
func KindWorkStarted() Kind {
	return OperationalKindFromProto(genv1.OperationalKind_OPERATIONAL_KIND_WORK_STARTED)
}
func KindWorkCompleted() Kind {
	return OperationalKindFromProto(genv1.OperationalKind_OPERATIONAL_KIND_WORK_COMPLETED)
}
func KindWorkRejected() Kind {
	return OperationalKindFromProto(genv1.OperationalKind_OPERATIONAL_KIND_WORK_REJECTED)
}
func KindNoOpCommit() Kind {
	return OperationalKindFromProto(genv1.OperationalKind_OPERATIONAL_KIND_NO_OP_COMMIT)
}
func KindOperatorOverride() Kind {
	return OperationalKindFromProto(genv1.OperationalKind_OPERATIONAL_KIND_OPERATOR_OVERRIDE)
}
func KindUnresolvedExecutor() Kind {
	return OperationalKindFromProto(genv1.OperationalKind_OPERATIONAL_KIND_UNRESOLVED_EXECUTOR)
}
func KindInstanceTerminated() Kind {
	return OperationalKindFromProto(genv1.OperationalKind_OPERATIONAL_KIND_INSTANCE_TERMINATED)
}
func KindError() Kind {
	return OperationalKindFromProto(genv1.OperationalKind_OPERATIONAL_KIND_ERROR)
}
func KindLockAcquired() Kind {
	return OperationalKindFromProto(genv1.OperationalKind_OPERATIONAL_KIND_LOCK_ACQUIRED)
}
func KindLockReleased() Kind {
	return OperationalKindFromProto(genv1.OperationalKind_OPERATIONAL_KIND_LOCK_RELEASED)
}
func KindLockOrphanReaped() Kind {
	return OperationalKindFromProto(genv1.OperationalKind_OPERATIONAL_KIND_LOCK_ORPHAN_REAPED)
}
func KindClaimAcquired() Kind {
	return OperationalKindFromProto(genv1.OperationalKind_OPERATIONAL_KIND_CLAIM_ACQUIRED)
}
func KindClaimHeld() Kind {
	return OperationalKindFromProto(genv1.OperationalKind_OPERATIONAL_KIND_CLAIM_HELD)
}
func KindClaimResolved() Kind {
	return OperationalKindFromProto(genv1.OperationalKind_OPERATIONAL_KIND_CLAIM_RESOLVED)
}
func KindOrphanedClaimReleased() Kind {
	return OperationalKindFromProto(genv1.OperationalKind_OPERATIONAL_KIND_ORPHANED_CLAIM_RELEASED)
}
func KindOrphanedClaimLostRace() Kind {
	return OperationalKindFromProto(genv1.OperationalKind_OPERATIONAL_KIND_ORPHANED_CLAIM_LOST_RACE)
}
func KindClaimResolutionCommit() Kind {
	return OperationalKindFromProto(genv1.OperationalKind_OPERATIONAL_KIND_CLAIM_RESOLUTION_COMMIT)
}
func KindClaimResolutionAbandon() Kind {
	return OperationalKindFromProto(genv1.OperationalKind_OPERATIONAL_KIND_CLAIM_RESOLUTION_ABANDON)
}
func KindAttributesSubstituted() Kind {
	return OperationalKindFromProto(genv1.OperationalKind_OPERATIONAL_KIND_ATTRIBUTES_SUBSTITUTED)
}
func KindAttributesCommitted() Kind {
	return OperationalKindFromProto(genv1.OperationalKind_OPERATIONAL_KIND_ATTRIBUTES_COMMITTED)
}
func KindAttributesValidationFailed() Kind {
	return OperationalKindFromProto(genv1.OperationalKind_OPERATIONAL_KIND_ATTRIBUTES_VALIDATION_FAILED)
}
func KindAttributesSchemaFailed() Kind {
	return OperationalKindFromProto(genv1.OperationalKind_OPERATIONAL_KIND_ATTRIBUTES_SCHEMA_FAILED)
}
func KindTemplateResolutionFailed() Kind {
	return OperationalKindFromProto(genv1.OperationalKind_OPERATIONAL_KIND_TEMPLATE_RESOLUTION_FAILED)
}
func KindTemplateValidationFailed() Kind {
	return OperationalKindFromProto(genv1.OperationalKind_OPERATIONAL_KIND_TEMPLATE_VALIDATION_FAILED)
}
func KindExecutorSchemaUnavailable() Kind {
	return OperationalKindFromProto(genv1.OperationalKind_OPERATIONAL_KIND_EXECUTOR_SCHEMA_UNAVAILABLE)
}
func KindBreakpointHit() Kind {
	return OperationalKindFromProto(genv1.OperationalKind_OPERATIONAL_KIND_BREAKPOINT_HIT)
}
func KindMessageEmitted() Kind {
	return OperationalKindFromProto(genv1.OperationalKind_OPERATIONAL_KIND_MESSAGE_EMITTED)
}
func KindMessageReceived() Kind {
	return OperationalKindFromProto(genv1.OperationalKind_OPERATIONAL_KIND_MESSAGE_RECEIVED)
}
func KindFanOutDispatched() Kind {
	return OperationalKindFromProto(genv1.OperationalKind_OPERATIONAL_KIND_FAN_OUT_DISPATCHED)
}
func KindFanoutChildrenCreated() Kind {
	return OperationalKindFromProto(genv1.OperationalKind_OPERATIONAL_KIND_FANOUT_CHILDREN_CREATED)
}
func KindSubclaimBeginCandidate() Kind {
	return OperationalKindFromProto(genv1.OperationalKind_OPERATIONAL_KIND_SUBCLAIM_BEGIN_CANDIDATE)
}
func KindSubclaimAcquired() Kind {
	return OperationalKindFromProto(genv1.OperationalKind_OPERATIONAL_KIND_SUBCLAIM_ACQUIRED)
}
func KindSubgraphInternalCascadeFired() Kind {
	return OperationalKindFromProto(genv1.OperationalKind_OPERATIONAL_KIND_SUBGRAPH_INTERNAL_CASCADE_FIRED)
}
func KindSubgraphDispatched() Kind {
	return OperationalKindFromProto(genv1.OperationalKind_OPERATIONAL_KIND_SUBGRAPH_DISPATCHED)
}
func KindSubgraphExitCarry() Kind {
	return OperationalKindFromProto(genv1.OperationalKind_OPERATIONAL_KIND_SUBGRAPH_EXIT_CARRY)
}
func KindParkTimeout() Kind {
	return OperationalKindFromProto(genv1.OperationalKind_OPERATIONAL_KIND_PARK_TIMEOUT)
}
func KindParkedResumeStarted() Kind {
	return OperationalKindFromProto(genv1.OperationalKind_OPERATIONAL_KIND_PARKED_RESUME_STARTED)
}
func KindDebugOverrideApplied() Kind {
	return OperationalKindFromProto(genv1.OperationalKind_OPERATIONAL_KIND_DEBUG_OVERRIDE_APPLIED)
}
