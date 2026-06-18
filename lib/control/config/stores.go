// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package config

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"gopkg.in/yaml.v3"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/locks"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/graph/node"
	"github.com/rimsky-ai/rimsky-core/lib/protocols/claimproducer"
	"github.com/rimsky-ai/rimsky-core/lib/runtime"
	peer "github.com/rimsky-ai/rimsky-core/lib/runtime/peer"
)

const (
	defaultRetentionRecentFramesKept             = 100
	defaultRetentionTraceTrailing                = 30 * 24 * time.Hour
	defaultRetentionLineageTrailing              = 30 * 24 * time.Hour
	defaultRetentionClaimHandlesTrailing         = 30 * 24 * time.Hour
	defaultRetentionMessageIdempotenciesTrailing = 24 * time.Hour
)

type yamlRetention struct {
	RecentFramesKept             *int           `yaml:"recent_frames_kept"`
	TraceTrailing                *time.Duration `yaml:"trace_trailing"`
	LineageTrailing              *time.Duration `yaml:"lineage_trailing"`
	ClaimHandlesTrailing         *time.Duration `yaml:"claim_handles_trailing"`
	MessageIdempotenciesTrailing *time.Duration `yaml:"message_idempotencies_trailing"`
}

const capabilitiesHandshakeTimeout = 30 * time.Second

// @concept: service — the protocol-role vocabulary a service may
// claim. ProtocolClaimProducer / ProtocolExecutor / ProtocolPublisher
// enumerate the wire protocols a peer may speak. Validated at parse
// time; any unknown value fails startup. The mix-in protocols
// (lifecycle_subscriber, validation, data_processing) live in
// protocols/claimproducer — the wire-vocabulary owner — and are
// referenced from there. Only the three role-anchor protocols specific
// to rimsky.yml's three top-level blocks (claim_producers, executors,
// publishers) are declared here.
const (
	ProtocolClaimProducer = "claim_producer"
	ProtocolExecutor      = "executor"
	ProtocolPublisher     = "publisher"
)

type StoreEntry struct {
	Endpoint              string
	Capabilities          claimproducer.Capabilities
	TLS                   string
	Protocols             []string
	ObservabilityEndpoint string
}

func (e StoreEntry) HasProtocol(p string) bool {
	for _, declared := range e.Protocols {
		if declared == p {
			return true
		}
	}
	return false
}

type RemoteStoresConfig struct {
	Stores map[string]StoreEntry
}

type ExecutorEntry struct {
	Transport             string
	Endpoint              string
	TLS                   string
	Protocols             []string
	ObservabilityEndpoint string
}

func (e ExecutorEntry) HasProtocol(p string) bool {
	for _, declared := range e.Protocols {
		if declared == p {
			return true
		}
	}
	return false
}

type ExecutorsConfig struct {
	Executors map[string]ExecutorEntry
}

type PublisherEntry struct {
	Endpoint              string
	TLS                   string
	Protocols             []string
	ObservabilityEndpoint string
}

func (e PublisherEntry) HasProtocol(p string) bool {
	for _, declared := range e.Protocols {
		if declared == p {
			return true
		}
	}
	return false
}

type RemotePublishersConfig struct {
	Publishers map[string]PublisherEntry
}

func (c ExecutorsConfig) Validate() error {
	for name, e := range c.Executors {
		if e.Transport == "" {
			return fmt.Errorf("executor %q: transport required", name)
		}
		if e.Endpoint == "" {
			return fmt.Errorf("executor %q: endpoint required", name)
		}
		if e.Transport == "http" && e.TLS == peer.TLSModeRequired && !strings.HasPrefix(e.Endpoint, "https://") {
			return fmt.Errorf("executor %q: tls: required with transport: http needs an https:// endpoint, got %q", name, e.Endpoint)
		}
	}
	return nil
}

func (c ExecutorsConfig) ExecutorDeclared(name string) bool {
	_, ok := c.Executors[name]
	return ok
}

// @concept: rimsky-yml
type RimskyConfig struct {
	Persistence            persistence.Config
	Blob                   persistence.BlobConfig
	Stores                 RemoteStoresConfig
	NamedLocks             locks.NamedLocksConfig
	Executors              ExecutorsConfig
	Publishers             RemotePublishersConfig
	MaxParkDuration        map[string]time.Duration
	Retention              runtime.RetentionConfig
	LateBindServiceProxies map[string]string
	RefValidationMode      node.RefValidationMode
}

func ParseRefValidationMode(raw string) (node.RefValidationMode, error) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "", "all":
		return node.RefValidateAll, nil
	case "available":
		return node.RefValidateAvailable, nil
	case "none":
		return node.RefValidateNone, nil
	default:
		return node.RefValidateAll, fmt.Errorf("ref_validation_mode: unknown value %q (one of: all, available, none)", raw)
	}
}

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
		Endpoint                     string   `yaml:"endpoint"`
		TLS                          string   `yaml:"tls"`
		Protocols                    []string `yaml:"protocols"`
		WriteSemanticsAllowed        []string `yaml:"write_semantics_allowed"`
		LegacyWriteSemantics         string   `yaml:"write_semantics"`
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
		TLS                   string   `yaml:"tls"`
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
		ClaimProducers         map[string]yamlClaimProducerEntry `yaml:"claim_producers"`
		Stores                 map[string]yamlClaimProducerEntry `yaml:"stores"`
		NamedLocks             map[string]locks.NamedLockConfig  `yaml:"named_locks"`
		Executors              map[string]yamlExecutorEntry      `yaml:"executors"`
		Publishers             map[string]yamlPublisherEntry     `yaml:"publishers"`
		MaxParkDuration        map[string]time.Duration          `yaml:"max_park_duration"`
		Retention              *yamlRetention                    `yaml:"retention"`
		LateBindServiceProxies map[string]string                 `yaml:"late_bind_service_proxies"`
		Templates              struct {
			RefValidationMode string `yaml:"ref_validation_mode"`
		} `yaml:"templates"`
	}
	if err := yaml.Unmarshal([]byte(expanded), &wrapper); err != nil {
		return RimskyConfig{}, fmt.Errorf("parse rimsky config %q: %w", path, err)
	}
	if len(wrapper.Stores) > 0 {
		return RimskyConfig{}, fmt.Errorf("rimsky config %q: unknown config key `stores`; rename to `claim_producers` (the `stores:` alias is no longer accepted)", path)
	}
	rawProducers := wrapper.ClaimProducers
	for name, e := range rawProducers {
		if e.LegacyWriteSemantics != "" {
			return RimskyConfig{}, fmt.Errorf("rimsky config %q: claim_producers[%q]: the `write_semantics:` single-value shortcut is no longer accepted; use `write_semantics_allowed: [<value>]`", path, name)
		}
		if len(e.LegacyWriteSemanticsEnvelope) > 0 {
			return RimskyConfig{}, fmt.Errorf("rimsky config %q: claim_producers[%q]: `write_semantics_envelope` is no longer accepted; rename it to `write_semantics_allowed`", path, name)
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
		hasClaimProducer := false
		for _, p := range protocols {
			if p == ProtocolClaimProducer {
				hasClaimProducer = true
			}
		}
		if !hasClaimProducer {
			return RimskyConfig{}, fmt.Errorf("rimsky config %q: claim_producers[%q]: protocols must include %q", path, name, ProtocolClaimProducer)
		}
		tlsMode, err := parseTLSMode("claim_producers", name, e.TLS)
		if err != nil {
			return RimskyConfig{}, fmt.Errorf("rimsky config %q: %w", path, err)
		}
		stores.Stores[name] = StoreEntry{
			Endpoint:              e.Endpoint,
			Capabilities:          claimproducer.Capabilities{WriteSemanticsAllowed: envelope},
			TLS:                   tlsMode,
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
		tlsMode, err := parseTLSMode("executors", name, e.TLS)
		if err != nil {
			return RimskyConfig{}, fmt.Errorf("rimsky config %q: %w", path, err)
		}
		executors.Executors[name] = ExecutorEntry{
			Transport:             e.Transport,
			Endpoint:              e.Endpoint,
			TLS:                   tlsMode,
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
		tlsMode, err := parseTLSMode("publishers", name, e.TLS)
		if err != nil {
			return RimskyConfig{}, fmt.Errorf("rimsky config %q: %w", path, err)
		}
		publishersCfg.Publishers[name] = PublisherEntry{
			Endpoint:              e.Endpoint,
			TLS:                   tlsMode,
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

	if err := validateMaxParkDurationKeys(wrapper.MaxParkDuration); err != nil {
		return RimskyConfig{}, fmt.Errorf("rimsky config %q: %w", path, err)
	}

	retentionCfg, err := parseRetention(wrapper.Retention)
	if err != nil {
		return RimskyConfig{}, fmt.Errorf("rimsky config %q: %w", path, err)
	}

	refModeRaw := wrapper.Templates.RefValidationMode
	if envMode := os.Getenv("RIMSKY_REF_VALIDATION_MODE"); envMode != "" {
		refModeRaw = envMode
	}
	refMode, err := ParseRefValidationMode(refModeRaw)
	if err != nil {
		return RimskyConfig{}, fmt.Errorf("rimsky config %q: templates.%w", path, err)
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
		RefValidationMode:      refMode,
	}, nil
}

func parseRetention(in *yamlRetention) (runtime.RetentionConfig, error) {
	out := runtime.RetentionConfig{
		RecentFramesKept:             defaultRetentionRecentFramesKept,
		TraceTrailing:                defaultRetentionTraceTrailing,
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
	if in.TraceTrailing != nil {
		if *in.TraceTrailing < 0 {
			return runtime.RetentionConfig{}, fmt.Errorf("retention.trace_trailing must be non-negative")
		}
		out.TraceTrailing = *in.TraceTrailing
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

func parseTLSMode(block, name, raw string) (string, error) {
	switch raw {
	case "", peer.TLSModeOff:
		return peer.TLSModeOff, nil
	case peer.TLSModeRequired:
		return peer.TLSModeRequired, nil
	default:
		return "", fmt.Errorf("%s[%q]: tls: unknown value %q (one of: off, required)", block, name, raw)
	}
}

func ValidProtocols() map[string]bool {
	return map[string]bool{
		ProtocolClaimProducer:                     true,
		ProtocolExecutor:                          true,
		ProtocolPublisher:                         true,
		claimproducer.ProtocolLifecycleSubscriber: true,
		claimproducer.ProtocolValidation:          true,
		claimproducer.ProtocolDataProcessing:      true,
	}
}

func validateProtocols(name string, protocols []string) error {
	known := ValidProtocols()
	for _, p := range protocols {
		if !known[p] {
			return fmt.Errorf("peer %q: unknown protocol %q", name, p)
		}
	}
	return nil
}

func dialRemoteStores(
	ctx context.Context,
	cfg RemoteStoresConfig,
	persist persistence.Tables,
	lateBindServiceProxies map[string]string,
) (*locks.Registry, error) {
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
		client, err := peer.Dial(dialCtx, name, entry.Endpoint, entry.TLS)
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

func DialLifecycleSubscribers(ctx context.Context, stores RemoteStoresConfig, execs ExecutorsConfig) (*locks.LifecycleRegistry, error) {
	reg := locks.NewLifecycleRegistry()
	for name, entry := range stores.Stores {
		if !entry.HasProtocol(claimproducer.ProtocolLifecycleSubscriber) {
			continue
		}
		dialCtx, cancel := context.WithTimeout(ctx, capabilitiesHandshakeTimeout)
		client, err := peer.DialLifecycle(dialCtx, name, entry.Endpoint, entry.TLS)
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
		client, err := peer.DialLifecycle(dialCtx, name, entry.Endpoint, entry.TLS)
		cancel()
		if err != nil {
			reg.Close()
			return nil, fmt.Errorf("DialLifecycleSubscribers: executor %q: %w", name, err)
		}
		reg.Add(name, client)
	}
	return reg, nil
}

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
