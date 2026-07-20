// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// @concept: event-log
// @decision: event-log-kind-enum
package events

import (
	"errors"
	"fmt"
	"sort"
	"strings"

	genv1 "github.com/rimsky-ai/rimsky-core/lib/protocols/proto/v1/gen"
)

type Family int

const (
	FamilyUnknown Family = iota
	FamilyOperational
	FamilySignal
)

type Kind struct {
	family     Family
	op         genv1.OperationalKind
	signalPath string
}

func OperationalKindFromProto(op genv1.OperationalKind) Kind {
	if _, ok := operationalKindWireForm[op]; !ok {
		panic(fmt.Sprintf("events.OperationalKindFromProto: unmapped OperationalKind %v", op))
	}
	return Kind{family: FamilyOperational, op: op}
}

func SignalKind(path string) Kind {
	if path == "" {
		panic("events.SignalKind: empty signal type-path")
	}
	return Kind{family: FamilySignal, signalPath: path}
}

func (k Kind) Family() Family { return k.family }

func (k Kind) OperationalKind() genv1.OperationalKind { return k.op }

func (k Kind) SignalPath() string { return k.signalPath }

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

func (k Kind) IsZero() bool { return k.family == FamilyUnknown }

var ErrUnknownKind = errors.New("events.ParseKindString: unknown kind")

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
	genv1.OperationalKind_OPERATIONAL_KIND_ATTRIBUTE_OVERRIDE_MATCHED:      "attribute_override_matched",
	genv1.OperationalKind_OPERATIONAL_KIND_TEMPLATE_RESOLUTION_FAILED:      "template_resolution_failed",
	genv1.OperationalKind_OPERATIONAL_KIND_TEMPLATE_VALIDATION_FAILED:      "template_validation_failed",
	genv1.OperationalKind_OPERATIONAL_KIND_EXECUTOR_SCHEMA_UNAVAILABLE:     "executor_schema_unavailable",
	genv1.OperationalKind_OPERATIONAL_KIND_BREAKPOINT_HIT:                  "breakpoint.hit",
	genv1.OperationalKind_OPERATIONAL_KIND_MESSAGE_SENT:                    "message_sent",
	genv1.OperationalKind_OPERATIONAL_KIND_MESSAGE_RECEIVED:                "message_received",
	genv1.OperationalKind_OPERATIONAL_KIND_MESSAGE_DEAD_LETTERED:           "message.dead_lettered",
	genv1.OperationalKind_OPERATIONAL_KIND_FAN_OUT_DISPATCHED:              "fan_out_dispatched",
	genv1.OperationalKind_OPERATIONAL_KIND_FANOUT_CHILDREN_CREATED:         "fanout.children_created",
	genv1.OperationalKind_OPERATIONAL_KIND_SUBCLAIM_BEGIN_CANDIDATE:        "subclaim.begin_candidate",
	genv1.OperationalKind_OPERATIONAL_KIND_SUBCLAIM_ACQUIRED:               "subclaim.acquired",
	genv1.OperationalKind_OPERATIONAL_KIND_SUBGRAPH_INTERNAL_CASCADE_FIRED: "subgraph_internal_cascade_fired",
	genv1.OperationalKind_OPERATIONAL_KIND_SUBGRAPH_DISPATCHED:             "subgraph.dispatched",
	genv1.OperationalKind_OPERATIONAL_KIND_SUBGRAPH_EXIT_CARRY:             "subgraph.exit_carry",
	genv1.OperationalKind_OPERATIONAL_KIND_PARKED_RESUME_STARTED:           "parked_resume_started",
	genv1.OperationalKind_OPERATIONAL_KIND_DEBUG_OVERRIDE_APPLIED:          "debug.override.applied",
}

var operationalKindFromWire = func() map[string]genv1.OperationalKind {
	m := make(map[string]genv1.OperationalKind, len(operationalKindWireForm))
	for op, s := range operationalKindWireForm {
		m[s] = op
	}
	return m
}()

func init() {
	var missing []string
	for value, name := range genv1.OperationalKind_name {
		if value == int32(genv1.OperationalKind_OPERATIONAL_KIND_UNSPECIFIED) {
			continue
		}
		if _, ok := operationalKindWireForm[genv1.OperationalKind(value)]; !ok {
			missing = append(missing, name)
		}
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		panic(fmt.Sprintf("events: operationalKindWireForm is missing a wire form for proto OperationalKind value(s): %s — every non-UNSPECIFIED value in events.proto must have an entry or it silently String()s to \"\"", strings.Join(missing, ", ")))
	}
}

func AllOperationalKinds() []string {
	out := make([]string, 0, len(operationalKindWireForm))
	for _, s := range operationalKindWireForm {
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}

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
func KindMessageSent() Kind {
	return OperationalKindFromProto(genv1.OperationalKind_OPERATIONAL_KIND_MESSAGE_SENT)
}
func KindAttributeOverrideMatched() Kind {
	return OperationalKindFromProto(genv1.OperationalKind_OPERATIONAL_KIND_ATTRIBUTE_OVERRIDE_MATCHED)
}
func KindMessageReceived() Kind {
	return OperationalKindFromProto(genv1.OperationalKind_OPERATIONAL_KIND_MESSAGE_RECEIVED)
}
func KindMessageDeadLettered() Kind {
	return OperationalKindFromProto(genv1.OperationalKind_OPERATIONAL_KIND_MESSAGE_DEAD_LETTERED)
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
func KindParkedResumeStarted() Kind {
	return OperationalKindFromProto(genv1.OperationalKind_OPERATIONAL_KIND_PARKED_RESUME_STARTED)
}
func KindDebugOverrideApplied() Kind {
	return OperationalKindFromProto(genv1.OperationalKind_OPERATIONAL_KIND_DEBUG_OVERRIDE_APPLIED)
}
