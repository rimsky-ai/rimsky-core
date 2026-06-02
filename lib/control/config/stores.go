// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// ClaimProducers + executors config + remote-dialing helpers for the
// rimsky processes. Per spec docs/specs/2026-05-04-service-protocol-
// contract.md and the layer-crystallization plan (Phase 4 / Task 28):
// the unified rimsky.yml shape (claim_producers + named_locks + executors
// loaded together by all four rimsky binaries — control-api, supervisor,
// scheduler, migrate — from $RIMSKY_CONFIG).
//
// Config shape (post-2026-05-12 nomenclature resolution):
//   - top-level block is `claim_producers:` (the pre-Phase-4 alias
//     `stores:` was rejected with a precise error in cross-layer #1 / B.6
//     and is no longer accepted).
//   - each producer / executor entry gains optional `protocols: [...]`
//     declaring which protocols it speaks. Default for claim_producers
//     is [claim_producer]; default for executors is [executor].
//   - producer entries declare required `write_semantics_allowed: [...]`
//     listing the operator-permissible value set (must be a non-empty
//     subset of the producer-advertised allowed set). The legacy
//     singular `write_semantics:` shortcut AND the intermediate
//     `write_semantics_envelope:` alias are both rejected at startup
//     with precise errors directing the operator to
//     `write_semantics_allowed:` — see the rejection paths immediately
//     below the wrapper unmarshal in `LoadRimskyConfig`.
package config

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"sort"
	"time"

	"github.com/google/uuid"
	"gopkg.in/yaml.v3"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/locks"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/protocols/claimproducer"
	"github.com/rimsky-ai/rimsky-core/lib/runtime"
	peer "github.com/rimsky-ai/rimsky-core/lib/runtime/peer"
)

// Documented retention defaults (mirroring the doc on
// runtime.RetentionConfig). Applied by LoadRimskyConfigYAML when the
// `retention:` block (or an individual key) is absent, so retention is on
// by default — the scheduler tick reaps stale lineage / run-tree /
// claim-handle / message-idempotency rows out of the box.
const (
	defaultRetentionRecentFramesKept             = 100
	defaultRetentionLineageTrailing              = 30 * 24 * time.Hour
	defaultRetentionClaimHandlesTrailing         = 30 * 24 * time.Hour
	defaultRetentionMessageIdempotenciesTrailing = 24 * time.Hour
)

// yamlRetention is the `retention:` block shape. Pointer fields so the
// loader can tell an absent key (→ apply the documented default) from an
// explicit zero (→ disable that sweep). Keys mirror
// runtime.RetentionConfig.
type yamlRetention struct {
	RecentFramesKept             *int           `yaml:"recent_frames_kept"`
	LineageTrailing              *time.Duration `yaml:"lineage_trailing"`
	ClaimHandlesTrailing         *time.Duration `yaml:"claim_handles_trailing"`
	MessageIdempotenciesTrailing *time.Duration `yaml:"message_idempotencies_trailing"`
}

// capabilitiesHandshakeTimeout bounds the per-producer Capabilities() RPC
// at startup. Without it a producer-service that accepts the connection
// but never replies blocks the rimsky process forever.
const capabilitiesHandshakeTimeout = 30 * time.Second

// Protocol enumerates the wire protocols a peer may speak. Validated at
// parse time; any unknown value fails startup.
//
// The mix-in protocols (lifecycle_subscriber, validation, data_processing)
// live in protocols/claimproducer — the wire-vocabulary owner — and are
// referenced from there. Only the three role-anchor protocols specific to
// rimsky.yml's three top-level blocks (claim_producers, executors,
// publishers) are declared here.
//
// @concept: service (a service is an orchestrated out-of-process binary that
// declares its protocol membership; this is the protocol-role vocabulary)
const (
	ProtocolClaimProducer = "claim_producer"
	ProtocolExecutor      = "executor"
	ProtocolPublisher     = "publisher"
)

// StoreEntry is the per-claim-producer config from rimsky.yml. The
// type is named StoreEntry for backward compatibility; new code should
// read it as "claim-producer entry."
type StoreEntry struct {
	Endpoint     string
	Capabilities claimproducer.Capabilities
	// Protocols is the set of wire protocols this peer speaks. Always
	// includes "claim_producer" for entries under the claim_producers:
	// block; may also include "lifecycle_subscriber".
	Protocols []string
	// ObservabilityEndpoint is the optional observability endpoint
	// override; when empty, Endpoint is reused for the observability
	// handshake.
	ObservabilityEndpoint string
}

// HasProtocol reports whether the entry declares the given protocol.
func (e StoreEntry) HasProtocol(p string) bool {
	for _, declared := range e.Protocols {
		if declared == p {
			return true
		}
	}
	return false
}

// RemoteStoresConfig is the parsed `claim_producers:` block from
// rimsky.yml. Keys are operator-chosen producer names; values carry the
// endpoint URL, declared capabilities, and the protocol list.
type RemoteStoresConfig struct {
	Stores map[string]StoreEntry
}

// ExecutorEntry is the per-executor config from rimsky.yml's
// `executors:` block.
type ExecutorEntry struct {
	Transport string // "grpc" | "http"
	Endpoint  string // e.g. "claude-agent:9090"
	TLS       string // "off" | "optional" | "required" (matches executor.Endpoint)
	// Protocols is the set of wire protocols this peer speaks. Always
	// includes "executor" for entries under the executors: block; may
	// also include "lifecycle_subscriber".
	Protocols []string
	// ObservabilityEndpoint is the optional observability endpoint
	// override; when empty, Endpoint is reused for the observability
	// handshake.
	ObservabilityEndpoint string
}

// HasProtocol reports whether the executor entry declares the given
// protocol.
func (e ExecutorEntry) HasProtocol(p string) bool {
	for _, declared := range e.Protocols {
		if declared == p {
			return true
		}
	}
	return false
}

// ExecutorsConfig is the parsed `executors:` block from rimsky.yml.
// Keys are operator-chosen executor names referenced from template
// node defs (`executor:` field).
type ExecutorsConfig struct {
	Executors map[string]ExecutorEntry
}

// PublisherEntry is the per-publisher config from rimsky.yml's new
// top-level `publishers:` block. Publishers are out-of-process peer
// services that POST messages into rimsky; sensors are one kind of
// publisher.
type PublisherEntry struct {
	Endpoint string
	// Protocols is the set of wire protocols this peer speaks. Always
	// includes "publisher" for entries under the publishers: block.
	Protocols []string
	// ObservabilityEndpoint is the optional observability endpoint
	// override; when empty, Endpoint is reused for the observability
	// handshake.
	ObservabilityEndpoint string
}

// HasProtocol reports whether the publisher entry declares the given
// protocol.
func (e PublisherEntry) HasProtocol(p string) bool {
	for _, declared := range e.Protocols {
		if declared == p {
			return true
		}
	}
	return false
}

// RemotePublishersConfig is the parsed `publishers:` block from
// rimsky.yml. Keys are operator-chosen publisher names referenced
// from template `publishers:` blocks.
type RemotePublishersConfig struct {
	Publishers map[string]PublisherEntry
}

// Validate rejects empty transport or endpoint (syntactic only; no DNS
// / dial).
func (c ExecutorsConfig) Validate() error {
	for name, e := range c.Executors {
		if e.Transport == "" {
			return fmt.Errorf("executor %q: transport required", name)
		}
		if e.Endpoint == "" {
			return fmt.Errorf("executor %q: endpoint required", name)
		}
	}
	return nil
}

// ExecutorDeclared returns true when name appears in the executors
// block. Used by the control-api template-validation hook.
func (c ExecutorsConfig) ExecutorDeclared(name string) bool {
	_, ok := c.Executors[name]
	return ok
}

// RimskyConfig is the parsed rimsky.yml: the unified deployment-shape
// config loaded by all four rimsky binaries from $RIMSKY_CONFIG.
//
// @concept: rimsky-yml
type RimskyConfig struct {
	Persistence persistence.Config
	// Blob is the spill-config triple parsed from persistence.blob.
	// Defaults to DefaultBlobConfig (inline; no spill) when the YAML
	// key is absent. Validated by ValidateBlobConfig at startup, before
	// driver.SetBlobBackend installs it.
	Blob       persistence.BlobConfig
	Stores     RemoteStoresConfig
	NamedLocks locks.NamedLocksConfig
	Executors  ExecutorsConfig
	// Publishers is the parsed top-level `publishers:` block. Per spec
	// .ok-planner/specs/2026-05-17-sensor-messaging-unification-design.md
	// §Publisher protocol unification, publishers are peer services that
	// implement the `publisher` protocol (Subscribe / Unsubscribe /
	// ListSubscriptions / Capabilities).
	Publishers RemotePublishersConfig
	// MaxParkDuration is the per-reason max_park_duration cap map. The
	// keys are the stored ParkReason values ("await_callback",
	// "snooze" — the closed two-value enum); the values are
	// time.Duration caps. The per-row col:rimsky_node_runs.max_park_duration_seconds
	// always takes priority — these are deployment-level fall-backs.
	// Per spec
	// .ok-planner/specs/2026-05-15-data-platform-extensions-design.md
	// §Parked-state taxonomy / Per-reason `max_park_duration` config.
	// Recommended defaults: await_callback: 7d, snooze: 1h.
	MaxParkDuration map[string]time.Duration
	// Retention is the parsed `retention:` block — the trailing-window
	// retention parameters threaded into scheduler.Config.Retention so the
	// scheduler tick's lineage / run-tree / claim-handle /
	// message-idempotency sweeps fire. Documented defaults (retention on by
	// default) are applied by LoadRimskyConfigYAML when the block or an
	// individual key is absent.
	Retention runtime.RetentionConfig
	// LateBindServiceProxies maps protocol name → proxy service name.
	// e.g., {"executor": "host-agent-proxy", "claim_producer": "host-agent-proxy"}.
	// Consumed by LateBindResolver (executor) and *locks.Registry.GetWithContext
	// (claim-producer) for late-bind dispatch resolution; consumed by
	// LifecyclePeersForSpec for late-bind-proxy fan-out subscription.
	LateBindServiceProxies map[string]string
}

// LoadRimskyConfigYAML reads rimsky.yml: the unified deployment-shape
// config. A missing file is a startup error.
func LoadRimskyConfigYAML(path string) (RimskyConfig, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return RimskyConfig{}, fmt.Errorf("rimsky config file not found at %q", path)
		}
		return RimskyConfig{}, fmt.Errorf("read rimsky config %q: %w", path, err)
	}
	expanded := os.ExpandEnv(string(raw))
	type yamlClaimProducerEntry struct {
		Endpoint              string   `yaml:"endpoint"`
		Protocols             []string `yaml:"protocols"`
		WriteSemanticsAllowed []string `yaml:"write_semantics_allowed"`
		// LegacyWriteSemantics catches the retired single-value
		// `write_semantics:` shortcut so the loader can reject it with
		// a precise error (cross-layer #6 / C.1).
		LegacyWriteSemantics string `yaml:"write_semantics"`
		// LegacyWriteSemanticsEnvelope catches the retired
		// `write_semantics_envelope:` key so the loader can reject it
		// with a precise error (cross-layer #6 / C.2).
		LegacyWriteSemanticsEnvelope []string `yaml:"write_semantics_envelope"`
		ObservabilityEndpoint        string   `yaml:"observability_endpoint"`
	}
	type yamlExecutorEntry struct {
		Transport             string   `yaml:"transport"`
		Endpoint              string   `yaml:"endpoint"`
		TLS                   string   `yaml:"tls"`
		Protocols             []string `yaml:"protocols"`
		ObservabilityEndpoint string   `yaml:"observability_endpoint"`
	}
	type yamlPublisherEntry struct {
		Endpoint              string   `yaml:"endpoint"`
		Protocols             []string `yaml:"protocols"`
		ObservabilityEndpoint string   `yaml:"observability_endpoint"`
	}
	type yamlBlob struct {
		Backend             string `yaml:"backend"`
		SpillThresholdBytes int    `yaml:"spill_threshold_bytes"`
		Filesystem          *struct {
			Root string `yaml:"root"`
		} `yaml:"filesystem"`
		PgLargeObject *struct {
			Schema string `yaml:"schema"`
		} `yaml:"pg_largeobject"`
		Retention *struct {
			OrphanSweepInterval        time.Duration `yaml:"orphan_sweep_interval"`
			RetentionAfterUnreferenced time.Duration `yaml:"retention_after_unreferenced"`
		} `yaml:"retention"`
	}
	var wrapper struct {
		Persistence struct {
			Driver   string `yaml:"driver"`
			Postgres *struct {
				DSN             string        `yaml:"dsn"`
				MaxOpenConns    int           `yaml:"max_open_conns"`
				MaxIdleConns    int           `yaml:"max_idle_conns"`
				ConnMaxLifetime time.Duration `yaml:"conn_max_lifetime"`
			} `yaml:"postgres"`
			SQLite *struct {
				Path string `yaml:"path"`
			} `yaml:"sqlite"`
			Blob *yamlBlob `yaml:"blob"`
		} `yaml:"persistence"`
		ClaimProducers map[string]yamlClaimProducerEntry `yaml:"claim_producers"`
		// Stores is captured into a sentinel field used to reject the
		// retired YAML alias with a precise error. Per the 2026-05-12
		// nomenclature resolution (cross-layer #1, B.6) the `stores:`
		// alias is no longer accepted; configs must use
		// `claim_producers:`.
		Stores     map[string]yamlClaimProducerEntry `yaml:"stores"`
		NamedLocks map[string]locks.NamedLockConfig  `yaml:"named_locks"`
		Executors  map[string]yamlExecutorEntry      `yaml:"executors"`
		Publishers map[string]yamlPublisherEntry     `yaml:"publishers"`
		// MaxParkDuration is the per-reason max_park_duration map. Keys
		// are the stored ParkReason values ("await_callback" / "snooze");
		// values are time.Duration. Spec §Parked-state taxonomy.
		MaxParkDuration map[string]time.Duration `yaml:"max_park_duration"`
		// Retention is the `retention:` block. Spec
		// .ok-planner/specs/2026-05-15-data-platform-extensions-design.md
		// §Retention.
		Retention *yamlRetention `yaml:"retention"`
		// LateBindServiceProxies maps protocol name → proxy service name
		// for late-bind dispatch resolution and fan-out subscription.
		LateBindServiceProxies map[string]string `yaml:"late_bind_service_proxies"`
	}
	if err := yaml.Unmarshal([]byte(expanded), &wrapper); err != nil {
		return RimskyConfig{}, fmt.Errorf("parse rimsky config %q: %w", path, err)
	}
	if len(wrapper.Stores) > 0 {
		return RimskyConfig{}, fmt.Errorf("rimsky config %q: unknown config key `stores`; rename to `claim_producers` (the `stores:` alias was retired in the 2026-05-12 nomenclature resolution)", path)
	}
	rawProducers := wrapper.ClaimProducers
	for name, e := range rawProducers {
		if e.LegacyWriteSemantics != "" {
			return RimskyConfig{}, fmt.Errorf("rimsky config %q: claim_producers[%q]: the `write_semantics:` single-value shortcut was retired in the 2026-05-12 nomenclature resolution; use `write_semantics_allowed: [<value>]`", path, name)
		}
		if len(e.LegacyWriteSemanticsEnvelope) > 0 {
			return RimskyConfig{}, fmt.Errorf("rimsky config %q: claim_producers[%q]: `write_semantics_envelope` was renamed to `write_semantics_allowed` in the 2026-05-12 nomenclature resolution", path, name)
		}
	}
	stores := RemoteStoresConfig{Stores: make(map[string]StoreEntry, len(rawProducers))}
	for name, e := range rawProducers {
		envelope, err := parseAllowed(name, e.WriteSemanticsAllowed)
		if err != nil {
			return RimskyConfig{}, fmt.Errorf("rimsky config %q: %w", path, err)
		}
		protocols := e.Protocols
		if len(protocols) == 0 {
			protocols = []string{ProtocolClaimProducer}
		}
		if err := validateProtocols(name, protocols); err != nil {
			return RimskyConfig{}, fmt.Errorf("rimsky config %q: %w", path, err)
		}
		// claim_producers entries must declare claim_producer.
		hasClaimProducer := false
		for _, p := range protocols {
			if p == ProtocolClaimProducer {
				hasClaimProducer = true
			}
		}
		if !hasClaimProducer {
			return RimskyConfig{}, fmt.Errorf("rimsky config %q: claim_producers[%q]: protocols must include %q", path, name, ProtocolClaimProducer)
		}
		stores.Stores[name] = StoreEntry{
			Endpoint:              e.Endpoint,
			Capabilities:          claimproducer.Capabilities{WriteSemanticsAllowed: envelope},
			Protocols:             protocols,
			ObservabilityEndpoint: e.ObservabilityEndpoint,
		}
	}
	executors := ExecutorsConfig{Executors: make(map[string]ExecutorEntry, len(wrapper.Executors))}
	for name, e := range wrapper.Executors {
		protocols := e.Protocols
		if len(protocols) == 0 {
			protocols = []string{ProtocolExecutor}
		}
		if err := validateProtocols(name, protocols); err != nil {
			return RimskyConfig{}, fmt.Errorf("rimsky config %q: %w", path, err)
		}
		hasExecutor := false
		for _, p := range protocols {
			if p == ProtocolExecutor {
				hasExecutor = true
			}
		}
		if !hasExecutor {
			return RimskyConfig{}, fmt.Errorf("rimsky config %q: executors[%q]: protocols must include %q", path, name, ProtocolExecutor)
		}
		executors.Executors[name] = ExecutorEntry{
			Transport:             e.Transport,
			Endpoint:              e.Endpoint,
			TLS:                   e.TLS,
			Protocols:             protocols,
			ObservabilityEndpoint: e.ObservabilityEndpoint,
		}
	}
	publishersCfg := RemotePublishersConfig{Publishers: make(map[string]PublisherEntry, len(wrapper.Publishers))}
	for name, e := range wrapper.Publishers {
		protocols := e.Protocols
		if len(protocols) == 0 {
			protocols = []string{ProtocolPublisher}
		}
		if err := validateProtocols(name, protocols); err != nil {
			return RimskyConfig{}, fmt.Errorf("rimsky config %q: %w", path, err)
		}
		hasPublisher := false
		for _, p := range protocols {
			if p == ProtocolPublisher {
				hasPublisher = true
			}
		}
		if !hasPublisher {
			return RimskyConfig{}, fmt.Errorf("rimsky config %q: publishers[%q]: protocols must include %q", path, name, ProtocolPublisher)
		}
		publishersCfg.Publishers[name] = PublisherEntry{
			Endpoint:              e.Endpoint,
			Protocols:             protocols,
			ObservabilityEndpoint: e.ObservabilityEndpoint,
		}
	}
	pcfg := persistence.Config{Driver: wrapper.Persistence.Driver}
	if wrapper.Persistence.Postgres != nil {
		pcfg.Postgres = &persistence.PostgresConfig{
			DSN:             wrapper.Persistence.Postgres.DSN,
			MaxOpenConns:    wrapper.Persistence.Postgres.MaxOpenConns,
			MaxIdleConns:    wrapper.Persistence.Postgres.MaxIdleConns,
			ConnMaxLifetime: wrapper.Persistence.Postgres.ConnMaxLifetime,
		}
	}
	if wrapper.Persistence.SQLite != nil {
		pcfg.SQLite = &persistence.SQLiteConfig{Path: wrapper.Persistence.SQLite.Path}
	}
	if err := pcfg.Validate(); err != nil {
		return RimskyConfig{}, fmt.Errorf("rimsky config %q: persistence: %w", path, err)
	}

	bcfg := persistence.DefaultBlobConfig()
	if blob := wrapper.Persistence.Blob; blob != nil {
		if blob.Backend != "" {
			bcfg.Backend = blob.Backend
		}
		if blob.SpillThresholdBytes > 0 {
			bcfg.SpillThresholdBytes = blob.SpillThresholdBytes
		}
		if blob.Filesystem != nil {
			bcfg.Filesystem.Root = blob.Filesystem.Root
		}
		if blob.PgLargeObject != nil {
			bcfg.PgLargeObject.Schema = blob.PgLargeObject.Schema
		}
		if blob.Retention != nil {
			if blob.Retention.OrphanSweepInterval > 0 {
				bcfg.Retention.OrphanSweepInterval = blob.Retention.OrphanSweepInterval
			}
			if blob.Retention.RetentionAfterUnreferenced > 0 {
				bcfg.Retention.RetentionAfterUnreferenced = blob.Retention.RetentionAfterUnreferenced
			}
		}
	}
	if err := persistence.ValidateBlobConfig(bcfg); err != nil {
		return RimskyConfig{}, fmt.Errorf("rimsky config %q: persistence.blob: %w", path, err)
	}

	// Validate per-reason max_park_duration keys against the known
	// ParkReason storage forms. Unknown keys reject at startup so
	// operators get a precise error rather than silently-ignored config.
	if err := validateMaxParkDurationKeys(wrapper.MaxParkDuration); err != nil {
		return RimskyConfig{}, fmt.Errorf("rimsky config %q: %w", path, err)
	}

	retentionCfg, err := parseRetention(wrapper.Retention)
	if err != nil {
		return RimskyConfig{}, fmt.Errorf("rimsky config %q: %w", path, err)
	}

	return RimskyConfig{
		Persistence:            pcfg,
		Blob:                   bcfg,
		Stores:                 stores,
		NamedLocks:             locks.NamedLocksConfig{Locks: wrapper.NamedLocks},
		Executors:              executors,
		Publishers:             publishersCfg,
		MaxParkDuration:        wrapper.MaxParkDuration,
		Retention:              retentionCfg,
		LateBindServiceProxies: wrapper.LateBindServiceProxies,
	}, nil
}

// parseRetention resolves the `retention:` block into a
// runtime.RetentionConfig, applying the documented defaults for any absent
// key so retention is on by default. An explicit zero disables the
// corresponding sweep (the pointer fields distinguish absent from zero); a
// negative value is rejected.
func parseRetention(in *yamlRetention) (runtime.RetentionConfig, error) {
	out := runtime.RetentionConfig{
		RecentFramesKept:             defaultRetentionRecentFramesKept,
		LineageTrailing:              defaultRetentionLineageTrailing,
		ClaimHandlesTrailing:         defaultRetentionClaimHandlesTrailing,
		MessageIdempotenciesTrailing: defaultRetentionMessageIdempotenciesTrailing,
	}
	if in == nil {
		return out, nil
	}
	if in.RecentFramesKept != nil {
		if *in.RecentFramesKept < 0 {
			return runtime.RetentionConfig{}, fmt.Errorf("retention.recent_frames_kept must be non-negative")
		}
		out.RecentFramesKept = *in.RecentFramesKept
	}
	if in.LineageTrailing != nil {
		if *in.LineageTrailing < 0 {
			return runtime.RetentionConfig{}, fmt.Errorf("retention.lineage_trailing must be non-negative")
		}
		out.LineageTrailing = *in.LineageTrailing
	}
	if in.ClaimHandlesTrailing != nil {
		if *in.ClaimHandlesTrailing < 0 {
			return runtime.RetentionConfig{}, fmt.Errorf("retention.claim_handles_trailing must be non-negative")
		}
		out.ClaimHandlesTrailing = *in.ClaimHandlesTrailing
	}
	if in.MessageIdempotenciesTrailing != nil {
		if *in.MessageIdempotenciesTrailing < 0 {
			return runtime.RetentionConfig{}, fmt.Errorf("retention.message_idempotencies_trailing must be non-negative")
		}
		out.MessageIdempotenciesTrailing = *in.MessageIdempotenciesTrailing
	}
	return out, nil
}

// validateMaxParkDurationKeys rejects keys that are not real stored
// ParkReason values. The closed two-value enum stores exactly
// `await_callback` and `snooze` (the runner persists nothing else), and
// the per-reason sweep does exact-equality on the stored reason — so a
// cap keyed by anything outside that set can never match a parked row.
// Accepting the wider obsolete vocabulary (`time_wait`, `callback_wait`,
// …) silently swallowed dead config; we reject it at load so an operator
// gets a precise startup error instead of a cap that never fires.
func validateMaxParkDurationKeys(m map[string]time.Duration) error {
	for k, v := range m {
		switch k {
		case "await_callback", "snooze":
		default:
			return fmt.Errorf("max_park_duration: unknown reason key %q (one of: await_callback, snooze)", k)
		}
		if v < 0 {
			return fmt.Errorf("max_park_duration[%q]: duration must be non-negative", k)
		}
	}
	return nil
}

// parseAllowed normalizes the operator-declared write_semantics_allowed
// into a non-empty []claimproducer.WriteSemantics. Returns an error when the
// list is absent or any value is unknown. The legacy single-value
// `write_semantics:` shortcut and `write_semantics_envelope:` alias
// are rejected at the loader entry point (cross-layer #6 / C.1, C.2).
func parseAllowed(name string, allowed []string) ([]claimproducer.WriteSemantics, error) {
	values := make([]claimproducer.WriteSemantics, 0, len(allowed))
	for _, raw := range allowed {
		ws, ok := claimproducer.ParseWriteSemantics(raw)
		if !ok {
			return nil, fmt.Errorf("claim_producers[%q]: unknown write_semantics value %q", name, raw)
		}
		values = append(values, ws)
	}
	if len(values) == 0 {
		return nil, fmt.Errorf("claim_producers[%q]: write_semantics_allowed is required", name)
	}
	// Sort + dedup for stable comparisons downstream.
	sort.Slice(values, func(i, j int) bool { return string(values[i]) < string(values[j]) })
	deduped := values[:0]
	for i, v := range values {
		if i > 0 && v == values[i-1] {
			continue
		}
		deduped = append(deduped, v)
	}
	return deduped, nil
}

// validateProtocols rejects unknown protocol values. Known set: the
// declared protocol constants in this package. Unknown protocols fail
// at startup with a precise error.
func validateProtocols(name string, protocols []string) error {
	for _, p := range protocols {
		switch p {
		case ProtocolClaimProducer, ProtocolExecutor, claimproducer.ProtocolLifecycleSubscriber,
			ProtocolPublisher, claimproducer.ProtocolValidation, claimproducer.ProtocolDataProcessing:
		default:
			return fmt.Errorf("peer %q: unknown protocol %q", name, p)
		}
	}
	return nil
}

// dialRemoteStores walks each entry, dials the gRPC endpoint, runs the
// Capabilities() handshake under a bounded timeout, validates the
// operator-declared envelope is a subset of the producer-advertised
// envelope, and registers the resulting Client in reg. On any failure
// (unreachable, mismatch, timeout), already-dialed clients are closed
// and the error is returned for the caller to propagate as a startup
// failure.
//
// Returns a non-nil *Registry even on the empty-config path.
//
// persist + lateBindServiceProxies wire the Registry's late-bind
// resolution hooks: lookupBindings reads a per-instance late-bound
// service catalog from rimsky_instances.service_bindings, and the proxy
// map maps a claim-producer name to the proxy peer the supervisor
// dispatches against when the producer is late-bound. Both are inert
// (no late-bind behavior) when the proxy map is empty.
func dialRemoteStores(
	ctx context.Context,
	cfg RemoteStoresConfig,
	persist persistence.Tables,
	lateBindServiceProxies map[string]string,
) (*locks.Registry, error) {
	// Bindings-lookup hook backed by the live persistence layer.
	lookupBindings := func(ctx context.Context, instanceID string) (map[string]json.RawMessage, bool, error) {
		return lookupInstanceBindings(ctx, persist, instanceID)
	}
	reg := locks.NewRegistry(
		locks.WithLookupInstanceBindings(lookupBindings),
		locks.WithLateBindServiceProxies(lateBindServiceProxies),
	)
	for name, entry := range cfg.Stores {
		if err := validateStoreEntry(name, entry); err != nil {
			reg.Close()
			return nil, fmt.Errorf("dialRemoteStores: %w", err)
		}
		dialCtx, cancel := context.WithTimeout(ctx, capabilitiesHandshakeTimeout)
		client, err := peer.Dial(dialCtx, name, entry.Endpoint)
		cancel()
		if err != nil {
			reg.Close()
			return nil, fmt.Errorf("dialRemoteStores: producer %q: %w", name, err)
		}
		if err := client.ValidateCapabilities(entry.Capabilities); err != nil {
			client.Close()
			reg.Close()
			return nil, fmt.Errorf("dialRemoteStores: %w", err)
		}
		reg.Add(name, client)
	}
	return reg, nil
}

// lookupInstanceBindings reads a per-instance late-bound service catalog
// from rimsky_instances.service_bindings. Backs the Registry's late-bind
// resolution hook (consumed by the executor resolver at dispatch). Returns
// (bindings, true, nil) when the row exists and carries a non-empty
// service_bindings blob; (nil, false, nil) when the instance is unknown or
// has no bindings.
//
// The instance read runs inside a short transaction: every Table method
// requires an explicit tx and panics on nil under both the SQLite and
// Postgres drivers (option-C contract; see
// foundation/persistence/sqlite/deadlock_guard_test.go). `instanceID` is a
// string because the resolver hook is typed that way; `shared.UUID` is an
// alias for google/uuid.UUID, parsed via uuid.Parse.
func lookupInstanceBindings(ctx context.Context, persist persistence.Tables, instanceID string) (map[string]json.RawMessage, bool, error) {
	instID, err := uuid.Parse(instanceID)
	if err != nil {
		return nil, false, err
	}
	var row *persistence.InstanceRow
	if err := persist.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		r, err := persist.Instances().Get(ctx, instID, tx)
		row = r
		return err
	}); err != nil {
		return nil, false, err
	}
	if row == nil || len(row.ServiceBindings) == 0 {
		return nil, false, nil
	}
	var bindings map[string]json.RawMessage
	if err := json.Unmarshal(row.ServiceBindings, &bindings); err != nil {
		return nil, false, err
	}
	return bindings, true, nil
}

// DialLifecycleSubscribers walks the union of claim_producers and
// executors and dials a LifecycleClient for any peer whose protocols
// list includes "lifecycle_subscriber". Each per-peer dial is bounded
// by capabilitiesHandshakeTimeout (same envelope as dialRemoteStores)
// so a wedged peer at startup cannot block the rimsky process forever.
// Returns a non-nil *LifecycleRegistry even on the empty path.
//
// Exported so the supervisor entrypoint can dial its own outbound
// lifecycle subscribers (the supervisor fires OnRunScopeTerminal at
// sub-graph and fanout-partition scope closes) — control-api dials the
// parallel set for the main-scope close.
func DialLifecycleSubscribers(ctx context.Context, stores RemoteStoresConfig, execs ExecutorsConfig) (*locks.LifecycleRegistry, error) {
	reg := locks.NewLifecycleRegistry()
	for name, entry := range stores.Stores {
		if !entry.HasProtocol(claimproducer.ProtocolLifecycleSubscriber) {
			continue
		}
		dialCtx, cancel := context.WithTimeout(ctx, capabilitiesHandshakeTimeout)
		client, err := peer.DialLifecycle(dialCtx, name, entry.Endpoint)
		cancel()
		if err != nil {
			reg.Close()
			return nil, fmt.Errorf("DialLifecycleSubscribers: producer %q: %w", name, err)
		}
		reg.Add(name, client)
	}
	for name, entry := range execs.Executors {
		if !entry.HasProtocol(claimproducer.ProtocolLifecycleSubscriber) {
			continue
		}
		dialCtx, cancel := context.WithTimeout(ctx, capabilitiesHandshakeTimeout)
		client, err := peer.DialLifecycle(dialCtx, name, entry.Endpoint)
		cancel()
		if err != nil {
			reg.Close()
			return nil, fmt.Errorf("DialLifecycleSubscribers: executor %q: %w", name, err)
		}
		reg.Add(name, client)
	}
	return reg, nil
}

// validateStoreEntry rejects malformed endpoint URLs at startup so
// operators get a clear error rather than a downstream gRPC dial
// failure. Acceptable: empty scheme (host:port form) or "grpc://"
// prefix; anything else (http://, https://, …) is rejected with a
// pointer to the expected form.
func validateStoreEntry(name string, entry StoreEntry) error {
	if entry.Endpoint == "" {
		return fmt.Errorf("claim_producer %q: endpoint is required", name)
	}
	for _, badScheme := range []string{"http://", "https://", "tcp://", "unix://"} {
		if len(entry.Endpoint) >= len(badScheme) && entry.Endpoint[:len(badScheme)] == badScheme {
			return fmt.Errorf("claim_producer %q: endpoint scheme must be grpc:// (got %s)", name, badScheme)
		}
	}
	return nil
}
