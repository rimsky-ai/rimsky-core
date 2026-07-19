// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package compose

import (
	"strings"
	"testing"
)

func TestManifest_ExecutorTLSInvalid_NamesEntry(t *testing.T) {
	m := &Manifest{
		Project: "p",
		Executors: map[string]ManifestExecutorEntry{
			"stub": {
				Transport: "grpc",
				Endpoint:  "127.0.0.1:9091",
				TLS:       "optional",
			},
		},
	}
	err := m.Validate()
	if err == nil {
		t.Fatal("expected error for tls: optional, got nil")
	}
	if !strings.Contains(err.Error(), `executors["stub"].tls`) {
		t.Fatalf("error %q does not name the entry", err)
	}
	if !strings.Contains(err.Error(), "must be one of off|required") {
		t.Fatalf("error %q does not name the accepted values", err)
	}
}

func TestManifest_ExecutorTLSEmpty_Accepted(t *testing.T) {
	m := &Manifest{
		Project: "p",
		Executors: map[string]ManifestExecutorEntry{
			"stub": {
				Transport: "grpc",
				Endpoint:  "127.0.0.1:9091",
			},
		},
	}
	if err := m.Validate(); err != nil {
		t.Fatalf("empty tls must be accepted (default off), got %v", err)
	}
}

func TestManifest_ClaimProducerTLSInvalid_NamesEntry(t *testing.T) {
	m := &Manifest{
		Project: "p",
		ClaimProducers: map[string]ManifestClaimProducerEntry{
			"items": {
				Endpoint:              "127.0.0.1:9095",
				WriteSemanticsAllowed: []string{"sync"},
				TLS:                   "mutual",
			},
		},
	}
	err := m.Validate()
	if err == nil {
		t.Fatal("expected error for tls: mutual, got nil")
	}
	if !strings.Contains(err.Error(), `claim_producers["items"].tls`) {
		t.Fatalf("error %q does not name the entry", err)
	}
	if !strings.Contains(err.Error(), "must be one of off|required") {
		t.Fatalf("error %q does not name the accepted values", err)
	}
}
