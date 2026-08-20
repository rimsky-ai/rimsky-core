// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package config

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

func TestLoadRimskyConfigYAML_ValidatorsAndDataProcessorsTopLevelBlocks(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "rimsky.yml")
	body := `
persistence:
  driver: sqlite
  sqlite:
    path: /tmp/rimsky.db
validators:
  policy-checker:
    endpoint: grpc://policy-checker:9090
    tls: off
    protocols: [validation, executor, claim_producer]
data_processors:
  redactor:
    endpoint: grpc://redactor:9090
    tls: off
`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write rimsky.yml: %v", err)
	}
	cfg, err := LoadRimskyConfigYAML(path)
	if err != nil {
		t.Fatalf("LoadRimskyConfigYAML: top-level validators:/data_processors: blocks must parse: %v", err)
	}

	v, ok := cfg.Validators.Validators["policy-checker"]
	if !ok {
		t.Fatal("validators[policy-checker] missing after load")
	}
	if v.Endpoint != "grpc://policy-checker:9090" {
		t.Fatalf("validators[policy-checker].endpoint = %q, want grpc://policy-checker:9090", v.Endpoint)
	}
	wantProtocols := []string{"validation", "executor", "claim_producer"}
	if len(v.Protocols) != len(wantProtocols) {
		t.Fatalf("validators[policy-checker].protocols = %v, want %v", v.Protocols, wantProtocols)
	}

	dp, ok := cfg.DataProcessors.DataProcessors["redactor"]
	if !ok {
		t.Fatal("data_processors[redactor] missing after load")
	}
	if dp.Endpoint != "grpc://redactor:9090" {
		t.Fatalf("data_processors[redactor].endpoint = %q, want grpc://redactor:9090", dp.Endpoint)
	}
	if len(dp.Protocols) != 1 || dp.Protocols[0] != "data_processing" {
		t.Fatalf("data_processors[redactor].protocols = %v, want [data_processing] (default)", dp.Protocols)
	}
}

func TestLoadRimskyConfigYAML_ValidatorEntryMissingValidationProtocolRejected(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "rimsky.yml")
	body := `
persistence:
  driver: sqlite
  sqlite:
    path: /tmp/rimsky.db
validators:
  policy-checker:
    endpoint: grpc://policy-checker:9090
    protocols: [executor]
`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write rimsky.yml: %v", err)
	}
	_, err := LoadRimskyConfigYAML(path)
	if err == nil {
		t.Fatal("LoadRimskyConfigYAML: validators entry omitting the validation protocol must be rejected")
	}
}

// @concept: validation
func TestLoadRimskyConfigYAML_ValidatorEntryDerivingNoRoleRejected(t *testing.T) {
	for name, entry := range map[string]struct {
		protocols   string
		wantMessage string
	}{
		"declaring only validation": {protocols: "    protocols: [validation]\n", wantMessage: "protocols declares only"},
		"declaring no protocols":    {wantMessage: "protocols is absent"},
	} {
		protocols := entry.protocols
		wantMessage := entry.wantMessage
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "rimsky.yml")
			body := `
persistence:
  driver: sqlite
  sqlite:
    path: /tmp/rimsky.db
validators:
  policy-checker:
    endpoint: grpc://policy-checker:9090
` + protocols
			if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
				t.Fatalf("write rimsky.yml: %v", err)
			}
			_, err := LoadRimskyConfigYAML(path)
			if err == nil {
				t.Fatal("LoadRimskyConfigYAML: a standalone validator deriving no role must be rejected")
			}
			if !strings.Contains(err.Error(), "policy-checker") {
				t.Fatalf("the refusal must name the entry; got %v", err)
			}
			if !strings.Contains(err.Error(), wantMessage) {
				t.Fatalf("the refusal must say what the entry declared; want a message containing %q, got %v", wantMessage, err)
			}
		})
	}
}

func TestDialPublisherAndValidationRegistries_DialsStandaloneTopLevelBlocks(t *testing.T) {
	validators := RemoteValidatorsConfig{Validators: map[string]ValidatorEntry{
		"policy-checker": {
			Endpoint:  "grpc://127.0.0.1:0",
			Protocols: []string{"validation", "executor", "claim_producer"},
		},
	}}
	dataProcessors := RemoteDataProcessorsConfig{DataProcessors: map[string]DataProcessorEntry{
		"redactor": {
			Endpoint:  "grpc://127.0.0.1:0",
			Protocols: []string{"data_processing"},
		},
	}}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	_, validatorReg, dpReg, closers, err := DialPublisherAndValidationRegistries(
		ctx, RemoteClaimProducersConfig{}, ExecutorsConfig{}, RemotePublishersConfig{}, validators, dataProcessors)
	if err != nil {
		t.Fatalf("DialPublisherAndValidationRegistries: %v", err)
	}
	defer func() {
		for _, c := range closers {
			c()
		}
	}()

	client, ok := validatorReg.Get("policy-checker")
	if !ok {
		t.Fatal("validators registry missing standalone top-level entry policy-checker")
	}
	got := append([]string(nil), client.SupportedRoles()...)
	sort.Strings(got)
	want := []string{"claim_producer", "executor"}
	if len(got) != len(want) {
		t.Fatalf("policy-checker.SupportedRoles() = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("policy-checker.SupportedRoles() = %v, want %v", got, want)
		}
	}

	if _, ok := dpReg.Get("redactor"); !ok {
		t.Fatal("data-processing registry missing standalone top-level entry redactor")
	}
}
