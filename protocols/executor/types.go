// Copyright © 2026 Fall Guy Consulting.
// Licensed under the Apache License, Version 2.0. See LICENSE.apache at the
// repo root, or http://www.apache.org/licenses/LICENSE-2.0.

package executor

import "encoding/json"

// ExecuteRequest mirrors the proto ExecuteRequest message. The userdata,
// attributes, and store handle fields are opaque to Rimsky; the executor
// alone interprets them.
type ExecuteRequest struct {
	NodeID           string
	InstanceID       string
	NodeType         string
	Userdata         json.RawMessage
	Attributes       json.RawMessage
	AttributesSchema json.RawMessage
	Stores           map[string]StoreHandle
	CallbackURL      string
	CancelToken      string
	DispatchID       string
	ResumeContext    *ResumeContext
}

// ResumeContext mirrors the proto ResumeContext message. When non-nil
// this dispatch is a resume of a previously parked run; rimsky echoes
// back the original Park payload + session_token plus a
// resume_reason describing why the wake fired.
type ResumeContext struct {
	Payload      []byte
	SessionToken string
	// One of "deadline_elapsed" | "external_invalidate".
	ResumeReason string
}

// StoreHandle is the per-claim reference handed to the executor at
// dispatch. Handle is opaque bytes returned by ClaimProducer.Open;
// the executor decodes per its producer-specific knowledge.
type StoreHandle struct {
	Kind   string
	Handle json.RawMessage
}

// ExecuteEvent is the per-record event type yielded by the Execute
// stream. Three categories mirror the proto oneof: Heartbeat (zero or
// more), NamedEvent (zero or more), StreamClose (exactly one final
// record carrying the outcome). Exactly one field MUST be non-nil per
// record.
type ExecuteEvent struct {
	Heartbeat   *Heartbeat
	NamedEvent  *NamedEvent
	StreamClose *StreamClose
}

// Heartbeat is a non-terminal progress indicator.
type Heartbeat struct {
	TimestampMs int64
	Note        string
}

// NamedEvent is a non-terminal record an executor MAY emit zero or
// more times during a run. `Name` MUST appear in the executor's
// ObservabilityCapabilities.declared_events; rimsky validates at
// template registration and rejects undeclared emissions at dispatch.
// Payload bytes are inert to rimsky.
type NamedEvent struct {
	Name    string
	Payload []byte
}

// StreamClose terminates the Execute stream. Exactly one is emitted
// per successful Execute call. The Outcome fields are a oneof —
// exactly one MUST be non-nil. The supervisor routes by which
// outcome variant is populated.
type StreamClose struct {
	Success    *Success
	Error      *Error
	Park       *Park
	AwaitAsync *AwaitAsyncCallback
}

// Success reports a successful executor run with a producer-declared
// `Changed` verdict. Optional AttributesDelta carries terminal-final
// attribute writeback; nil/empty for the incremental-via-callback
// pattern.
type Success struct {
	Changed         bool
	ChangeSummary   string
	AttributesDelta json.RawMessage
}

// Error reports an executor error. ErrorClass is the discriminator the
// operator-side `error_types:` policy routes on. Common classes
// include "executor_blocked" (the pre-Phase-E.2 Blocked terminal
// collapsed into this), "rate_limited", "transient_io", etc.
type Error struct {
	ErrorClass string
	Payload    json.RawMessage
}

// Park signals that the executor wants to pause the node and resume
// later. The held claim handle is retained across the park boundary;
// on resume, rimsky re-dispatches with ExecuteRequest.ResumeContext
// populated from Payload + SessionToken captured here. ResumeAtMs may
// be zero (signal-based-only); when non-zero, the supervisor's
// SweepParkedNodes sweep wakes the node at that wall-clock time with
// resume_reason = "deadline_elapsed".
type Park struct {
	Reason       string
	Payload      []byte
	ResumeAtMs   int64
	SessionToken string
}

// AwaitAsyncCallback (formerly AsyncAccepted) signals that the
// executor has accepted the work but will report the final outcome
// later via HTTP+JSON POST to ExecuteRequest.CallbackURL. The
// supervisor holds the dispatch row claim and keeps the node in
// running state until the callback arrives (subject to heartbeat-loss
// sweep).
type AwaitAsyncCallback struct {
	AsyncAckID           string
	ExpectedCompletionMs int64
}
