// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// synthetic_config.go — emits the two YAML files `rimsky compose run`
// drops in each per-run artifact directory so the in-process role
// runners can load configuration off disk via the existing
// env:RIMSKY_CONFIG / env:RIMSKY_SUPERVISOR_CONFIG seam. See
// @decision: launch-config-injection, persistence-driver, blob-backend,
// services-source.
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

// syntheticRimskyYAML mirrors the top-level rimsky.yml shape that
// LoadRimskyConfigYAML consumes. The persistence + executors +
// claim_producers blocks are composed by the verb itself; the
// publishers and named_locks blocks pass through verbatim from a
// sibling rimsky.yml next to the compose manifest (compose schema
// does not carry these blocks — @decision: services-source).
type syntheticRimskyYAML struct {
	Persistence    syntheticPersistence                  `yaml:"persistence"`
	Executors      map[string]ManifestExecutorEntry      `yaml:"executors,omitempty"`
	ClaimProducers map[string]ManifestClaimProducerEntry `yaml:"claim_producers,omitempty"`
	Publishers     map[string]syntheticPublisherEntry    `yaml:"publishers,omitempty"`
	NamedLocks     map[string]locks.NamedLockConfig      `yaml:"named_locks,omitempty"`
}

// syntheticPublisherEntry is the wire shape of one publishers-block
// entry in the synthetic rimsky.yml. Fields mirror the rimsky.yml
// loader's yamlPublisherEntry so the round-trip is loss-free.
type syntheticPublisherEntry struct {
	Endpoint              string   `yaml:"endpoint"`
	TLS                   string   `yaml:"tls,omitempty"`
	Protocols             []string `yaml:"protocols,omitempty"`
	ObservabilityEndpoint string   `yaml:"observability_endpoint,omitempty"`
}

// syntheticPersistence carries the persistence block: a sqlite-rooted
// driver per @decision: persistence-driver, plus a filesystem-rooted
// blob backend per @decision: blob-backend.
type syntheticPersistence struct {
	Driver string                     `yaml:"driver"`
	SQLite syntheticPersistenceSQLite `yaml:"sqlite"`
	Blob   syntheticPersistenceBlob   `yaml:"blob"`
}

// syntheticPersistenceSQLite is the `persistence.sqlite:` sub-block.
type syntheticPersistenceSQLite struct {
	Path string `yaml:"path"`
}

// syntheticPersistenceBlob is the `persistence.blob:` sub-block.
type syntheticPersistenceBlob struct {
	Backend             string                  `yaml:"backend"`
	Filesystem          syntheticBlobFilesystem `yaml:"filesystem"`
	SpillThresholdBytes int                     `yaml:"spill_threshold_bytes,omitempty"`
}

// syntheticBlobFilesystem is the `persistence.blob.filesystem:`
// sub-block.
type syntheticBlobFilesystem struct {
	Root string `yaml:"root"`
}

// SiblingBlocks carries the publishers + named_locks blocks the verb
// reads from a sibling rimsky.yml (when one is present next to the
// compose manifest) and folds into the synthetic rimsky.yml. The
// compose manifest schema does not carry these two blocks
// (@decision: services-source), so this is the only path that
// surfaces them to the in-process role runners.
type SiblingBlocks struct {
	Publishers map[string]syntheticPublisherEntry
	NamedLocks map[string]locks.NamedLockConfig
}

// WriteSyntheticRimskyYAML composes the synthetic `rimsky.yml` for a
// one-shot run and writes it under runDir. The persistence block is
// always sqlite-with-filesystem-blobs, rooted at runDir.
//
// The executors block is composed in two layers: the manifest's
// `executors:` block is the base layer (read directly from m here so
// the caller cannot forget to seed it), and spawnOverlay — the caller's
// resolved `--service <name>=<path>` endpoints — overlays on top
// (per-name overwrite). The priority order (manifest base < --service
// overlay) matches @decision: services-source.
//
// The claim_producers block passes through from the manifest verbatim.
// Per @decision: services-source, --service can only override executors,
// not claim_producers.
//
// The siblings block (publishers + named_locks) is folded from a
// sibling rimsky.yml next to the compose manifest — the compose
// schema doesn't carry these blocks, so a manifest that needs a
// publisher or a named lock leans on the sibling file. The verb
// resolves the sibling via SiblingRimskyYMLPath + LoadSiblingBlocks
// and threads the result here; a nil siblings argument drops both
// blocks (the synthetic config emits no publishers/named_locks keys
// at all, which is the normal case).
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
	// @constraint: post-merge belt-and-braces validation. The manifest
	// validator already checked the manifest's own executors block, but
	// the merged result is what hits disk. A programming error in the
	// spawn-overlay constructor (e.g., an entry with an empty transport)
	// would otherwise surface from the role-runner's config loader at
	// boot time as a confusing deep-stack error. Sort the merged names
	// so the error list is deterministic across runs.
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

// LoadSiblingBlocks reads a sibling rimsky.yml's publishers + named_locks
// blocks for fold-through into the synthetic config. An empty path
// returns (nil, nil) — the verb skips the load when no sibling exists.
//
// The loader is intentionally narrow: it parses only the two blocks
// the compose schema doesn't carry, ignoring every other key the
// rimsky.yml file might contain (persistence, executors, claim_producers,
// retention — anything the manifest or the verb itself owns). This
// keeps the source-priority order @decision: services-source names:
// sibling rimsky.yml provides ONLY the blocks compose can't, and a
// publisher/named-lock entry only exists when this path surfaces it.
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

// syntheticSupervisorYAML is the supervisor-tuning file the one-shot
// verb writes to <run>/supervisor.yml. The contents are inherited
// verbatim — byte-for-byte — from the all-in-one baked file shipped
// with the rimsky-all-in-one image. The
// TestWriteSyntheticSupervisorYAML_MatchesBakedDefault test
// byte-compares against the source file and fails on any drift.
//
// @source: dockerfiles/all-in-one.supervisor-config.yml — the
// all-in-one image's baked supervisor-config file is the canonical
// default; this const is a mirror and the byte-compare test
// (TestWriteSyntheticSupervisorYAML_MatchesBakedDefault) pins drift.
// @constraint: any change to the all-in-one baked supervisor-config
// file must mirror here, and vice versa; the byte-compare test
// catches divergence.
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

// WriteSyntheticSupervisorYAML writes the supervisor-tuning file the
// in-process supervisor runner consumes via env:RIMSKY_SUPERVISOR_CONFIG.
// The contents are verbatim with the all-in-one baked default per
// spec §6 — see @decision: launch-config-injection. The byte-equality
// test (TestWriteSyntheticSupervisorYAML_MatchesBakedDefault) pins
// drift between this constant and the all-in-one image's baked file.
//
// @constraint: the verb's production callsite (run.go) calls
// WriteSyntheticSupervisorYAMLWithCallbackPort with a kernel-picked
// port instead — the verb cannot rely on the fixed port 9100 because
// a parallel one-shot run, or any other rimsky process on the host,
// would collide on bind. This default-port writer remains for the
// drift test and for any caller that explicitly wants the baked
// callback port.
func WriteSyntheticSupervisorYAML(runDir string) error {
	path := filepath.Join(runDir, "supervisor.yml")
	if err := os.WriteFile(path, []byte(syntheticSupervisorYAML), 0o644); err != nil {
		return fmt.Errorf("write synthetic supervisor.yml to %q: %w", path, err)
	}
	return nil
}

// supervisorYAMLProbe mirrors the supervisor-tuning YAML's top-level
// shape — the same fields the launch package's supervisorYAMLConfig
// loads. Held here as a structural anchor for the YAML-aware splice
// in WriteSyntheticSupervisorYAMLWithCallbackPort: parsing then
// re-marshaling via this type is robust to whitespace, comment, and
// field-order drift in the baked default, where the previous
// strings.Replace-based splice was not. The field set tracks the
// supervisor loader's surface (see lib/control/launch/supervisor.go::supervisorYAMLConfig)
// so a re-marshal here round-trips through the loader to the same
// SupervisorConfig.
type supervisorYAMLProbe struct {
	SupervisorID        string                      `yaml:"supervisor_id,omitempty"`
	Concurrency         int                         `yaml:"concurrency,omitempty"`
	HeartbeatIntervalMs int                         `yaml:"heartbeat_interval_ms,omitempty"`
	ClaimPollIntervalMs int                         `yaml:"claim_poll_interval_ms,omitempty"`
	Callback            supervisorYAMLProbeCallback `yaml:"callback"`
}

type supervisorYAMLProbeCallback struct {
	Host          string `yaml:"host"`
	Port          int    `yaml:"port"`
	AdvertiseHost string `yaml:"advertise_host,omitempty"`
	AdvertisePort int    `yaml:"advertise_port,omitempty"`
}

// WriteSyntheticSupervisorYAMLWithCallbackPort writes a per-run
// supervisor-tuning file that takes the all-in-one baked default but
// substitutes the callback bind port with the supplied value. A
// callback port of 0 lets the kernel pick a free port at supervisor
// bind time, which is what the `compose run` verb needs: every
// concurrent one-shot run on a host (and any deployed rimsky already
// holding 9100) would otherwise collide on `listen tcp 0.0.0.0:9100`
// and fail StartRoleStack.
//
// The splice is YAML-aware: the baked default is parsed into a typed
// struct, callback.port is set to the supplied value, and the result
// is re-marshaled. The output is byte-different from the baked
// default (re-serialized YAML drops the leading comment block and
// may reorder fields), but it round-trips through the supervisor
// YAML loader to the same SupervisorConfig — which is the property
// the role runners actually rely on. Robust to whitespace, comment-
// text, and field-order drift in the baked default that the previous
// strings.Replace-based splice would silently degrade against.
//
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
