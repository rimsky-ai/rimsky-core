// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package server

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rimsky-ai/rimsky-core/lib/protocols/claimproducer"
)

func writeOptsConfig(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write %q: %v", path, err)
	}
}

func TestLoadOptsFromEnv_LedgerMaxRecordsWiredThrough(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")
	writeOptsConfig(t, cfgPath, "connection: postgres://example/db\nledger_max_records: 2048\n")

	t.Setenv(ConfigEnv, cfgPath)

	opts, err := LoadOptsFromEnv()
	if err != nil {
		t.Fatalf("LoadOptsFromEnv: %v", err)
	}
	if opts.LedgerMaxRecords != 2048 {
		t.Fatalf("Opts.LedgerMaxRecords = %d, want 2048", opts.LedgerMaxRecords)
	}
	if opts.ServerConfig().LedgerMaxRecords != 2048 {
		t.Fatalf("ServerConfig().LedgerMaxRecords = %d, want 2048", opts.ServerConfig().LedgerMaxRecords)
	}
}

func TestLoadOptsFromEnv_LedgerMaxRecordsDefaultsZero(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")
	writeOptsConfig(t, cfgPath, "connection: postgres://example/db\n")

	t.Setenv(ConfigEnv, cfgPath)

	opts, err := LoadOptsFromEnv()
	if err != nil {
		t.Fatalf("LoadOptsFromEnv: %v", err)
	}
	if opts.LedgerMaxRecords != 0 {
		t.Fatalf("Opts.LedgerMaxRecords = %d, want 0 (unset, store applies its own default)", opts.LedgerMaxRecords)
	}
}

func TestLoadOptsFromEnv_UnrecognizedWriteSemanticsFailsStartup(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")
	writeOptsConfig(t, cfgPath, "connection: postgres://example/db\nwrite_semantics: staged_asnyc\n")

	t.Setenv(ConfigEnv, cfgPath)

	_, err := LoadOptsFromEnv()
	if err == nil {
		t.Fatal("LoadOptsFromEnv: expected an error for a typo'd write_semantics value, got nil")
	}
	if !strings.Contains(err.Error(), "write_semantics") {
		t.Fatalf("LoadOptsFromEnv error = %q, want it to name write_semantics", err.Error())
	}
}

func TestLoadOptsFromEnv_WriteSemanticsDefaultsToStagedAsync(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")
	writeOptsConfig(t, cfgPath, "connection: postgres://example/db\n")

	t.Setenv(ConfigEnv, cfgPath)

	opts, err := LoadOptsFromEnv()
	if err != nil {
		t.Fatalf("LoadOptsFromEnv: %v", err)
	}
	if opts.WriteSemantics != claimproducer.WriteSemanticsStagedAsync {
		t.Fatalf("Opts.WriteSemantics = %q, want %q", opts.WriteSemantics, claimproducer.WriteSemanticsStagedAsync)
	}
}

func TestLoadOptsFromEnv_PortsDefaultToDocumentedValuesWhenOmitted(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")
	writeOptsConfig(t, cfgPath, "connection: postgres://example/db\n")

	t.Setenv(ConfigEnv, cfgPath)

	opts, err := LoadOptsFromEnv()
	if err != nil {
		t.Fatalf("LoadOptsFromEnv: %v", err)
	}
	if opts.GRPCPort != defaultGRPCPort {
		t.Fatalf("Opts.GRPCPort = %d, want documented default %d (an omitted grpc_port must not silently bind an ephemeral port)", opts.GRPCPort, defaultGRPCPort)
	}
	if opts.HTTPPort != defaultHTTPPort {
		t.Fatalf("Opts.HTTPPort = %d, want documented default %d", opts.HTTPPort, defaultHTTPPort)
	}
}

func TestLoadOptsFromEnv_ExplicitPortsWinOverDefaults(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")
	writeOptsConfig(t, cfgPath, "connection: postgres://example/db\ngrpc_port: 5555\nhttp_port: 5556\n")

	t.Setenv(ConfigEnv, cfgPath)

	opts, err := LoadOptsFromEnv()
	if err != nil {
		t.Fatalf("LoadOptsFromEnv: %v", err)
	}
	if opts.GRPCPort != 5555 || opts.HTTPPort != 5556 {
		t.Fatalf("Opts = {GRPCPort:%d HTTPPort:%d}, want explicit 5555/5556", opts.GRPCPort, opts.HTTPPort)
	}
}

func TestLoadOptsFromEnv_UndefinedEnvVarInConfigFailsStartup(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")
	writeOptsConfig(t, cfgPath, "connection: \"postgres://user:${RIMSKY_CLAIM_PRODUCER_POSTGRES_TEST_UNDEFINED_VAR}@db/rimsky\"\n")

	t.Setenv(ConfigEnv, cfgPath)
	os.Unsetenv("RIMSKY_CLAIM_PRODUCER_POSTGRES_TEST_UNDEFINED_VAR")

	_, err := LoadOptsFromEnv()
	if err == nil {
		t.Fatal("LoadOptsFromEnv: expected an error for an undefined ${VAR} reference, got nil")
	}
	if !strings.Contains(err.Error(), "RIMSKY_CLAIM_PRODUCER_POSTGRES_TEST_UNDEFINED_VAR") {
		t.Fatalf("LoadOptsFromEnv error = %q, want it to name the undefined variable", err.Error())
	}
}

func TestLoadOptsFromEnv_ExplicitWriteSemanticsWiredThrough(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")
	writeOptsConfig(t, cfgPath, "connection: postgres://example/db\nwrite_semantics: sync\n")

	t.Setenv(ConfigEnv, cfgPath)

	opts, err := LoadOptsFromEnv()
	if err != nil {
		t.Fatalf("LoadOptsFromEnv: %v", err)
	}
	if opts.WriteSemantics != claimproducer.WriteSemanticsSync {
		t.Fatalf("Opts.WriteSemantics = %q, want %q", opts.WriteSemantics, claimproducer.WriteSemanticsSync)
	}
}

func TestLoadOptsFromEnv_PartitionPolicyParamOrderPreservesYAMLKeyOrder(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")
	writeOptsConfig(t, cfgPath, `connection: postgres://example/db
partition_policies:
  by_region:
    items_table: items
    select: "*"
    where: "region = ANY_REGION AND category = ANY_CATEGORY"
    limit: 10
    params_schema:
      properties:
        region: {type: string}
        category: {type: string}
        zeta: {type: string}
        alpha: {type: string}
`)
	t.Setenv(ConfigEnv, cfgPath)

	opts, err := LoadOptsFromEnv()
	if err != nil {
		t.Fatalf("LoadOptsFromEnv: %v", err)
	}
	pp, ok := opts.PartitionPolicies["by_region"]
	if !ok {
		t.Fatalf("PartitionPolicies missing %q", "by_region")
	}
	want := []string{"region", "category", "zeta", "alpha"}
	if len(pp.ParamOrder) != len(want) {
		t.Fatalf("ParamOrder = %v, want %v", pp.ParamOrder, want)
	}
	for i, k := range want {
		if pp.ParamOrder[i] != k {
			t.Fatalf("ParamOrder[%d] = %q, want %q (YAML key order is load-bearing for $1..$N SQL binding); got full order %v",
				i, pp.ParamOrder[i], k, pp.ParamOrder)
		}
	}
}

func TestLoadOptsFromEnv_PickPolicyRejectsInvalidItemsTableIdentifier(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")
	writeOptsConfig(t, cfgPath, `connection: postgres://example/db
pick_policies:
  "@queue":
    items_table: "items; DROP TABLE users;--"
    on_commit: recycle
    on_give_up: recycle
`)
	t.Setenv(ConfigEnv, cfgPath)

	_, err := LoadOptsFromEnv()
	if err == nil {
		t.Fatal("LoadOptsFromEnv: expected an error for a non-identifier items_table, got nil")
	}
	if !strings.Contains(err.Error(), "items_table") {
		t.Fatalf("LoadOptsFromEnv error = %q, want it to name items_table", err.Error())
	}
}

func TestLoadOptsFromEnv_PartitionPolicyRejectsInvalidItemsTableIdentifier(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")
	writeOptsConfig(t, cfgPath, `connection: postgres://example/db
partition_policies:
  by_region:
    items_table: "1items"
    select: "*"
    where: "1=1"
`)
	t.Setenv(ConfigEnv, cfgPath)

	_, err := LoadOptsFromEnv()
	if err == nil {
		t.Fatal("LoadOptsFromEnv: expected an error for an items_table starting with a digit, got nil")
	}
	if !strings.Contains(err.Error(), "items_table") {
		t.Fatalf("LoadOptsFromEnv error = %q, want it to name items_table", err.Error())
	}
}
