// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package runtime

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/rimsky-ai/rimsky-core/lib/protocols/claimproducer"
)

// abandonStub is a minimal locks.ClaimProducer test double that
// records the most-recent Abandon call and returns a preset error.
// ClaimProducer's wire protocol is 4 verbs (Open / Commit / Abandon /
// Release) plus the Capabilities() startup handshake; the Go interface
// additionally carries Name() as a rimsky-side identifier not
// transported on the wire. The stub implements all six so it satisfies
// the interface, but only Abandon is exercised.
type abandonStub struct {
	lastClaimID claimproducer.ClaimID
	lastScope   []byte
	lastAddress []byte
	abandonErr  error
}

func (s *abandonStub) Name() string { return "abandon-stub" }

func (s *abandonStub) Capabilities(context.Context) (claimproducer.Capabilities, error) {
	return claimproducer.Capabilities{}, errors.New("Capabilities not implemented in stub")
}

func (s *abandonStub) Open(context.Context, claimproducer.ClaimID, claimproducer.ClaimSpec) (claimproducer.OpenOutcome, error) {
	return claimproducer.OpenOutcome{}, errors.New("Open not implemented in stub")
}

func (s *abandonStub) Commit(context.Context, claimproducer.ClaimID, []byte, []byte) error {
	return errors.New("Commit not implemented in stub")
}

func (s *abandonStub) Abandon(_ context.Context, claimID claimproducer.ClaimID, scope, address []byte) error {
	s.lastClaimID = claimID
	s.lastScope = scope
	s.lastAddress = address
	return s.abandonErr
}

func (s *abandonStub) Release(context.Context, claimproducer.ClaimID, []byte, []byte) error {
	return errors.New("Release not implemented in stub")
}

func (s *abandonStub) SplitScope(context.Context, claimproducer.SplitClaimScopeRequest) (claimproducer.SplitClaimScopeResponse, error) {
	return claimproducer.SplitClaimScopeResponse{}, errors.New("SplitScope not implemented in stub")
}

func (s *abandonStub) ScopesConflict(_ context.Context, a, b []byte) (bool, error) {
	return string(a) == string(b), nil
}

func TestAbandonOpenedClaim(t *testing.T) {
	t.Parallel()

	handleID := uuid.MustParse("00000000-0000-4000-8000-000000000001")
	scope := []byte(`{"path":"/tmp/x"}`)
	address := []byte(`{"version":"abc"}`)

	t.Run("forwards args to producer.Abandon", func(t *testing.T) {
		stub := &abandonStub{}
		err := abandonOpenedClaim(context.Background(), stub, handleID, scope, address)
		if err != nil {
			t.Fatalf("expected nil, got %v", err)
		}
		want := claimproducer.ClaimID(handleID.String())
		if stub.lastClaimID != want {
			t.Errorf("claim_id = %q, want %q", stub.lastClaimID, want)
		}
		if string(stub.lastScope) != string(scope) {
			t.Errorf("scope = %q, want %q", stub.lastScope, scope)
		}
		if string(stub.lastAddress) != string(address) {
			t.Errorf("address = %q, want %q", stub.lastAddress, address)
		}
	})

	t.Run("returns producer.Abandon error", func(t *testing.T) {
		want := errors.New("producer go boom")
		stub := &abandonStub{abandonErr: want}
		err := abandonOpenedClaim(context.Background(), stub, handleID, scope, address)
		if !errors.Is(err, want) {
			t.Errorf("err = %v, want %v", err, want)
		}
	})
}
