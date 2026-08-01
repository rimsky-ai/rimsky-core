// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: Apache-2.0

package serverkit

import (
	"errors"

	"github.com/rimsky-ai/rimsky-core/lib/protocols/claimproducer"
	genv1 "github.com/rimsky-ai/rimsky-core/lib/protocols/proto/v1/gen"
)

var ErrCapabilitiesUnknownWriteSemantics = errors.New("Capabilities advertises UNKNOWN write_semantics value")

var ErrCapabilitiesEmptyWriteSemantics = errors.New("Capabilities returned empty write_semantics_allowed")

var ErrOpenResponseMissingOutcome = errors.New("Open: response carries neither Acquired nor Unavailable")

func ClaimProducerCapabilitiesFromProto(resp *genv1.CapabilitiesResponse) (claimproducer.Capabilities, error) {
	envelope := make([]claimproducer.WriteSemantics, 0, len(resp.GetWriteSemanticsAllowed()))
	for _, ws := range resp.GetWriteSemanticsAllowed() {
		mapped, ok := claimproducer.ParseWriteSemantics(WriteSemanticsFromProto(ws))
		if !ok {
			return claimproducer.Capabilities{}, ErrCapabilitiesUnknownWriteSemantics
		}
		envelope = append(envelope, mapped)
	}
	if len(envelope) == 0 {
		return claimproducer.Capabilities{}, ErrCapabilitiesEmptyWriteSemantics
	}
	return claimproducer.Capabilities{
		WriteSemanticsAllowed:    envelope,
		SupportsSplitScope:       resp.GetSupportsSplitScope(),
		SupportsScopesConflict:   resp.GetSupportsScopesConflict(),
		Protocols:                resp.GetProtocols(),
		ValidationSupportedRoles: resp.GetValidationSupportedRoles(),
		DeclaredErrorClasses:     resp.GetDeclaredErrorClasses(),
	}, nil
}

func OpenRequestFromSpec(claimID claimproducer.ClaimID, spec claimproducer.ClaimSpec) *genv1.OpenRequest {
	return &genv1.OpenRequest{
		ClaimId:      string(claimID),
		ProducerName: spec.ProducerName,
		Selector:     spec.Selector,
		Intent:       string(spec.Intent),
		Alias:        spec.Alias,
		TemplateId:   spec.TemplateID,
		InstanceId:   spec.InstanceID,
		RunScopeId:   spec.RunScopeID,
		Lifetime:     spec.Lifetime,
	}
}

func OpenOutcomeFromProto(resp *genv1.OpenResponse) (claimproducer.OpenOutcome, error) {
	if u := resp.GetUnavailable(); u != nil {
		return claimproducer.OpenOutcome{Available: false, UnavailableClass: u.GetErrorClass()}, nil
	}
	acq := resp.GetAcquired()
	if acq == nil {
		return claimproducer.OpenOutcome{}, ErrOpenResponseMissingOutcome
	}
	return claimproducer.OpenOutcome{
		Available: true,
		Result: claimproducer.ClaimResult{
			Address:                acq.GetAddress(),
			Payload:                acq.GetPayload(),
			ClaimScope:             acq.GetClaimScope(),
			RealizedWriteSemantics: claimproducer.WriteSemantics(WriteSemanticsFromProto(acq.GetRealizedWriteSemantics())),
		},
	}, nil
}

func CommitRequestFromArgs(claimID claimproducer.ClaimID, scope, address []byte, leaseToken string) *genv1.CommitRequest {
	return &genv1.CommitRequest{ClaimId: string(claimID), ClaimScope: scope, Address: address, LeaseToken: leaseToken}
}

func CommitResultFromProto(resp *genv1.CommitResponse) claimproducer.CommitResult {
	return claimproducer.CommitResult{
		VersionID:        resp.GetVersionId(),
		ProducerMetadata: resp.GetProducerMetadata(),
	}
}

func AbandonRequestFromArgs(claimID claimproducer.ClaimID, scope, address []byte, leaseToken string) *genv1.AbandonRequest {
	return &genv1.AbandonRequest{ClaimId: string(claimID), ClaimScope: scope, Address: address, LeaseToken: leaseToken}
}

func ReleaseRequestFromArgs(claimID claimproducer.ClaimID, scope, address []byte, leaseToken string) *genv1.ReleaseRequest {
	return &genv1.ReleaseRequest{ClaimId: string(claimID), ClaimScope: scope, Address: address, LeaseToken: leaseToken}
}

func SplitScopeRequestToProto(req claimproducer.SplitClaimScopeRequest) *genv1.SplitScopeRequest {
	return &genv1.SplitScopeRequest{
		ClaimHandleId:    req.ClaimHandleID,
		PartitionRequest: req.PartitionRequest,
	}
}

func SplitScopeResponseFromProto(resp *genv1.SplitScopeResponse) claimproducer.SplitClaimScopeResponse {
	out := claimproducer.SplitClaimScopeResponse{}
	for _, sub := range resp.GetSubScopes() {
		out.SubClaimScopes = append(out.SubClaimScopes, claimproducer.SubClaimScopeDescriptor{
			ClaimScopeData:   sub.GetClaimScopeData(),
			PartitionKey:     sub.GetPartitionKey(),
			ProducerMetadata: sub.GetProducerMetadata(),
			Address:          sub.GetAddress(),
			Payload:          sub.GetPayload(),
			LeaseToken:       sub.GetLeaseToken(),
		})
	}
	return out
}
