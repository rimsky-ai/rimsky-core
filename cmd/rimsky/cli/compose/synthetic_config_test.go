// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package compose

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rimsky-ai/rimsky-core/lib/control/config"
	"gopkg.in/yaml.v3"
)

func TestWriteSyntheticRimskyYAML_PathsCorrect(t *testing.T) {
	tmp := t.TempDir()
	runDir, err := EnsureRunDir(tmp, "2026-06-13T10-00-00Z", "demo")
	if err != nil {
		t.Fatalf("EnsureRunDir: %v", err)
	}
	m := &Manifest{Project: "demo"}
	if err := WriteSyntheticRimskyYAML(runDir, m, nil, nil, 0); err != nil {
		t.Fatalf("WriteSyntheticRimskyYAML: %v", err)
	}
	cfg, err := config.LoadRimskyConfigYAML(filepath.Join(runDir, "rimsky.yml"))
	if err != nil {
		t.Fatalf("LoadRimskyConfigYAML round-trip: %v", err)
	}
	if cfg.Persistence.Driver != "sqlite" {
		t.Fatalf("persistence.driver: got %q, want %q", cfg.Persistence.Driver, "sqlite")
	}
	if cfg.Persistence.SQLite == nil {
		t.Fatalf("persistence.sqlite block missing")
	}
	wantSQLitePath := filepath.Join(runDir, "state.db")
	if cfg.Persistence.SQLite.Path != wantSQLitePath {
		t.Fatalf("persistence.sqlite.path: got %q, want %q", cfg.Persistence.SQLite.Path, wantSQLitePath)
	}
	if cfg.Blob.Backend != "filesystem" {
		t.Fatalf("persistence.blob.backend: got %q, want %q", cfg.Blob.Backend, "filesystem")
	}
	wantBlobRoot := filepath.Join(runDir, "blobs")
	if cfg.Blob.Filesystem.Root != wantBlobRoot {
		t.Fatalf("persistence.blob.filesystem.root: got %q, want %q", cfg.Blob.Filesystem.Root, wantBlobRoot)
	}
}

func TestWriteSyntheticRimskyYAML_MergedExecutors(t *testing.T) {
	tmp := t.TempDir()
	runDir, err := EnsureRunDir(tmp, "2026-06-13T10-01-00Z", "demo")
	if err != nil {
		t.Fatalf("EnsureRunDir: %v", err)
	}
	merged := map[string]ManifestExecutorEntry{
		"foo": {Transport: "grpc", Endpoint: "127.0.0.1:9091"},
	}
	if err := WriteSyntheticRimskyYAML(runDir, &Manifest{Project: "demo"}, merged, nil, 0); err != nil {
		t.Fatalf("WriteSyntheticRimskyYAML: %v", err)
	}
	cfg, err := config.LoadRimskyConfigYAML(filepath.Join(runDir, "rimsky.yml"))
	if err != nil {
		t.Fatalf("LoadRimskyConfigYAML: %v", err)
	}
	entry, ok := cfg.Executors.Executors["foo"]
	if !ok {
		t.Fatalf("executors[foo] missing from round-tripped config")
	}
	if entry.Transport != "grpc" {
		t.Fatalf("executors[foo].transport: got %q, want %q", entry.Transport, "grpc")
	}
	if entry.Endpoint != "127.0.0.1:9091" {
		t.Fatalf("executors[foo].endpoint: got %q, want %q", entry.Endpoint, "127.0.0.1:9091")
	}
}

func TestWriteSyntheticRimskyYAML_ManifestExecutorsFoldedAsBase(t *testing.T) {
	tmp := t.TempDir()
	runDir, err := EnsureRunDir(tmp, "2026-06-13T10-04-00Z", "demo")
	if err != nil {
		t.Fatalf("EnsureRunDir: %v", err)
	}
	m := &Manifest{
		Project: "demo",
		Executors: map[string]ManifestExecutorEntry{
			"manifest-foo": {Transport: "grpc", Endpoint: "127.0.0.1:9101"},
		},
	}
	if err := WriteSyntheticRimskyYAML(runDir, m, nil, nil, 0); err != nil {
		t.Fatalf("WriteSyntheticRimskyYAML: %v", err)
	}
	cfg, err := config.LoadRimskyConfigYAML(filepath.Join(runDir, "rimsky.yml"))
	if err != nil {
		t.Fatalf("LoadRimskyConfigYAML: %v", err)
	}
	entry, ok := cfg.Executors.Executors["manifest-foo"]
	if !ok {
		t.Fatalf("manifest executor not folded into synthetic config")
	}
	if entry.Endpoint != "127.0.0.1:9101" {
		t.Fatalf("executors[manifest-foo].endpoint: got %q, want %q", entry.Endpoint, "127.0.0.1:9101")
	}
}

func TestWriteSyntheticRimskyYAML_SpawnOverlayOverridesManifest(t *testing.T) {
	tmp := t.TempDir()
	runDir, err := EnsureRunDir(tmp, "2026-06-13T10-05-00Z", "demo")
	if err != nil {
		t.Fatalf("EnsureRunDir: %v", err)
	}
	m := &Manifest{
		Project: "demo",
		Executors: map[string]ManifestExecutorEntry{
			"foo": {Transport: "grpc", Endpoint: "127.0.0.1:1111"},
		},
	}
	overlay := map[string]ManifestExecutorEntry{
		"foo": {Transport: "grpc", Endpoint: "127.0.0.1:2222"},
	}
	if err := WriteSyntheticRimskyYAML(runDir, m, overlay, nil, 0); err != nil {
		t.Fatalf("WriteSyntheticRimskyYAML: %v", err)
	}
	cfg, err := config.LoadRimskyConfigYAML(filepath.Join(runDir, "rimsky.yml"))
	if err != nil {
		t.Fatalf("LoadRimskyConfigYAML: %v", err)
	}
	entry, ok := cfg.Executors.Executors["foo"]
	if !ok {
		t.Fatalf("executors[foo] missing from round-tripped config")
	}
	if entry.Endpoint != "127.0.0.1:2222" {
		t.Fatalf("spawn overlay did not override manifest: endpoint=%q want=%q", entry.Endpoint, "127.0.0.1:2222")
	}
}

func TestWriteSyntheticRimskyYAML_ExecutorProtocolsRoundTrip(t *testing.T) {
	tmp := t.TempDir()
	runDir, err := EnsureRunDir(tmp, "2026-06-13T10-06-00Z", "demo")
	if err != nil {
		t.Fatalf("EnsureRunDir: %v", err)
	}
	m := &Manifest{
		Project: "demo",
		Executors: map[string]ManifestExecutorEntry{
			"multi": {
				Transport: "grpc",
				Endpoint:  "127.0.0.1:9201",
				Protocols: []string{"executor", "lifecycle_subscriber"},
			},
		},
	}
	if err := WriteSyntheticRimskyYAML(runDir, m, nil, nil, 0); err != nil {
		t.Fatalf("WriteSyntheticRimskyYAML: %v", err)
	}
	cfg, err := config.LoadRimskyConfigYAML(filepath.Join(runDir, "rimsky.yml"))
	if err != nil {
		t.Fatalf("LoadRimskyConfigYAML: %v", err)
	}
	entry, ok := cfg.Executors.Executors["multi"]
	if !ok {
		t.Fatalf("executors[multi] missing from round-tripped config")
	}
	if !entry.HasProtocol("executor") || !entry.HasProtocol("lifecycle_subscriber") {
		t.Fatalf("executors[multi].protocols round-trip lost values: got %v", entry.Protocols)
	}
}

func TestWriteSyntheticRimskyYAML_ClaimProducersFromManifest(t *testing.T) {
	tmp := t.TempDir()
	runDir, err := EnsureRunDir(tmp, "2026-06-13T10-02-00Z", "demo")
	if err != nil {
		t.Fatalf("EnsureRunDir: %v", err)
	}
	m := &Manifest{
		Project: "demo",
		ClaimProducers: map[string]ManifestClaimProducerEntry{
			"items": {
				Endpoint:              "127.0.0.1:9100",
				WriteSemanticsAllowed: []string{"sync"},
			},
		},
	}
	if err := WriteSyntheticRimskyYAML(runDir, m, nil, nil, 0); err != nil {
		t.Fatalf("WriteSyntheticRimskyYAML: %v", err)
	}
	cfg, err := config.LoadRimskyConfigYAML(filepath.Join(runDir, "rimsky.yml"))
	if err != nil {
		t.Fatalf("LoadRimskyConfigYAML: %v", err)
	}
	entry, ok := cfg.ClaimProducers.ClaimProducers["items"]
	if !ok {
		t.Fatalf("claim_producers[items] missing from round-tripped config")
	}
	if entry.Endpoint != "127.0.0.1:9100" {
		t.Fatalf("claim_producers[items].endpoint: got %q, want %q", entry.Endpoint, "127.0.0.1:9100")
	}
}

// @decision: services-source
func TestLoadSiblingBlocks_PublishersAndNamedLocksFromSibling(t *testing.T) {
	tmp := t.TempDir()
	runDir, err := EnsureRunDir(tmp, "2026-06-13T10-08-00Z", "demo")
	if err != nil {
		t.Fatalf("EnsureRunDir: %v", err)
	}
	siblingPath := filepath.Join(tmp, "rimsky.yml")
	sibling := []byte(`persistence:
  driver: sqlite
  sqlite:
    path: /tmp/sibling.db
publishers:
  ticker:
    endpoint: 127.0.0.1:9301
    tls: off
named_locks:
  resource-a:
    limit: 3
`)
	if err := os.WriteFile(siblingPath, sibling, 0o644); err != nil {
		t.Fatalf("write sibling rimsky.yml: %v", err)
	}
	siblings, err := LoadSiblingBlocks(siblingPath)
	if err != nil {
		t.Fatalf("LoadSiblingBlocks: %v", err)
	}
	if siblings == nil {
		t.Fatal("LoadSiblingBlocks returned nil for a sibling that declares publishers+named_locks")
	}
	if _, ok := siblings.Publishers["ticker"]; !ok {
		t.Fatalf("publishers[ticker] missing from loaded sibling: %#v", siblings.Publishers)
	}
	if lock, ok := siblings.NamedLocks["resource-a"]; !ok || lock.Limit != 3 {
		t.Fatalf("named_locks[resource-a] missing or wrong limit: %#v", siblings.NamedLocks)
	}

	if err := WriteSyntheticRimskyYAML(runDir, &Manifest{Project: "demo"}, nil, siblings, 0); err != nil {
		t.Fatalf("WriteSyntheticRimskyYAML: %v", err)
	}
	cfg, err := config.LoadRimskyConfigYAML(filepath.Join(runDir, "rimsky.yml"))
	if err != nil {
		t.Fatalf("LoadRimskyConfigYAML: %v", err)
	}
	pub, ok := cfg.Publishers.Publishers["ticker"]
	if !ok {
		t.Fatalf("publishers[ticker] not folded into synthetic rimsky.yml: %#v", cfg.Publishers.Publishers)
	}
	if pub.Endpoint != "127.0.0.1:9301" {
		t.Fatalf("publishers[ticker].endpoint: got %q, want 127.0.0.1:9301", pub.Endpoint)
	}
	if pub.TLS != "off" {
		t.Fatalf("publishers[ticker].tls: got %q, want \"off\" (round-tripped from the sibling's `tls: off`)", pub.TLS)
	}
	nl, ok := cfg.NamedLocks.Locks["resource-a"]
	if !ok {
		t.Fatalf("named_locks[resource-a] not folded into synthetic rimsky.yml: %#v", cfg.NamedLocks)
	}
	if nl.Limit != 3 {
		t.Fatalf("named_locks[resource-a].limit: got %d, want 3", nl.Limit)
	}
}

func TestLoadSiblingBlocks_EmptyPathNoOp(t *testing.T) {
	siblings, err := LoadSiblingBlocks("")
	if err != nil {
		t.Fatalf("LoadSiblingBlocks(\"\"): %v", err)
	}
	if siblings != nil {
		t.Fatalf("LoadSiblingBlocks(\"\") = %#v, want nil", siblings)
	}
}

// @concept: rimsky-yml
// @decision: launch-config-injection
func TestWriteSyntheticRimskyYAML_CarriesTheSupervisorSectionWithTheCallbackPort(t *testing.T) {
	tmp := t.TempDir()
	runDir, err := EnsureRunDir(tmp, "2026-06-13T10-07-00Z", "demo")
	if err != nil {
		t.Fatalf("EnsureRunDir: %v", err)
	}
	const wantPort = 0
	if err := WriteSyntheticRimskyYAML(runDir, &Manifest{Project: "demo"}, nil, nil, wantPort); err != nil {
		t.Fatalf("WriteSyntheticRimskyYAML: %v", err)
	}
	body, err := os.ReadFile(filepath.Join(runDir, "rimsky.yml"))
	if err != nil {
		t.Fatalf("read written rimsky.yml: %v", err)
	}
	if strings.Contains(string(body), "supervisor.yml") {
		t.Fatalf("the run directory must carry one configuration file\n%s", string(body))
	}
	if _, err := os.Stat(filepath.Join(runDir, "supervisor.yml")); !os.IsNotExist(err) {
		t.Fatalf("the run wrote a second configuration file at supervisor.yml")
	}
	var probe struct {
		Supervisor struct {
			Concurrency         int `yaml:"concurrency"`
			ClaimPollIntervalMs int `yaml:"claim_poll_interval_ms"`
			Callback            struct {
				Host          string `yaml:"host"`
				Port          *int   `yaml:"port"`
				AdvertiseHost string `yaml:"advertise_host"`
			} `yaml:"callback"`
		} `yaml:"supervisor"`
	}
	if err := yaml.Unmarshal(body, &probe); err != nil {
		t.Fatalf("yaml round-trip: %v\n%s", err, string(body))
	}
	if probe.Supervisor.Callback.Port == nil {
		t.Fatalf("supervisor.callback.port is absent, so the supervisor takes the core-block default instead of an operating-system-assigned port\n%s", string(body))
	}
	if *probe.Supervisor.Callback.Port != wantPort {
		t.Fatalf("supervisor.callback.port: got %d, want %d\n%s", *probe.Supervisor.Callback.Port, wantPort, string(body))
	}
	if probe.Supervisor.Callback.Host != syntheticSupervisorCallbackHost {
		t.Fatalf("supervisor.callback.host: got %q, want %q\n%s", probe.Supervisor.Callback.Host, syntheticSupervisorCallbackHost, string(body))
	}
	if probe.Supervisor.Callback.AdvertiseHost != syntheticSupervisorCallbackAdvertiseHost {
		t.Fatalf("supervisor.callback.advertise_host: got %q, want %q\n%s", probe.Supervisor.Callback.AdvertiseHost, syntheticSupervisorCallbackAdvertiseHost, string(body))
	}
	if probe.Supervisor.Concurrency != syntheticSupervisorConcurrency {
		t.Fatalf("supervisor.concurrency: got %d, want %d\n%s", probe.Supervisor.Concurrency, syntheticSupervisorConcurrency, string(body))
	}
	if probe.Supervisor.ClaimPollIntervalMs != syntheticSupervisorClaimPollIntervalMs {
		t.Fatalf("supervisor.claim_poll_interval_ms: got %d, want %d\n%s", probe.Supervisor.ClaimPollIntervalMs, syntheticSupervisorClaimPollIntervalMs, string(body))
	}
}

// @decision: launch-config-injection
func TestSyntheticSupervisorDefaultsMatchTheBakedAllInOneFile(t *testing.T) {
	baked := filepath.Join(repoRoot(t), "dockerfiles", "all-in-one.rimsky.yml")
	cfg, err := config.LoadRimskyConfigYAML(baked)
	if err != nil {
		t.Fatalf("load baked all-in-one config %q: %v", baked, err)
	}
	if cfg.Supervisor.Concurrency != syntheticSupervisorConcurrency {
		t.Fatalf("supervisor.concurrency: baked %d, CLI %d — a local run and the all-in-one image would tune the supervisor differently",
			cfg.Supervisor.Concurrency, syntheticSupervisorConcurrency)
	}
	if cfg.Supervisor.ClaimPollIntervalMs != syntheticSupervisorClaimPollIntervalMs {
		t.Fatalf("supervisor.claim_poll_interval_ms: baked %d, CLI %d",
			cfg.Supervisor.ClaimPollIntervalMs, syntheticSupervisorClaimPollIntervalMs)
	}
	if cfg.Supervisor.Callback.Host != syntheticSupervisorCallbackHost {
		t.Fatalf("supervisor.callback.host: baked %q, CLI %q",
			cfg.Supervisor.Callback.Host, syntheticSupervisorCallbackHost)
	}
	if cfg.Supervisor.Callback.AdvertiseHost != syntheticSupervisorCallbackAdvertiseHost {
		t.Fatalf("supervisor.callback.advertise_host: baked %q, CLI %q",
			cfg.Supervisor.Callback.AdvertiseHost, syntheticSupervisorCallbackAdvertiseHost)
	}
}
