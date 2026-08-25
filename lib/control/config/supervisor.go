// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

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
	"github.com/rimsky-ai/rimsky-core/lib/foundation/spec"
	"github.com/rimsky-ai/rimsky-core/lib/runtime"
	"github.com/rimsky-ai/rimsky-core/lib/runtime/executor"
	"github.com/rimsky-ai/rimsky-core/lib/runtime/service"
)

// @concept: rimsky-yml
// @decision: launch-config-injection
type SupervisorSection struct {
	SupervisorID        string
	Concurrency         int
	LivenessIntervalMs  int
	ClaimPollIntervalMs int
	Callback            SupervisorCallbackSection
}

// @concept: rimsky-yml
type SupervisorCallbackSection struct {
	Host          string
	Port          *int
	AdvertiseHost string
	AdvertisePort int
}

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
	// @decision: dispatch-defaults-cover-every-node-timing-key
	MaxRetriesDefault     int
	RetryBackoffDefault   *spec.RetryBackoffConfig
	Resolver              executor.Resolver
	ClaimProducers        RemoteClaimProducersConfig
	Publishers            RemotePublishersConfig
	NamedLocks            locks.NamedLocksConfig
	CallbackHost          string
	CallbackPort          int
	CallbackAdvertiseHost string
	CallbackAdvertisePort int

	// @concept: attribute
	ExpectedAttributesSchemaFor func(executorName string) (schema []byte, ok bool)
	Metrics                     runtime.MetricsHook

	// @decision: lifecycle-drain-per-role
	SharedLifecycleDrain *runtime.LifecycleReconciler

	Executors ExecutorsConfig

	Validators     RemoteValidatorsConfig
	DataProcessors RemoteDataProcessorsConfig

	LateBindServiceProxies map[string]string

	ExtraInprocHandlers map[string]executor.InProcessHandler

	Bundled *BundledRegistrations

	ServiceAuth string

	// @decision: service-delivery-stall-signal
	ServiceDeliveryStallAfter time.Duration
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
		serverIdentity *service.IdentityHolder
		clientCAs      *x509.CertPool
		stopIdentity   = func() {}
	)
	if cfg.ServiceAuth == service.ServiceAuthMTLS {
		holder, ca, cancel, err := installServiceIdentity(context.Background(), persistStore, cfg.SupervisorID, cfg.Clock, cfg.Logger)
		if err != nil {
			return nil, fmt.Errorf("StartSupervisor: %w", err)
		}
		serverIdentity = holder
		clientCAs = ca.CertPool()
		stopIdentity = cancel
	}

	// @concept: service-address-book
	registry := newAddressBookProducerRegistry(persistStore, cfg.LateBindServiceProxies)
	if cfg.Bundled != nil {
		logger := cfg.Logger
		if logger == nil {
			logger = shared.SilentLogger{}
		}
		mergeBundledClaimProducers(registry, cfg.Bundled.ClaimProducerClients(), func(name string) {
			logger.Info("BUNDLED.CLAIMPRODUCER.OVERRIDDEN", "producer", name)
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
	// @decision: lifecycle-drain-per-role
	lifecycleDrain := cfg.SharedLifecycleDrain
	var lifecycleSubs *lifecycle.Registry
	if lifecycleDrain == nil {
		subs, err := DialLifecycleSubscribers(context.Background(), cfg.ClaimProducers, cfg.Executors, cfg.Publishers)
		if err != nil {
			stopIdentity()
			registry.Close()
			return nil, fmt.Errorf("StartSupervisor: dial lifecycle subscribers: %w", err)
		}
		lifecycleSubs = subs
		lifecycleDrain = runtime.NewLifecycleReconciler(runtime.LifecycleReconcilerConfig{
			Persist:        persistStore,
			AdvisoryLocker: cfg.Driver.AdvisoryLocker(),
			Subscribers:    lifecycleSubs,
			Clock:          cfg.Clock,
			Logger:         cfg.Logger,
			StallAfter:     cfg.ServiceDeliveryStallAfter,
		})
	}
	_, _, dataProcessors, dpClosers, err := DialPublisherAndValidationRegistries(
		context.Background(), cfg.ClaimProducers, cfg.Executors, RemotePublishersConfig{}, cfg.Validators, cfg.DataProcessors)
	if err != nil {
		stopIdentity()
		registry.Close()
		closeLifecycleSubs(lifecycleSubs)
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
		closeLifecycleSubs(lifecycleSubs)
		closeDataProcessors()
		return nil, fmt.Errorf("StartSupervisor: %w", err)
	}
	persistQueue := cfg.Driver.Queue()
	if persistQueue == nil {
		stopIdentity()
		registry.Close()
		closeLifecycleSubs(lifecycleSubs)
		closeDataProcessors()
		return nil, fmt.Errorf("StartSupervisor: Driver.Queue() returned nil")
	}
	coordinator := cfg.Driver.AdvisoryLocker()
	if coordinator == nil {
		stopIdentity()
		registry.Close()
		closeLifecycleSubs(lifecycleSubs)
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
		MaxRetriesDefault:           cfg.MaxRetriesDefault,
		RetryBackoffDefault:         cfg.RetryBackoffDefault,
		Resolver:                    cfg.Resolver,
		ClaimProducerRegistry:       registry,
		NamedLocks:                  cfg.NamedLocks,
		CallbackHost:                cfg.CallbackHost,
		CallbackPort:                cfg.CallbackPort,
		CallbackAdvertiseHost:       cfg.CallbackAdvertiseHost,
		CallbackAdvertisePort:       cfg.CallbackAdvertisePort,
		ExpectedAttributesSchemaFor: cfg.ExpectedAttributesSchemaFor,
		Metrics:                     cfg.Metrics,
		LateBindServiceProxies:      cfg.LateBindServiceProxies,
		LifecycleKick:               lifecycleDrain.Kick,
		ServiceDeliveryStallAfter:   cfg.ServiceDeliveryStallAfter,
		DataProcessors:              dataProcessors,
		ExtraInprocHandlers:         cfg.ExtraInprocHandlers,
		ServiceAuth:                 cfg.ServiceAuth,
		ServerIdentity:              serverIdentity,
		ClientCAs:                   clientCAs,
	})
	if err != nil {
		stopIdentity()
		registry.Close()
		closeLifecycleSubs(lifecycleSubs)
		closeDataProcessors()
		return nil, err
	}
	handle := supervisorHandleWithRegistry{
		inner:               inner,
		registry:            registry,
		lifecycleSubs:       lifecycleSubs,
		closeDataProcessors: closeDataProcessors,
		stopIdentity:        stopIdentity,
	}
	// @decision: lifecycle-drain-per-role
	if cfg.SharedLifecycleDrain == nil {
		drainCtx, drainCancel := context.WithCancel(context.Background())
		go lifecycleDrain.Run(drainCtx)
		handle.lifecycleDrain = lifecycleDrain
		handle.drainCancel = drainCancel
	}
	return handle, nil
}

func closeLifecycleSubs(subs *lifecycle.Registry) {
	if subs != nil {
		subs.Close()
	}
}

type supervisorHandleWithRegistry struct {
	inner               SupervisorHandle
	registry            *locks.Registry
	lifecycleSubs       *lifecycle.Registry
	closeDataProcessors func()
	stopIdentity        func()
	lifecycleDrain      *runtime.LifecycleReconciler
	drainCancel         context.CancelFunc
}

func (h supervisorHandleWithRegistry) Shutdown(ctx context.Context) error {
	err := h.inner.Shutdown(ctx)
	if h.lifecycleDrain != nil {
		h.lifecycleDrain.Stop()
	}
	if h.drainCancel != nil {
		h.drainCancel()
	}
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
