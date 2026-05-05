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
	RunAttempt       int32
	DispatchID       string
}

// StoreHandle is the per-claim reference handed to the executor at
// dispatch. Address is opaque bytes returned by ClaimProducer.Open;
// the executor decodes per its producer-specific knowledge.
type StoreHandle struct {
	Kind   string
	Handle json.RawMessage
}

// ExecuteEvent is the union of events the executor may stream back.
// Exactly one terminal event (Complete | Blocked | Errored |
// AsyncAccepted) MUST close the stream; zero or more Heartbeat events
// may precede it.
type ExecuteEvent struct {
	Heartbeat     *Heartbeat
	Complete      *Complete
	Blocked       *Blocked
	Errored       *Errored
	AsyncAccepted *AsyncAccepted
}

// Heartbeat is a non-terminal progress indicator.
type Heartbeat struct {
	TimestampMs int64
	Note        string
}

// Complete is a terminal event reporting successful execution.
type Complete struct {
	Changed         bool
	ChangeSummary   string
	AttributesDelta json.RawMessage
}

// Blocked is a terminal event reporting that the executor cannot
// progress without external intervention.
type Blocked struct {
	Reason  string
	Context json.RawMessage
}

// Errored is a terminal event reporting an application-level error.
type Errored struct {
	ErrorClass string
	Payload    json.RawMessage
}

// AsyncAccepted is a terminal event signaling the executor has accepted
// the work and will report the final outcome via HTTP+JSON callback.
type AsyncAccepted struct {
	AsyncAckID           string
	ExpectedCompletionMs int64
}
