// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

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

	"github.com/rimsky-ai/rimsky-core/lib/foundation/lifecycle"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/locks"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/pki"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/spec"
	"github.com/rimsky-ai/rimsky-core/lib/protocols/claimproducer"
	configload "github.com/rimsky-ai/rimsky-core/lib/protocols/config"
	"github.com/rimsky-ai/rimsky-core/lib/runtime"
	service "github.com/rimsky-ai/rimsky-core/lib/runtime/service"
)

const (
	defaultRetentionRecentFramesKept             = 100
	defaultRetentionTraceTrailing                = 30 * 24 * time.Hour
	defaultRetentionLineageTrailing              = 30 * 24 * time.Hour
	defaultRetentionClaimHandlesTrailing         = 30 * 24 * time.Hour
	defaultRetentionMessageIdempotenciesTrailing = 24 * time.Hour
	defaultRetentionLifecycleOutboxTrailing      = time.Duration(0)
)

type yamlRetention struct {
	RecentFramesKept             *int           `yaml:"recent_frames_kept"`
	TraceTrailing                *time.Duration `yaml:"trace_trailing"`
	LineageTrailing              *time.Duration `yaml:"lineage_trailing"`
	ClaimHandlesTrailing         *time.Duration `yaml:"claim_handles_trailing"`
	MessageIdempotenciesTrailing *time.Duration `yaml:"message_idempotencies_trailing"`
	LifecycleOutboxTrailing      *time.Duration `yaml:"lifecycle_outbox_trailing"`
}

// @decision: service-delivery-stall-signal
type yamlServiceDelivery struct {
	StallAfter *time.Duration `yaml:"stall_after"`
}

// @decision: service-delivery-stall-signal
type ServiceDeliveryConfig struct {
	StallAfter time.Duration
}

type yamlDispatchDefaults struct {
	SyncRPCDeadline *time.Duration           `yaml:"sync_rpc_deadline"`
	MaxQuietPeriod  *time.Duration           `yaml:"max_quiet_period"`
	MaxRuntime      *time.Duration           `yaml:"max_runtime"`
	MaxRetries      *int                     `yaml:"max_retries"`
	RetryBackoff    *spec.RetryBackoffConfig `yaml:"retry_backoff"`
}

// @decision: three-dispatch-deadlines
// @decision: dispatch-defaults-cover-every-node-timing-key
type DispatchDefaultsConfig struct {
	SyncRPCDeadlineDefault time.Duration
	MaxQuietPeriodDefault  time.Duration
	MaxRuntimeDefault      time.Duration
	MaxRetriesDefault      int
	RetryBackoffDefault    *spec.RetryBackoffConfig
}

const capabilitiesHandshakeTimeout = 30 * time.Second

// @concept: service
const (
	ProtocolClaimProducer = "claim_producer"
	ProtocolExecutor      = "executor"
	ProtocolPublisher     = "publisher"
)

type ClaimProducerEntry struct {
	Endpoint              string
	Capabilities          claimproducer.Capabilities
	TLS                   string
	Protocols             []string
	ObservabilityEndpoint string
}

func (e ClaimProducerEntry) HasProtocol(p string) bool {
	for _, declared := range e.Protocols {
		if declared == p {
			return true
		}
	}
	return false
}

type RemoteClaimProducersConfig struct {
	ClaimProducers map[string]ClaimProducerEntry
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

type ValidatorEntry struct {
	Endpoint              string
	TLS                   string
	Protocols             []string
	ObservabilityEndpoint string
}

func (e ValidatorEntry) HasProtocol(p string) bool {
	for _, declared := range e.Protocols {
		if declared == p {
			return true
		}
	}
	return false
}

type RemoteValidatorsConfig struct {
	Validators map[string]ValidatorEntry
}

type DataProcessorEntry struct {
	Endpoint              string
	TLS                   string
	Protocols             []string
	ObservabilityEndpoint string
}

func (e DataProcessorEntry) HasProtocol(p string) bool {
	for _, declared := range e.Protocols {
		if declared == p {
			return true
		}
	}
	return false
}

type RemoteDataProcessorsConfig struct {
	DataProcessors map[string]DataProcessorEntry
}

func (c ExecutorsConfig) Validate() error {
	for name, e := range c.Executors {
		if e.Transport == "" {
			return fmt.Errorf("executor %q: transport required", name)
		}
		if e.Endpoint == "" {
			return fmt.Errorf("executor %q: endpoint required", name)
		}
		if e.Transport == "http" && e.TLS == service.TLSModeRequired && !strings.HasPrefix(e.Endpoint, "https://") {
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
	ClaimProducers         RemoteClaimProducersConfig
	NamedLocks             locks.NamedLocksConfig
	Executors              ExecutorsConfig
	Publishers             RemotePublishersConfig
	Validators             RemoteValidatorsConfig
	DataProcessors         RemoteDataProcessorsConfig
	Retention              runtime.RetentionConfig
	ServiceDelivery        ServiceDeliveryConfig
	Dispatch               DispatchDefaultsConfig
	Supervisor             SupervisorSection
	LateBindServiceProxies map[string]string
	ServiceAuth            string
	Topology               persistence.Topology
	Warnings               []string

	// @concept: validation
	UnreachableValidatorPolicy string

	ObservabilityRefreshInterval time.Duration
}

const defaultObservabilityRefreshInterval = 60 * time.Second

func parseObservabilityRefreshInterval() (time.Duration, error) {
	v := os.Getenv("RIMSKY_OBSERVABILITY_REFRESH_INTERVAL")
	if v == "" {
		return defaultObservabilityRefreshInterval, nil
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		return 0, fmt.Errorf("RIMSKY_OBSERVABILITY_REFRESH_INTERVAL=%q: %w", v, err)
	}
	if d <= 0 {
		return 0, fmt.Errorf("RIMSKY_OBSERVABILITY_REFRESH_INTERVAL=%q: must be positive", v)
	}
	return d, nil
}

// @decision: service-auth-mtls
func ParseServiceAuth(raw string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "", service.ServiceAuthNone:
		return service.ServiceAuthNone, nil
	case service.ServiceAuthMTLS:
		return service.ServiceAuthMTLS, nil
	default:
		return "", fmt.Errorf("service_auth: unknown value %q (one of: none, mtls)", raw)
	}
}

const (
	unreachableValidatorPolicyPermissiveWarn = "permissive_warn"
	unreachableValidatorPolicyStrict         = "strict"
)

func ParseUnreachableValidatorPolicy(raw string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "", unreachableValidatorPolicyPermissiveWarn:
		return unreachableValidatorPolicyPermissiveWarn, nil
	case unreachableValidatorPolicyStrict:
		return unreachableValidatorPolicyStrict, nil
	default:
		return "", fmt.Errorf("unreachable_validator_policy: unknown value %q (one of: permissive_warn, strict)", raw)
	}
}

func LoadRimskyConfigYAML(path string) (RimskyConfig, error) {
	if _, err := os.Stat(path); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return RimskyConfig{}, fmt.Errorf("rimsky config file not found at %q", path)
		}
		return RimskyConfig{}, fmt.Errorf("read rimsky config %q: %w", path, err)
	}
	type yamlClaimProducerEntry struct {
		Endpoint              string   `yaml:"endpoint"`
		TLS                   string   `yaml:"tls"`
		Protocols             []string `yaml:"protocols"`
		WriteSemanticsAllowed []string `yaml:"write_semantics_allowed"`
		ObservabilityEndpoint string   `yaml:"observability_endpoint"`
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
	type yamlValidatorEntry struct {
		Endpoint              string   `yaml:"endpoint"`
		TLS                   string   `yaml:"tls"`
		Protocols             []string `yaml:"protocols"`
		ObservabilityEndpoint string   `yaml:"observability_endpoint"`
	}
	type yamlSupervisorCallback struct {
		Host          string `yaml:"host"`
		Port          *int   `yaml:"port"`
		AdvertiseHost string `yaml:"advertise_host"`
		AdvertisePort int    `yaml:"advertise_port"`
	}
	// @concept: rimsky-yml
	// @decision: launch-config-injection
	type yamlSupervisor struct {
		SupervisorID        string                 `yaml:"supervisor_id"`
		Concurrency         int                    `yaml:"concurrency"`
		LivenessIntervalMs  int                    `yaml:"liveness_interval_ms"`
		ClaimPollIntervalMs int                    `yaml:"claim_poll_interval_ms"`
		Callback            yamlSupervisorCallback `yaml:"callback"`
	}
	type yamlDataProcessorEntry struct {
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
				MinConns        int           `yaml:"min_conns"`
				ConnMaxLifetime time.Duration `yaml:"conn_max_lifetime"`
			} `yaml:"postgres"`
			SQLite *struct {
				Path string `yaml:"path"`
			} `yaml:"sqlite"`
		} `yaml:"persistence"`
		ClaimProducers map[string]yamlClaimProducerEntry `yaml:"claim_producers"`
		// @concept: named-lock
		NamedLocks                 map[string]locks.NamedLockConfig  `yaml:"named_locks"`
		Executors                  map[string]yamlExecutorEntry      `yaml:"executors"`
		Publishers                 map[string]yamlPublisherEntry     `yaml:"publishers"`
		Validators                 map[string]yamlValidatorEntry     `yaml:"validators"`
		DataProcessors             map[string]yamlDataProcessorEntry `yaml:"data_processors"`
		Retention                  *yamlRetention                    `yaml:"retention"`
		ServiceDelivery            *yamlServiceDelivery              `yaml:"service_delivery"`
		DispatchDefaults           *yamlDispatchDefaults             `yaml:"dispatch_defaults"`
		LateBindServiceProxies     map[string]string                 `yaml:"late_bind_service_proxies"`
		Supervisor                 *yamlSupervisor                   `yaml:"supervisor"`
		ServiceAuth                string                            `yaml:"service_auth"`
		UnreachableValidatorPolicy string                            `yaml:"unreachable_validator_policy"`
	}
	if err := configload.LoadFile(path, &wrapper); err != nil {
		return RimskyConfig{}, err
	}
	rawProducers := wrapper.ClaimProducers
	serviceAuth, err := ParseServiceAuth(wrapper.ServiceAuth)
	if err != nil {
		return RimskyConfig{}, fmt.Errorf("rimsky config %q: %w", path, err)
	}
	if serviceAuth == service.ServiceAuthMTLS {
		if _, err := pki.ParseCAEncryptionKey(os.Getenv(pki.EnvCAEncryptionKey)); err != nil {
			return RimskyConfig{}, fmt.Errorf("rimsky config %q: service_auth: mtls requires %w", path, err)
		}
	}
	// @decision: service-auth-mtls
	serviceTLSDefault := defaultServiceTLSMode(serviceAuth)

	producers := RemoteClaimProducersConfig{ClaimProducers: make(map[string]ClaimProducerEntry, len(rawProducers))}
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
		tlsMode, err := parseTLSMode("claim_producers", name, e.TLS, serviceTLSDefault)
		if err != nil {
			return RimskyConfig{}, fmt.Errorf("rimsky config %q: %w", path, err)
		}
		producers.ClaimProducers[name] = ClaimProducerEntry{
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
		tlsMode, err := parseTLSMode("executors", name, e.TLS, serviceTLSDefault)
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
		tlsMode, err := parseTLSMode("publishers", name, e.TLS, serviceTLSDefault)
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
	validatorsCfg := RemoteValidatorsConfig{Validators: make(map[string]ValidatorEntry, len(wrapper.Validators))}
	for name, e := range wrapper.Validators {
		protocols := e.Protocols
		// @concept: validation
		if len(protocols) == 0 {
			return RimskyConfig{}, fmt.Errorf(
				"rimsky config %q: validators[%q]: protocols is absent, so the entry derives no role to validate and rimsky would never consult it; name the protocols whose registrations it validates",
				path, name)
		}
		if err := validateProtocols(name, protocols); err != nil {
			return RimskyConfig{}, fmt.Errorf("rimsky config %q: %w", path, err)
		}
		hasValidation := false
		for _, p := range protocols {
			if p == claimproducer.ProtocolValidation {
				hasValidation = true
			}
		}
		if !hasValidation {
			return RimskyConfig{}, fmt.Errorf("rimsky config %q: validators[%q]: protocols must include %q", path, name, claimproducer.ProtocolValidation)
		}
		// @concept: validation
		if len(validatorRoleDiscriminators(protocols)) == 0 {
			return RimskyConfig{}, fmt.Errorf(
				"rimsky config %q: validators[%q]: protocols declares only %q, so the entry derives no role to validate and rimsky would never consult it; name the protocols whose registrations it validates",
				path, name, claimproducer.ProtocolValidation)
		}
		tlsMode, err := parseTLSMode("validators", name, e.TLS, serviceTLSDefault)
		if err != nil {
			return RimskyConfig{}, fmt.Errorf("rimsky config %q: %w", path, err)
		}
		validatorsCfg.Validators[name] = ValidatorEntry{
			Endpoint:              e.Endpoint,
			TLS:                   tlsMode,
			Protocols:             protocols,
			ObservabilityEndpoint: e.ObservabilityEndpoint,
		}
	}
	dataProcessorsCfg := RemoteDataProcessorsConfig{DataProcessors: make(map[string]DataProcessorEntry, len(wrapper.DataProcessors))}
	for name, e := range wrapper.DataProcessors {
		protocols := e.Protocols
		if len(protocols) == 0 {
			protocols = []string{claimproducer.ProtocolDataProcessing}
		}
		if err := validateProtocols(name, protocols); err != nil {
			return RimskyConfig{}, fmt.Errorf("rimsky config %q: %w", path, err)
		}
		hasDataProcessing := false
		for _, p := range protocols {
			if p == claimproducer.ProtocolDataProcessing {
				hasDataProcessing = true
			}
		}
		if !hasDataProcessing {
			return RimskyConfig{}, fmt.Errorf("rimsky config %q: data_processors[%q]: protocols must include %q", path, name, claimproducer.ProtocolDataProcessing)
		}
		tlsMode, err := parseTLSMode("data_processors", name, e.TLS, serviceTLSDefault)
		if err != nil {
			return RimskyConfig{}, fmt.Errorf("rimsky config %q: %w", path, err)
		}
		dataProcessorsCfg.DataProcessors[name] = DataProcessorEntry{
			Endpoint:              e.Endpoint,
			TLS:                   tlsMode,
			Protocols:             protocols,
			ObservabilityEndpoint: e.ObservabilityEndpoint,
		}
	}
	topology := persistence.TopologyFromEnv()

	pcfg := persistence.Config{Driver: wrapper.Persistence.Driver}
	if wrapper.Persistence.Postgres != nil {
		pcfg.Postgres = &persistence.PostgresConfig{
			DSN:             wrapper.Persistence.Postgres.DSN,
			MaxOpenConns:    wrapper.Persistence.Postgres.MaxOpenConns,
			MinConns:        wrapper.Persistence.Postgres.MinConns,
			ConnMaxLifetime: wrapper.Persistence.Postgres.ConnMaxLifetime,
		}
	}
	if wrapper.Persistence.SQLite != nil {
		pcfg.SQLite = &persistence.SQLiteConfig{Path: wrapper.Persistence.SQLite.Path}
	}
	if err := pcfg.Validate(); err != nil {
		return RimskyConfig{}, fmt.Errorf("rimsky config %q: persistence: %w", path, err)
	}
	var warnings []string
	if msg, warn := persistence.SQLiteReplicaWarning(pcfg.Driver, topology); warn {
		warnings = append(warnings, msg)
	}

	retentionCfg, err := parseRetention(wrapper.Retention)
	if err != nil {
		return RimskyConfig{}, fmt.Errorf("rimsky config %q: %w", path, err)
	}

	serviceDeliveryCfg, err := parseServiceDelivery(wrapper.ServiceDelivery)
	if err != nil {
		return RimskyConfig{}, fmt.Errorf("rimsky config %q: %w", path, err)
	}

	// @decision: service-delivery-stall-signal
	if err := checkStallSignalPrecedesRetention(serviceDeliveryCfg, retentionCfg); err != nil {
		return RimskyConfig{}, fmt.Errorf("rimsky config %q: %w", path, err)
	}

	dispatchCfg, err := parseDispatchDefaults(wrapper.DispatchDefaults)
	if err != nil {
		return RimskyConfig{}, fmt.Errorf("rimsky config %q: %w", path, err)
	}

	unreachableValidatorPolicy, err := ParseUnreachableValidatorPolicy(wrapper.UnreachableValidatorPolicy)
	if err != nil {
		return RimskyConfig{}, fmt.Errorf("rimsky config %q: %w", path, err)
	}

	observabilityRefreshInterval, err := parseObservabilityRefreshInterval()
	if err != nil {
		return RimskyConfig{}, fmt.Errorf("rimsky config %q: %w", path, err)
	}

	supervisorSection := SupervisorSection{}
	if sup := wrapper.Supervisor; sup != nil {
		supervisorSection = SupervisorSection{
			SupervisorID:        sup.SupervisorID,
			Concurrency:         sup.Concurrency,
			LivenessIntervalMs:  sup.LivenessIntervalMs,
			ClaimPollIntervalMs: sup.ClaimPollIntervalMs,
			Callback: SupervisorCallbackSection{
				Host:          sup.Callback.Host,
				Port:          sup.Callback.Port,
				AdvertiseHost: sup.Callback.AdvertiseHost,
				AdvertisePort: sup.Callback.AdvertisePort,
			},
		}
	}

	return RimskyConfig{
		Persistence:                  pcfg,
		ClaimProducers:               producers,
		NamedLocks:                   locks.NamedLocksConfig{Locks: wrapper.NamedLocks},
		Executors:                    executors,
		Publishers:                   publishersCfg,
		Validators:                   validatorsCfg,
		DataProcessors:               dataProcessorsCfg,
		Retention:                    retentionCfg,
		ServiceDelivery:              serviceDeliveryCfg,
		Dispatch:                     dispatchCfg,
		Supervisor:                   supervisorSection,
		LateBindServiceProxies:       wrapper.LateBindServiceProxies,
		ServiceAuth:                  serviceAuth,
		Topology:                     topology,
		Warnings:                     warnings,
		UnreachableValidatorPolicy:   unreachableValidatorPolicy,
		ObservabilityRefreshInterval: observabilityRefreshInterval,
	}, nil
}

// @decision: service-delivery-stall-signal
func checkStallSignalPrecedesRetention(sd ServiceDeliveryConfig, ret runtime.RetentionConfig) error {
	if ret.LifecycleOutboxTrailing <= 0 || ret.LifecycleOutboxTrailing > sd.StallAfter {
		return nil
	}
	return fmt.Errorf(
		"retention.lifecycle_outbox_trailing (%s) must be longer than service_delivery.stall_after (%s), "+
			"or the window discards an undelivered event before the stall signal reports it",
		ret.LifecycleOutboxTrailing, sd.StallAfter)
}

// @decision: service-delivery-stall-signal
func parseServiceDelivery(in *yamlServiceDelivery) (ServiceDeliveryConfig, error) {
	out := ServiceDeliveryConfig{StallAfter: runtime.DefaultServiceDeliveryStallAfter}
	if in == nil || in.StallAfter == nil {
		return out, nil
	}
	if *in.StallAfter <= 0 {
		return ServiceDeliveryConfig{}, fmt.Errorf("service_delivery.stall_after must be positive")
	}
	out.StallAfter = *in.StallAfter
	return out, nil
}

func parseRetention(in *yamlRetention) (runtime.RetentionConfig, error) {
	out := runtime.RetentionConfig{
		RecentFramesKept:             defaultRetentionRecentFramesKept,
		TraceTrailing:                defaultRetentionTraceTrailing,
		LineageTrailing:              defaultRetentionLineageTrailing,
		ClaimHandlesTrailing:         defaultRetentionClaimHandlesTrailing,
		MessageIdempotenciesTrailing: defaultRetentionMessageIdempotenciesTrailing,
		LifecycleOutboxTrailing:      defaultRetentionLifecycleOutboxTrailing,
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
	// @decision: lifecycle-subscriber-at-least-once-delivery
	if in.LifecycleOutboxTrailing != nil {
		if *in.LifecycleOutboxTrailing < 0 {
			return runtime.RetentionConfig{}, fmt.Errorf("retention.lifecycle_outbox_trailing must be non-negative")
		}
		out.LifecycleOutboxTrailing = *in.LifecycleOutboxTrailing
	}
	return out, nil
}

// @decision: three-dispatch-deadlines
func parseDispatchDefaults(in *yamlDispatchDefaults) (DispatchDefaultsConfig, error) {
	var out DispatchDefaultsConfig
	if in == nil {
		return out, nil
	}
	if in.SyncRPCDeadline != nil {
		if *in.SyncRPCDeadline < 0 {
			return DispatchDefaultsConfig{}, fmt.Errorf("dispatch_defaults.sync_rpc_deadline must be non-negative")
		}
		out.SyncRPCDeadlineDefault = *in.SyncRPCDeadline
	}
	if in.MaxQuietPeriod != nil {
		if *in.MaxQuietPeriod < 0 {
			return DispatchDefaultsConfig{}, fmt.Errorf("dispatch_defaults.max_quiet_period must be non-negative")
		}
		if *in.MaxQuietPeriod > 0 && *in.MaxQuietPeriod < time.Second {
			return DispatchDefaultsConfig{}, fmt.Errorf("dispatch_defaults.max_quiet_period %s is below the one-second dispatch-deadline resolution: use 0 to disable or >= 1s", *in.MaxQuietPeriod)
		}
		out.MaxQuietPeriodDefault = *in.MaxQuietPeriod
	}
	if in.MaxRuntime != nil {
		if *in.MaxRuntime < 0 {
			return DispatchDefaultsConfig{}, fmt.Errorf("dispatch_defaults.max_runtime must be non-negative")
		}
		if *in.MaxRuntime > 0 && *in.MaxRuntime < time.Second {
			return DispatchDefaultsConfig{}, fmt.Errorf("dispatch_defaults.max_runtime %s is below the one-second dispatch-deadline resolution: use 0 to disable or >= 1s", *in.MaxRuntime)
		}
		out.MaxRuntimeDefault = *in.MaxRuntime
	}
	// @decision: dispatch-defaults-cover-every-node-timing-key
	if in.MaxRetries != nil {
		if *in.MaxRetries < 0 {
			return DispatchDefaultsConfig{}, fmt.Errorf("dispatch_defaults.max_retries must be non-negative")
		}
		out.MaxRetriesDefault = *in.MaxRetries
	}
	if in.RetryBackoff != nil {
		if err := validateRetryBackoffDefault(*in.RetryBackoff); err != nil {
			return DispatchDefaultsConfig{}, err
		}
		backoff := *in.RetryBackoff
		out.RetryBackoffDefault = &backoff
	}
	return out, nil
}

func validateRetryBackoffDefault(b spec.RetryBackoffConfig) error {
	switch b.Kind {
	case "", spec.BackoffLinear, spec.BackoffExponential:
	default:
		return fmt.Errorf("dispatch_defaults.retry_backoff.kind %q invalid (want %q or %q)", b.Kind, spec.BackoffLinear, spec.BackoffExponential)
	}
	switch b.Jitter {
	case "", spec.JitterNone, spec.JitterPlusMinus:
	default:
		return fmt.Errorf("dispatch_defaults.retry_backoff.jitter %q invalid (want %q or %q)", b.Jitter, spec.JitterNone, spec.JitterPlusMinus)
	}
	if b.BaseDelayMs < 0 {
		return fmt.Errorf("dispatch_defaults.retry_backoff.base_delay_ms must not be negative, got %d", b.BaseDelayMs)
	}
	if b.MaxDelayMs < 0 {
		return fmt.Errorf("dispatch_defaults.retry_backoff.max_delay_ms must not be negative, got %d", b.MaxDelayMs)
	}
	if b.MaxDelayMs > 0 && b.BaseDelayMs > 0 && b.MaxDelayMs < b.BaseDelayMs {
		return fmt.Errorf("dispatch_defaults.retry_backoff.max_delay_ms %d is below base_delay_ms %d", b.MaxDelayMs, b.BaseDelayMs)
	}
	if b.BaseDelayMs == 0 && (b.Kind != "" || b.Jitter != "" || b.MaxDelayMs > 0) {
		return fmt.Errorf("dispatch_defaults.retry_backoff.base_delay_ms must be positive when a backoff is configured")
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

// @decision: tls-mode-validation
// @decision: service-auth-mtls
func parseTLSMode(block, name, raw, defaultMode string) (string, error) {
	switch raw {
	case "":
		return defaultMode, nil
	case service.TLSModeOff:
		return service.TLSModeOff, nil
	case service.TLSModeRequired:
		return service.TLSModeRequired, nil
	default:
		return "", fmt.Errorf("%s[%q]: tls: unknown value %q (one of: off, required)", block, name, raw)
	}
}

// @decision: service-auth-mtls
// @story: service-auth-mtls-mutual
func defaultServiceTLSMode(serviceAuth string) string {
	if serviceAuth == service.ServiceAuthMTLS {
		return service.TLSModeRequired
	}
	return service.TLSModeOff
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
			return fmt.Errorf("service %q: unknown protocol %q", name, p)
		}
	}
	return nil
}

func dialRemoteClaimProducers(
	ctx context.Context,
	cfg RemoteClaimProducersConfig,
	persist persistence.Tables,
	lateBindServiceProxies map[string]string,
) (*locks.Registry, error) {
	lookupBindings := func(ctx context.Context, instanceID string, tx persistence.Tx) (map[string]json.RawMessage, bool, error) {
		return LookupInstanceBindings(ctx, persist, instanceID, tx)
	}
	reg := locks.NewRegistry(
		locks.WithLookupInstanceBindings(lookupBindings),
		locks.WithLateBindServiceProxies(lateBindServiceProxies),
		locks.WithAddressBookResolution(addressBookStoreLookup(persist), addressBookStoreDialer(), 0, nil),
	)
	for name, entry := range cfg.ClaimProducers {
		if err := validateClaimProducerEntry(name, entry); err != nil {
			reg.Close()
			return nil, fmt.Errorf("dialRemoteClaimProducers: %w", err)
		}
		dialCtx, cancel := context.WithTimeout(ctx, capabilitiesHandshakeTimeout)
		client, err := service.Dial(dialCtx, name, entry.Endpoint, entry.TLS)
		cancel()
		if err != nil {
			reg.Close()
			return nil, fmt.Errorf("dialRemoteClaimProducers: producer %q: %w", name, err)
		}
		if err := client.ValidateCapabilities(entry.Capabilities); err != nil {
			client.Close()
			reg.Close()
			return nil, fmt.Errorf("dialRemoteClaimProducers: %w", err)
		}
		reg.Add(name, client)
	}
	return reg, nil
}

func LookupInstanceBindings(ctx context.Context, persist persistence.Tables, instanceID string, tx persistence.Tx) (map[string]json.RawMessage, bool, error) {
	instID, err := uuid.Parse(instanceID)
	if err != nil {
		return nil, false, err
	}
	var row *persistence.InstanceRow
	if tx != nil {
		r, err := persist.Instances().Get(ctx, instID, tx)
		if err != nil {
			return nil, false, err
		}
		row = r
	} else if err := persist.Transaction(ctx, func(ctx context.Context, itx persistence.Tx) error {
		r, err := persist.Instances().Get(ctx, instID, itx)
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

// @concept: lifecycle-subscriber
func DialLifecycleSubscribers(ctx context.Context, producers RemoteClaimProducersConfig, execs ExecutorsConfig, publishers RemotePublishersConfig) (*lifecycle.Registry, error) {
	reg := lifecycle.NewRegistry()
	for name, entry := range producers.ClaimProducers {
		if !entry.HasProtocol(claimproducer.ProtocolLifecycleSubscriber) {
			continue
		}
		dialCtx, cancel := context.WithTimeout(ctx, capabilitiesHandshakeTimeout)
		client, err := service.DialLifecycle(dialCtx, name, entry.Endpoint, entry.TLS)
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
		client, err := service.DialLifecycle(dialCtx, name, entry.Endpoint, entry.TLS)
		cancel()
		if err != nil {
			reg.Close()
			return nil, fmt.Errorf("DialLifecycleSubscribers: executor %q: %w", name, err)
		}
		reg.Add(name, client)
	}
	for name, entry := range publishers.Publishers {
		if !entry.HasProtocol(claimproducer.ProtocolLifecycleSubscriber) {
			continue
		}
		dialCtx, cancel := context.WithTimeout(ctx, capabilitiesHandshakeTimeout)
		client, err := service.DialLifecycle(dialCtx, name, entry.Endpoint, entry.TLS)
		cancel()
		if err != nil {
			reg.Close()
			return nil, fmt.Errorf("DialLifecycleSubscribers: publisher %q: %w", name, err)
		}
		reg.Add(name, client)
	}
	return reg, nil
}

func validateClaimProducerEntry(name string, entry ClaimProducerEntry) error {
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

// @concept: validation
func validatorRoleDiscriminators(protocols []string) []string {
	roles := make([]string, 0, len(protocols))
	for _, p := range protocols {
		if p == claimproducer.ProtocolValidation {
			continue
		}
		roles = append(roles, p)
	}
	return roles
}
