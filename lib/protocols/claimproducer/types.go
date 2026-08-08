// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: Apache-2.0

package claimproducer

import "encoding/json"

// @concept: write-semantics
type WriteSemantics string

const (
	WriteSemanticsUnknown WriteSemantics = ""

	WriteSemanticsSync WriteSemantics = "sync"

	WriteSemanticsStagedAsync WriteSemantics = "staged_async"

	WriteSemanticsBlockingAsync WriteSemantics = "blocking_async"

	WriteSemanticsReadOnly WriteSemantics = "read_only"
)

func (w WriteSemantics) String() string { return string(w) }

func ParseWriteSemantics(s string) (WriteSemantics, bool) {
	switch s {
	case string(WriteSemanticsSync):
		return WriteSemanticsSync, true
	case string(WriteSemanticsStagedAsync):
		return WriteSemanticsStagedAsync, true
	case string(WriteSemanticsBlockingAsync):
		return WriteSemanticsBlockingAsync, true
	case string(WriteSemanticsReadOnly):
		return WriteSemanticsReadOnly, true
	default:
		return WriteSemanticsUnknown, false
	}
}

type ClaimID string

type Intent string

const (
	IntentRead      Intent = "r"
	IntentReadWrite Intent = "rw"
)

type ClaimSpec struct {
	ProducerName string
	Selector     string
	Intent       Intent
	Alias        string
	TemplateID   string
	InstanceID   string
	// @concept: host-agent-proxy
	RunScopeID string
	// @concept: claim-lifetime
	Lifetime string
	// @concept: inertness
	Data []byte
}

type ClaimResult struct {
	Address                json.RawMessage
	Payload                json.RawMessage
	ClaimScope             json.RawMessage
	RealizedWriteSemantics WriteSemantics
}

type CommitResult struct {
	VersionID        string
	ProducerMetadata []byte
}

type OpenOutcome struct {
	Available        bool
	Result           ClaimResult
	UnavailableClass string
}

type Capabilities struct {
	WriteSemanticsAllowed    []WriteSemantics
	SupportsSplitScope       bool
	SupportsScopesConflict   bool
	Protocols                []string
	ValidationSupportedRoles []string
	DeclaredErrorClasses     []string
}

func (c Capabilities) Contains(w WriteSemantics) bool {
	for _, v := range c.WriteSemanticsAllowed {
		if v == w {
			return true
		}
	}
	return false
}

func (c Capabilities) AdvertisesProtocol(p string) bool {
	for _, v := range c.Protocols {
		if v == p {
			return true
		}
	}
	return false
}

type SplitClaimScopeRequest struct {
	ClaimHandleID    string
	PartitionRequest []byte
}

type SubClaimScopeDescriptor struct {
	ClaimScopeData   []byte
	PartitionKey     string
	ProducerMetadata []byte
	Address          []byte
	Payload          []byte
	LeaseToken       string
}

type SplitClaimScopeResponse struct {
	SubClaimScopes []SubClaimScopeDescriptor
}

const (
	ProtocolDataProcessing      = "data_processing"
	ProtocolValidation          = "validation"
	ProtocolLifecycleSubscriber = "lifecycle_subscriber"
	ProtocolExecutor            = "executor"
)
