// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// @decision: launch-config-injection, persistence-driver, blob-backend,
package compose

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"gopkg.in/yaml.v3"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/locks"
)

type syntheticRimskyYAML struct {
	Persistence    syntheticPersistence                  `yaml:"persistence"`
	Executors      map[string]ManifestExecutorEntry      `yaml:"executors,omitempty"`
	ClaimProducers map[string]ManifestClaimProducerEntry `yaml:"claim_producers,omitempty"`
	Publishers     map[string]syntheticPublisherEntry    `yaml:"publishers,omitempty"`
	NamedLocks     map[string]locks.NamedLockConfig      `yaml:"named_locks,omitempty"`
}

type syntheticPublisherEntry struct {
	Endpoint              string   `yaml:"endpoint"`
	TLS                   string   `yaml:"tls,omitempty"`
	Protocols             []string `yaml:"protocols,omitempty"`
	ObservabilityEndpoint string   `yaml:"observability_endpoint,omitempty"`
}

type syntheticPersistence struct {
	Driver string                     `yaml:"driver"`
	SQLite syntheticPersistenceSQLite `yaml:"sqlite"`
	Blob   syntheticPersistenceBlob   `yaml:"blob"`
}

type syntheticPersistenceSQLite struct {
	Path string `yaml:"path"`
}

type syntheticPersistenceBlob struct {
	Backend             string                  `yaml:"backend"`
	Filesystem          syntheticBlobFilesystem `yaml:"filesystem"`
	SpillThresholdBytes int                     `yaml:"spill_threshold_bytes,omitempty"`
}

type syntheticBlobFilesystem struct {
	Root string `yaml:"root"`
}

type SiblingBlocks struct {
	Publishers map[string]syntheticPublisherEntry
	NamedLocks map[string]locks.NamedLockConfig
}

func WriteSyntheticRimskyYAML(runDir string, m *Manifest, spawnOverlay map[string]ManifestExecutorEntry, siblings *SiblingBlocks) error {
	out := syntheticRimskyYAML{
		Persistence: syntheticPersistence{
			Driver: "sqlite",
			SQLite: syntheticPersistenceSQLite{
				Path: filepath.Join(runDir, "state.db"),
			},
			Blob: syntheticPersistenceBlob{
				Backend: "filesystem",
				Filesystem: syntheticBlobFilesystem{
					Root: filepath.Join(runDir, "blobs"),
				},
			},
		},
	}
	executors := map[string]ManifestExecutorEntry{}
	if m != nil {
		for name, entry := range m.Executors {
			executors[name] = entry
		}
	}
	for name, entry := range spawnOverlay {
		executors[name] = entry
	}
	mergedNames := make([]string, 0, len(executors))
	for name := range executors {
		mergedNames = append(mergedNames, name)
	}
	sort.Strings(mergedNames)
	var mergedErrs []error
	for _, name := range mergedNames {
		if !serviceNameRe.MatchString(name) {
			mergedErrs = append(mergedErrs, fmt.Errorf("executors[%q]: service name does not match %s", name, serviceNameRe.String()))
		}
		mergedErrs = append(mergedErrs, validateExecutorEntry(name, executors[name])...)
	}
	if len(mergedErrs) > 0 {
		return fmt.Errorf("synthetic rimsky.yml: merged executors invalid: %w", errors.Join(mergedErrs...))
	}
	if len(executors) > 0 {
		out.Executors = executors
	}
	if m != nil && len(m.ClaimProducers) > 0 {
		out.ClaimProducers = m.ClaimProducers
	}
	if siblings != nil {
		if len(siblings.Publishers) > 0 {
			out.Publishers = siblings.Publishers
		}
		if len(siblings.NamedLocks) > 0 {
			out.NamedLocks = siblings.NamedLocks
		}
	}
	body, err := yaml.Marshal(&out)
	if err != nil {
		return fmt.Errorf("marshal synthetic rimsky.yml: %w", err)
	}
	path := filepath.Join(runDir, "rimsky.yml")
	if err := os.WriteFile(path, body, 0o644); err != nil {
		return fmt.Errorf("write synthetic rimsky.yml to %q: %w", path, err)
	}
	return nil
}

func LoadSiblingBlocks(path string) (*SiblingBlocks, error) {
	if path == "" {
		return nil, nil
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read sibling rimsky.yml %q: %w", path, err)
	}
	expanded := os.ExpandEnv(string(raw))
	var wrapper struct {
		Publishers map[string]syntheticPublisherEntry `yaml:"publishers"`
		NamedLocks map[string]locks.NamedLockConfig   `yaml:"named_locks"`
	}
	if err := yaml.Unmarshal([]byte(expanded), &wrapper); err != nil {
		return nil, fmt.Errorf("parse sibling rimsky.yml %q: %w", path, err)
	}
	if len(wrapper.Publishers) == 0 && len(wrapper.NamedLocks) == 0 {
		return nil, nil
	}
	return &SiblingBlocks{
		Publishers: wrapper.Publishers,
		NamedLocks: wrapper.NamedLocks,
	}, nil
}

const syntheticSupervisorYAML = `# Default supervisor tuning baked into the rimsky-all-in-one image and copied
# to /etc/rimsky/supervisor-config.yml at build time. Loaded by the supervisor
# process via RIMSKY_SUPERVISOR_CONFIG; deployment-shape config
# (claim_producers, named_locks, executors) lives separately in rimsky.yml
# under RIMSKY_CONFIG.
#
# Single-container defaults: the async-callback listener binds 0.0.0.0:9100 and
# advertises 127.0.0.1, because every rimsky role (scheduler, supervisor,
# control-api) runs in the single all-in-one process inside this container. Override advertise_host
# via RIMSKY_SUPERVISOR_CALLBACK_ADVERTISE_HOST when executors run outside the
# container (or mount your own file at /etc/rimsky/supervisor-config.yml).

concurrency: 8
heartbeat_interval_ms: 5000
claim_poll_interval_ms: 1000
callback:
  host: 0.0.0.0
  port: 9100
  advertise_host: 127.0.0.1
`

func WriteSyntheticSupervisorYAML(runDir string) error {
	path := filepath.Join(runDir, "supervisor.yml")
	if err := os.WriteFile(path, []byte(syntheticSupervisorYAML), 0o644); err != nil {
		return fmt.Errorf("write synthetic supervisor.yml to %q: %w", path, err)
	}
	return nil
}

type supervisorYAMLProbe struct {
	SupervisorID        string                      `yaml:"supervisor_id,omitempty"`
	Concurrency         int                         `yaml:"concurrency,omitempty"`
	LivenessIntervalMs  int                         `yaml:"liveness_interval_ms,omitempty"`
	ClaimPollIntervalMs int                         `yaml:"claim_poll_interval_ms,omitempty"`
	Callback            supervisorYAMLProbeCallback `yaml:"callback"`
}

type supervisorYAMLProbeCallback struct {
	Host          string `yaml:"host"`
	Port          int    `yaml:"port"`
	AdvertiseHost string `yaml:"advertise_host,omitempty"`
	AdvertisePort int    `yaml:"advertise_port,omitempty"`
}

// @decision: launch-config-injection
// @decision: network-binding
func WriteSyntheticSupervisorYAMLWithCallbackPort(runDir string, callbackPort int) error {
	var probe supervisorYAMLProbe
	if err := yaml.Unmarshal([]byte(syntheticSupervisorYAML), &probe); err != nil {
		return fmt.Errorf("synthetic supervisor.yml: parse baked default: %w", err)
	}
	probe.Callback.Port = callbackPort
	body, err := yaml.Marshal(&probe)
	if err != nil {
		return fmt.Errorf("synthetic supervisor.yml: marshal spliced config: %w", err)
	}
	path := filepath.Join(runDir, "supervisor.yml")
	if err := os.WriteFile(path, body, 0o644); err != nil {
		return fmt.Errorf("write synthetic supervisor.yml to %q: %w", path, err)
	}
	return nil
}
