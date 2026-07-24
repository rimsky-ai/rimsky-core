// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package config

import (
	"context"
	"crypto/x509"
	"fmt"
	"time"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/lifecycle"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/locks"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	"github.com/rimsky-ai/rimsky-core/lib/graph/node"
	"github.com/rimsky-ai/rimsky-core/lib/runtime"
	"github.com/rimsky-ai/rimsky-core/lib/runtime/executor"
	"github.com/rimsky-ai/rimsky-core/lib/runtime/peer"
)

type SupervisorConfig struct {
	SupervisorID      string
	Driver            persistence.Database
	Clock             shared.Clock
	Logger            shared.Logger
	Concurrency       int
	LivenessInterval  time.Duration
	ClaimPollInterval time.Duration
	// @decision: three-dispatch-deadlines
	SyncRPCDeadlineDefault time.Duration
	MaxQuietPeriodDefault  time.Duration
	MaxRuntimeDefault      time.Duration
	Resolver               executor.Resolver
	ClaimProducers         RemoteClaimProducersConfig
	Publishers             RemotePublishersConfig
	NamedLocks             locks.NamedLocksConfig
	CallbackHost           string
	CallbackPort           int
	CallbackAdvertiseHost  string
	CallbackAdvertisePort  int

	Blob               persistence.BlobBackend
	BlobSpillThreshold int
	// @concept: attribute
	ExpectedAttributesSchemaFor func(executorName string) (schema []byte, ok bool)
	Metrics                     runtime.MetricsHook

	LifecyclePeersForSpec func(tplSpec node.TemplateSpec) []string

	LifecycleSubs *lifecycle.Registry

	Executors ExecutorsConfig

	Validators     RemoteValidatorsConfig
	DataProcessors RemoteDataProcessorsConfig

	LateBindServiceProxies map[string]string

	ExtraInprocHandlers map[string]executor.InProcessHandler

	Bundled *BundledRegistrations

	PeerAuth string
}

type SupervisorHandle interface {
	Shutdown(ctx context.Context) error
	CallbackAddr() string
	CallbackRegistry() *runtime.CallbackRegistry
	CallbackServeErr() <-chan error
}

func StartSupervisor(cfg SupervisorConfig) (SupervisorHandle, error) {
	if cfg.SupervisorID == "" {
		return nil, fmt.Errorf("StartSupervisor: SupervisorID required")
	}
	if cfg.Resolver == nil {
		return nil, fmt.Errorf("StartSupervisor: Resolver required")
	}
	if cfg.Driver == nil {
		return nil, fmt.Errorf("StartSupervisor: Driver required")
	}
	persistStore := cfg.Driver.Tables()
	if persistStore == nil {
		return nil, fmt.Errorf("StartSupervisor: Database.Tables() returned nil — driver did not initialize the Tables accessor")
	}

	var (
		serverIdentity *peer.IdentityHolder
		clientCAs      *x509.CertPool
		stopIdentity   = func() {}
	)
	if cfg.PeerAuth == peer.PeerAuthMTLS {
		holder, ca, cancel, err := installPeerIdentity(context.Background(), persistStore, cfg.SupervisorID, cfg.Clock, cfg.Logger)
		if err != nil {
			return nil, fmt.Errorf("StartSupervisor: %w", err)
		}
		serverIdentity = holder
		clientCAs = ca.CertPool()
		stopIdentity = cancel
	}

	registry, err := dialRemoteClaimProducers(context.Background(), cfg.ClaimProducers, persistStore, cfg.LateBindServiceProxies)
	if err != nil {
		stopIdentity()
		return nil, fmt.Errorf("StartSupervisor: %w", err)
	}
	if cfg.Bundled != nil {
		logger := cfg.Logger
		if logger == nil {
			logger = shared.SilentLogger{}
		}
		mergeBundledClaimProducers(registry, cfg.Bundled.ClaimProducerClients(), func(name string) {
			logger.Info("bundled claim producer overridden by configured endpoint", "producer", name)
		})
		merged := make(map[string]executor.InProcessHandler, len(cfg.ExtraInprocHandlers)+len(cfg.Bundled.ExecutorHandlers))
		for url, h := range cfg.ExtraInprocHandlers {
			merged[url] = h
		}
		for url, h := range cfg.Bundled.ExecutorHandlers {
			if _, exists := merged[url]; exists {
				stopIdentity()
				registry.Close()
				return nil, fmt.Errorf("StartSupervisor: bundled in-proc handler collides with extra handler %q", url)
			}
			merged[url] = h
		}
		cfg.ExtraInprocHandlers = merged
	}
	lifecycleSubs, err := DialLifecycleSubscribers(context.Background(), cfg.ClaimProducers, cfg.Executors, cfg.Publishers)
	if err != nil {
		stopIdentity()
		registry.Close()
		return nil, fmt.Errorf("StartSupervisor: dial lifecycle subscribers: %w", err)
	}
	_, _, dataProcessors, dpClosers, err := DialPublisherAndValidationRegistries(
		context.Background(), cfg.ClaimProducers, cfg.Executors, RemotePublishersConfig{}, cfg.Validators, cfg.DataProcessors)
	if err != nil {
		stopIdentity()
		registry.Close()
		lifecycleSubs.Close()
		return nil, fmt.Errorf("StartSupervisor: dial data-processing registry: %w", err)
	}
	closeDataProcessors := func() {
		for _, c := range dpClosers {
			c()
		}
	}
	if err := cfg.NamedLocks.Validate(); err != nil {
		stopIdentity()
		registry.Close()
		lifecycleSubs.Close()
		closeDataProcessors()
		return nil, fmt.Errorf("StartSupervisor: %w", err)
	}
	persistQueue := cfg.Driver.Queue()
	if persistQueue == nil {
		stopIdentity()
		registry.Close()
		lifecycleSubs.Close()
		closeDataProcessors()
		return nil, fmt.Errorf("StartSupervisor: Driver.Queue() returned nil")
	}
	coordinator := cfg.Driver.AdvisoryLocker()
	if coordinator == nil {
		stopIdentity()
		registry.Close()
		lifecycleSubs.Close()
		closeDataProcessors()
		return nil, fmt.Errorf("StartSupervisor: Driver.AdvisoryLocker() returned nil")
	}

	inner, err := runtime.Start(runtime.Config{
		SupervisorID:                cfg.SupervisorID,
		Persist:                     persistStore,
		Queue:                       persistQueue,
		AdvisoryLocker:              coordinator,
		Clock:                       cfg.Clock,
		Logger:                      cfg.Logger,
		Concurrency:                 cfg.Concurrency,
		LivenessInterval:            cfg.LivenessInterval,
		ClaimPollInterval:           cfg.ClaimPollInterval,
		SyncRPCDeadlineDefault:      cfg.SyncRPCDeadlineDefault,
		MaxQuietPeriodDefault:       cfg.MaxQuietPeriodDefault,
		MaxRuntimeDefault:           cfg.MaxRuntimeDefault,
		Resolver:                    cfg.Resolver,
		StoreRegistry:               registry,
		NamedLocks:                  cfg.NamedLocks,
		CallbackHost:                cfg.CallbackHost,
		CallbackPort:                cfg.CallbackPort,
		CallbackAdvertiseHost:       cfg.CallbackAdvertiseHost,
		CallbackAdvertisePort:       cfg.CallbackAdvertisePort,
		Blob:                        cfg.Blob,
		BlobSpillThreshold:          cfg.BlobSpillThreshold,
		ExpectedAttributesSchemaFor: cfg.ExpectedAttributesSchemaFor,
		Metrics:                     cfg.Metrics,
		LifecycleSubs:               lifecycleSubs,
		LifecyclePeersForSpec:       cfg.LifecyclePeersForSpec,
		LateBindServiceProxies:      cfg.LateBindServiceProxies,
		DataProcessors:              dataProcessors,
		ExtraInprocHandlers:         cfg.ExtraInprocHandlers,
		PeerAuth:                    cfg.PeerAuth,
		ServerIdentity:              serverIdentity,
		ClientCAs:                   clientCAs,
	})
	if err != nil {
		stopIdentity()
		registry.Close()
		lifecycleSubs.Close()
		closeDataProcessors()
		return nil, err
	}
	return supervisorHandleWithRegistry{
		inner:               inner,
		registry:            registry,
		lifecycleSubs:       lifecycleSubs,
		closeDataProcessors: closeDataProcessors,
		stopIdentity:        stopIdentity,
	}, nil
}

type supervisorHandleWithRegistry struct {
	inner               SupervisorHandle
	registry            *locks.Registry
	lifecycleSubs       *lifecycle.Registry
	closeDataProcessors func()
	stopIdentity        func()
}

func (h supervisorHandleWithRegistry) Shutdown(ctx context.Context) error {
	err := h.inner.Shutdown(ctx)
	if h.stopIdentity != nil {
		h.stopIdentity()
	}
	h.registry.Close()
	if h.lifecycleSubs != nil {
		h.lifecycleSubs.Close()
	}
	if h.closeDataProcessors != nil {
		h.closeDataProcessors()
	}
	return err
}

func (h supervisorHandleWithRegistry) CallbackAddr() string {
	return h.inner.CallbackAddr()
}

func (h supervisorHandleWithRegistry) CallbackRegistry() *runtime.CallbackRegistry {
	return h.inner.CallbackRegistry()
}

func (h supervisorHandleWithRegistry) CallbackServeErr() <-chan error {
	return h.inner.CallbackServeErr()
}
