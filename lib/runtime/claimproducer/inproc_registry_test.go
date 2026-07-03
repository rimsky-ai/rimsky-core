// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package claimproducer

import (
	"context"
	"strings"
	"testing"

	protocol "github.com/rimsky-ai/rimsky-core/lib/protocols/claimproducer"
	"github.com/rimsky-ai/rimsky-core/lib/runtime/clientiface"
)

func coreCapabilities() protocol.Capabilities {
	return protocol.Capabilities{
		WriteSemanticsAllowed: []protocol.WriteSemantics{protocol.WriteSemanticsSync},
	}
}

func coreRegistration() Registration {
	return Registration{
		Handler:      &fakeHandler{},
		Capabilities: coreCapabilities(),
	}
}

func TestRegisterAndLookup(t *testing.T) {
	r := NewInProcessRegistry()
	if err := r.Register("items", coreRegistration()); err != nil {
		t.Fatalf("Register: %v", err)
	}
	reg, ok := r.Lookup("items")
	if !ok {
		t.Fatal("Lookup(items) not found")
	}
	if reg.Handler == nil {
		t.Fatal("Lookup(items) returned nil handler")
	}
	if _, ok := r.Lookup("absent"); ok {
		t.Fatal("Lookup(absent) unexpectedly found")
	}
}

func TestRegisterDuplicateFails(t *testing.T) {
	r := NewInProcessRegistry()
	if err := r.Register("items", coreRegistration()); err != nil {
		t.Fatalf("first Register: %v", err)
	}
	err := r.Register("items", coreRegistration())
	if err == nil || !strings.Contains(err.Error(), "duplicate registration") {
		t.Fatalf("duplicate Register: want duplicate-registration error, got %v", err)
	}
}

func TestRegisterValidation(t *testing.T) {
	cases := []struct {
		name    string
		bind    string
		mutate  func(*Registration)
		wantErr string
	}{
		{"empty name", "", func(*Registration) {}, "name is required"},
		{"nil handler", "items", func(reg *Registration) { reg.Handler = nil }, "Handler is required"},
		{"empty envelope", "items", func(reg *Registration) { reg.Capabilities.WriteSemanticsAllowed = nil }, "write_semantics_allowed is empty"},
		{"unknown write semantics", "items", func(reg *Registration) {
			reg.Capabilities.WriteSemanticsAllowed = []protocol.WriteSemantics{protocol.WriteSemantics("bogus")}
		}, "unknown write_semantics value"},
		{"validation advertised without client", "items", func(reg *Registration) {
			reg.Capabilities.Protocols = []string{protocol.ProtocolValidation}
		}, "validation mix-in"},
		{"validation client without advertisement", "items", func(reg *Registration) {
			reg.Validation = fakeValidation{}
		}, "validation mix-in"},
		{"data-processing advertised without client", "items", func(reg *Registration) {
			reg.Capabilities.Protocols = []string{protocol.ProtocolDataProcessing}
		}, "data-processing mix-in"},
		{"data-processing client without advertisement", "items", func(reg *Registration) {
			reg.DataProcessing = fakeDataProcessing{}
		}, "data-processing mix-in"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			reg := coreRegistration()
			tc.mutate(&reg)
			err := NewInProcessRegistry().Register(tc.bind, reg)
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("want error containing %q, got %v", tc.wantErr, err)
			}
		})
	}
}

func TestRegisteredNamesSorted(t *testing.T) {
	r := NewInProcessRegistry()
	for _, name := range []string{"zeta", "alpha", "mid"} {
		if err := r.Register(name, coreRegistration()); err != nil {
			t.Fatalf("Register(%s): %v", name, err)
		}
	}
	got := r.RegisteredNames()
	want := []string{"alpha", "mid", "zeta"}
	if len(got) != len(want) {
		t.Fatalf("RegisteredNames: got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("RegisteredNames: got %v, want %v", got, want)
		}
	}
}

func TestClientAccessor(t *testing.T) {
	r := NewInProcessRegistry()
	if err := r.Register("items", coreRegistration()); err != nil {
		t.Fatalf("Register: %v", err)
	}
	c, ok := r.Client("items")
	if !ok {
		t.Fatal("Client(items) not found")
	}
	if c.Name() != "items" {
		t.Fatalf("Client name: got %q, want items", c.Name())
	}
	caps, err := c.Capabilities(context.Background())
	if err != nil {
		t.Fatalf("Capabilities: %v", err)
	}
	if !caps.Contains(protocol.WriteSemanticsSync) {
		t.Fatalf("Capabilities: envelope missing sync: %v", caps.WriteSemanticsAllowed)
	}
	if _, ok := r.Client("absent"); ok {
		t.Fatal("Client(absent) unexpectedly found")
	}
}

func TestMixinViews(t *testing.T) {
	r := NewInProcessRegistry()
	full := coreRegistration()
	full.Capabilities.Protocols = []string{protocol.ProtocolValidation, protocol.ProtocolDataProcessing}
	full.Validation = fakeValidation{}
	full.DataProcessing = fakeDataProcessing{}
	if err := r.Register("full", full); err != nil {
		t.Fatalf("Register(full): %v", err)
	}
	if err := r.Register("core-only", coreRegistration()); err != nil {
		t.Fatalf("Register(core-only): %v", err)
	}

	if _, ok := r.Validations().Get("full"); !ok {
		t.Fatal("Validations().Get(full) not found")
	}
	if _, ok := r.Validations().Get("core-only"); ok {
		t.Fatal("Validations().Get(core-only) unexpectedly found")
	}
	if _, ok := r.Validations().Get("absent"); ok {
		t.Fatal("Validations().Get(absent) unexpectedly found")
	}
	if _, ok := r.DataProcessors().Get("full"); !ok {
		t.Fatal("DataProcessors().Get(full) not found")
	}
	if _, ok := r.DataProcessors().Get("core-only"); ok {
		t.Fatal("DataProcessors().Get(core-only) unexpectedly found")
	}
}

type fakeValidation struct{}

func (fakeValidation) Name() string             { return "fake-validation" }
func (fakeValidation) SupportedRoles() []string { return []string{"claim_producer"} }
func (fakeValidation) ValidateExecutor(context.Context, clientiface.ValidateExecutorInput) ([]clientiface.ValidationFinding, []clientiface.ValidationFinding, error) {
	return nil, nil, nil
}
func (fakeValidation) ValidateClaimProducer(context.Context, clientiface.ValidateClaimProducerInput) ([]clientiface.ValidationFinding, []clientiface.ValidationFinding, error) {
	return nil, nil, nil
}
func (fakeValidation) ValidatePublisher(context.Context, clientiface.ValidatePublisherInput) ([]clientiface.ValidationFinding, []clientiface.ValidationFinding, error) {
	return nil, nil, nil
}
func (fakeValidation) ValidateLifecycleSubscriber(context.Context, clientiface.ValidateLifecycleSubscriberInput) ([]clientiface.ValidationFinding, []clientiface.ValidationFinding, error) {
	return nil, nil, nil
}

type fakeDataProcessing struct{}

func (fakeDataProcessing) Name() string { return "fake-data-processing" }
func (fakeDataProcessing) BeginCandidate(context.Context, clientiface.BeginCandidateInput) (clientiface.BeginCandidateOutput, error) {
	return clientiface.BeginCandidateOutput{}, nil
}
func (fakeDataProcessing) CommitCandidate(context.Context, clientiface.CommitCandidateInput) (clientiface.CommitCandidateOutput, error) {
	return clientiface.CommitCandidateOutput{}, nil
}
func (fakeDataProcessing) AbandonCandidate(context.Context, clientiface.AbandonCandidateInput) error {
	return nil
}
func (fakeDataProcessing) ListVersions(context.Context, clientiface.ListVersionsInput) (clientiface.ListVersionsOutput, error) {
	return clientiface.ListVersionsOutput{}, nil
}
func (fakeDataProcessing) ListPartitions(context.Context, clientiface.ListPartitionsInput) (clientiface.ListPartitionsOutput, error) {
	return clientiface.ListPartitionsOutput{}, nil
}
func (fakeDataProcessing) GetVersionSchema(context.Context, clientiface.GetVersionSchemaInput) (clientiface.GetVersionSchemaOutput, error) {
	return clientiface.GetVersionSchemaOutput{}, nil
}
