// ClaimProducers + executors config + remote-dialing helpers for the
// rimsky processes. Per spec docs/specs/2026-05-04-service-protocol-
// contract.md and the layer-crystallization plan (Phase 4 / Task 28):
// the unified rimsky.yml shape (claim_producers + named_locks + executors
// loaded together by all four rimsky binaries — control-api, supervisor,
// scheduler, migrate — from $RIMSKY_CONFIG).
//
// Phase 4 changes:
//   - block `stores:` renamed `claim_producers:`.
//   - each producer / executor entry gains optional `protocols: [...]`
//     declaring which protocols it speaks. Default for claim_producers
//     is [claim_producer]; default for executors is [executor].
//   - producer entries gain required `write_semantics_envelope: [...]`
//     declaring the operator-permissible value set (must be a non-empty
//     subset of the producer-advertised envelope).
//   - the previous singular `write_semantics:` field is supported as a
//     legacy shortcut for a single-element envelope.
package config

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"sort"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/fallguy/rimsky/foundation/integration/remote"
	"github.com/fallguy/rimsky/foundation/locks"
	"github.com/fallguy/rimsky/foundation/persistence"
)

// capabilitiesHandshakeTimeout bounds the per-producer Capabilities() RPC
// at startup. Without it a producer-service that accepts the connection
// but never replies blocks the rimsky process forever.
const capabilitiesHandshakeTimeout = 30 * time.Second

// Protocol enumerates the wire protocols a peer may speak. Validated at
// parse time; any unknown value fails startup.
const (
	ProtocolClaimProducer       = "claim_producer"
	ProtocolExecutor            = "executor"
	ProtocolLifecycleSubscriber = "lifecycle_subscriber"
)

// StoreEntry is the per-claim-producer config from rimsky.yml. The
// type is named StoreEntry for backward compatibility; new code should
// read it as "claim-producer entry."
type StoreEntry struct {
	Endpoint     string
	Capabilities locks.Capabilities
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
type RimskyConfig struct {
	Persistence persistence.Config
	Stores      RemoteStoresConfig
	NamedLocks  locks.NamedLocksConfig
	Executors   ExecutorsConfig
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
		Endpoint               string   `yaml:"endpoint"`
		Protocols              []string `yaml:"protocols"`
		WriteSemanticsEnvelope []string `yaml:"write_semantics_envelope"`
		WriteSemantics         string   `yaml:"write_semantics"` // legacy shortcut
		ObservabilityEndpoint  string   `yaml:"observability_endpoint"`
	}
	type yamlExecutorEntry struct {
		Transport             string   `yaml:"transport"`
		Endpoint              string   `yaml:"endpoint"`
		TLS                   string   `yaml:"tls"`
		Protocols             []string `yaml:"protocols"`
		ObservabilityEndpoint string   `yaml:"observability_endpoint"`
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
		} `yaml:"persistence"`
		ClaimProducers map[string]yamlClaimProducerEntry `yaml:"claim_producers"`
		// Stores: is supported as a deprecated alias for the
		// claim_producers: block during the layer-crystallization
		// rollout. New configs SHOULD use claim_producers:.
		Stores     map[string]yamlClaimProducerEntry `yaml:"stores"`
		NamedLocks map[string]locks.NamedLockConfig  `yaml:"named_locks"`
		Executors  map[string]yamlExecutorEntry      `yaml:"executors"`
	}
	if err := yaml.Unmarshal([]byte(expanded), &wrapper); err != nil {
		return RimskyConfig{}, fmt.Errorf("parse rimsky config %q: %w", path, err)
	}
	rawProducers := wrapper.ClaimProducers
	if len(rawProducers) == 0 && len(wrapper.Stores) > 0 {
		rawProducers = wrapper.Stores
	}
	stores := RemoteStoresConfig{Stores: make(map[string]StoreEntry, len(rawProducers))}
	for name, e := range rawProducers {
		envelope, err := parseEnvelope(name, e.WriteSemanticsEnvelope, e.WriteSemantics)
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
			Capabilities:          locks.Capabilities{WriteSemanticsEnvelope: envelope},
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

	return RimskyConfig{
		Persistence: pcfg,
		Stores:      stores,
		NamedLocks:  locks.NamedLocksConfig{Locks: wrapper.NamedLocks},
		Executors:   executors,
	}, nil
}

// parseEnvelope normalizes the operator-declared write_semantics_envelope
// into a non-empty []locks.WriteSemantics. Falls back to a single-element
// envelope from the legacy `write_semantics:` field when envelope is
// empty. Returns an error when both forms are absent or any value is
// unknown.
func parseEnvelope(name string, envelope []string, legacy string) ([]locks.WriteSemantics, error) {
	values := make([]locks.WriteSemantics, 0, len(envelope))
	for _, raw := range envelope {
		ws, ok := locks.ParseWriteSemantics(raw)
		if !ok {
			return nil, fmt.Errorf("claim_producers[%q]: unknown write_semantics value %q", name, raw)
		}
		values = append(values, ws)
	}
	if len(values) == 0 && legacy != "" {
		ws, ok := locks.ParseWriteSemantics(legacy)
		if !ok {
			return nil, fmt.Errorf("claim_producers[%q]: unknown write_semantics value %q", name, legacy)
		}
		values = append(values, ws)
	}
	if len(values) == 0 {
		return nil, fmt.Errorf("claim_producers[%q]: write_semantics_envelope is required", name)
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

// validateProtocols rejects unknown protocol values. Known: claim_producer,
// executor, lifecycle_subscriber.
func validateProtocols(name string, protocols []string) error {
	for _, p := range protocols {
		switch p {
		case ProtocolClaimProducer, ProtocolExecutor, ProtocolLifecycleSubscriber:
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
func dialRemoteStores(ctx context.Context, cfg RemoteStoresConfig) (*locks.Registry, error) {
	reg := locks.NewRegistry()
	for name, entry := range cfg.Stores {
		if err := validateStoreEntry(name, entry); err != nil {
			reg.Close()
			return nil, fmt.Errorf("dialRemoteStores: %w", err)
		}
		dialCtx, cancel := context.WithTimeout(ctx, capabilitiesHandshakeTimeout)
		client, err := remote.Dial(dialCtx, name, entry.Endpoint)
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

// dialLifecycleSubscribers walks the union of claim_producers and
// executors and dials a LifecycleClient for any peer whose protocols
// list includes "lifecycle_subscriber". Each per-peer dial is bounded
// by capabilitiesHandshakeTimeout (same envelope as dialRemoteStores)
// so a wedged peer at startup cannot block the rimsky process forever.
// Returns a non-nil *LifecycleRegistry even on the empty path.
func dialLifecycleSubscribers(ctx context.Context, stores RemoteStoresConfig, execs ExecutorsConfig) (*locks.LifecycleRegistry, error) {
	reg := locks.NewLifecycleRegistry()
	for name, entry := range stores.Stores {
		if !entry.HasProtocol(ProtocolLifecycleSubscriber) {
			continue
		}
		dialCtx, cancel := context.WithTimeout(ctx, capabilitiesHandshakeTimeout)
		client, err := remote.DialLifecycle(dialCtx, name, entry.Endpoint)
		cancel()
		if err != nil {
			reg.Close()
			return nil, fmt.Errorf("dialLifecycleSubscribers: producer %q: %w", name, err)
		}
		reg.Add(name, client)
	}
	for name, entry := range execs.Executors {
		if !entry.HasProtocol(ProtocolLifecycleSubscriber) {
			continue
		}
		dialCtx, cancel := context.WithTimeout(ctx, capabilitiesHandshakeTimeout)
		client, err := remote.DialLifecycle(dialCtx, name, entry.Endpoint)
		cancel()
		if err != nil {
			reg.Close()
			return nil, fmt.Errorf("dialLifecycleSubscribers: executor %q: %w", name, err)
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
