// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package config

import (
	"context"
	"fmt"
	"time"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/locks"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	"github.com/rimsky-ai/rimsky-core/lib/graph/node"
	"github.com/rimsky-ai/rimsky-core/lib/runtime"
	"github.com/rimsky-ai/rimsky-core/lib/runtime/executor"
)

type SupervisorConfig struct {
	SupervisorID string
	Driver            persistence.Database
	Clock             shared.Clock
	Logger            shared.Logger
	Concurrency       int
	LivenessInterval  time.Duration
	ClaimPollInterval time.Duration
	Resolver          executor.Resolver
	Stores RemoteStoresConfig
	NamedLocks   locks.NamedLocksConfig
	CallbackHost string
	CallbackPort int
	CallbackAdvertiseHost string
	CallbackAdvertisePort int

	Blob persistence.BlobBackend
	BlobSpillThreshold int
	// @concept: attribute
	ExpectedAttributesSchemaFor func(executorName string) (schema []byte, ok bool)
	Metrics runtime.MetricsHook
	MaxParkDuration map[string]time.Duration

	LifecyclePeersForSpec func(tplSpec node.TemplateSpec) []string

	LifecycleSubs *locks.LifecycleRegistry

	Executors ExecutorsConfig

	LateBindServiceProxies map[string]string

	// TD-inproc-registry. @concept: executor
	ExtraInprocHandlers map[string]executor.InProcessHandler
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
	registry, err := dialRemoteStores(context.Background(), cfg.Stores, persistStore, cfg.LateBindServiceProxies)
	if err != nil {
		return nil, fmt.Errorf("StartSupervisor: %w", err)
	}
	lifecycleSubs, err := DialLifecycleSubscribers(context.Background(), cfg.Stores, cfg.Executors)
	if err != nil {
		registry.Close()
		return nil, fmt.Errorf("StartSupervisor: dial lifecycle subscribers: %w", err)
	}
	_, _, dataProcessors, dpClosers, err := DialPublisherAndValidationRegistries(
		context.Background(), cfg.Stores, cfg.Executors, RemotePublishersConfig{})
	if err != nil {
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
		registry.Close()
		lifecycleSubs.Close()
		closeDataProcessors()
		return nil, fmt.Errorf("StartSupervisor: %w", err)
	}
	persistQueue := cfg.Driver.Queue()
	if persistQueue == nil {
		registry.Close()
		lifecycleSubs.Close()
		closeDataProcessors()
		return nil, fmt.Errorf("StartSupervisor: Driver.Queue() returned nil")
	}
	coordinator := cfg.Driver.AdvisoryLocker()
	if coordinator == nil {
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
		MaxParkDuration:             cfg.MaxParkDuration,
		LifecycleSubs:               lifecycleSubs,
		LifecyclePeersForSpec:       cfg.LifecyclePeersForSpec,
		LateBindServiceProxies:      cfg.LateBindServiceProxies,
		DataProcessors:              dataProcessors,
		ExtraInprocHandlers:         cfg.ExtraInprocHandlers,
	})
	if err != nil {
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
	}, nil
}

type supervisorHandleWithRegistry struct {
	inner               SupervisorHandle
	registry            *locks.Registry
	lifecycleSubs       *locks.LifecycleRegistry
	closeDataProcessors func()
}

func (h supervisorHandleWithRegistry) Shutdown(ctx context.Context) error {
	err := h.inner.Shutdown(ctx)
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
