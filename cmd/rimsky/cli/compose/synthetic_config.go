// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

// @decision: launch-config-injection
// @decision: persistence-driver
// @decision: blob-backend
package compose

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"gopkg.in/yaml.v3"

	"github.com/rimsky-ai/rimsky-core/lib/control/config"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/locks"
)

type syntheticRimskyYAML struct {
	Persistence    syntheticPersistence                  `yaml:"persistence"`
	Supervisor     syntheticSupervisor                   `yaml:"supervisor"`
	Executors      map[string]ManifestExecutorEntry      `yaml:"executors,omitempty"`
	ClaimProducers map[string]ManifestClaimProducerEntry `yaml:"claim_producers,omitempty"`
	Publishers     map[string]syntheticPublisherEntry    `yaml:"publishers,omitempty"`
	NamedLocks     map[string]locks.NamedLockConfig      `yaml:"named_locks,omitempty"`
}

// @concept: rimsky-yml
// @decision: launch-config-injection
type syntheticSupervisor struct {
	Concurrency         int                         `yaml:"concurrency"`
	ClaimPollIntervalMs int                         `yaml:"claim_poll_interval_ms"`
	Callback            syntheticSupervisorCallback `yaml:"callback"`
}

// @decision: network-binding
type syntheticSupervisorCallback struct {
	Host          string `yaml:"host"`
	Port          *int   `yaml:"port"`
	AdvertiseHost string `yaml:"advertise_host"`
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

func WriteSyntheticRimskyYAML(
	runDir string, m *Manifest, spawnOverlay map[string]ManifestExecutorEntry,
	siblings *SiblingBlocks, callbackPort int,
) error {
	out := syntheticRimskyYAML{
		Supervisor: syntheticSupervisor{
			Concurrency:         syntheticSupervisorConcurrency,
			ClaimPollIntervalMs: syntheticSupervisorClaimPollIntervalMs,
			Callback: syntheticSupervisorCallback{
				Host:          syntheticSupervisorCallbackHost,
				Port:          &callbackPort,
				AdvertiseHost: syntheticSupervisorCallbackAdvertiseHost,
			},
		},
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
	cfg, err := config.LoadRimskyConfigYAML(path)
	if err != nil {
		return nil, fmt.Errorf("load sibling rimsky.yml: %w", err)
	}
	if len(cfg.Publishers.Publishers) == 0 && len(cfg.NamedLocks.Locks) == 0 {
		return nil, nil
	}
	pubs := make(map[string]syntheticPublisherEntry, len(cfg.Publishers.Publishers))
	for name, entry := range cfg.Publishers.Publishers {
		pubs[name] = syntheticPublisherEntry{
			Endpoint:              entry.Endpoint,
			TLS:                   entry.TLS,
			Protocols:             entry.Protocols,
			ObservabilityEndpoint: entry.ObservabilityEndpoint,
		}
	}
	return &SiblingBlocks{
		Publishers: pubs,
		NamedLocks: cfg.NamedLocks.Locks,
	}, nil
}

// @decision: launch-config-injection
// @decision: network-binding
const (
	syntheticSupervisorConcurrency           = 8
	syntheticSupervisorClaimPollIntervalMs   = 200
	syntheticSupervisorCallbackHost          = "0.0.0.0"
	syntheticSupervisorCallbackAdvertiseHost = "127.0.0.1"
)
