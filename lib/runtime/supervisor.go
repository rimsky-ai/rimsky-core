// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package runtime

import (
	"context"
	"crypto/x509"
	"errors"
	"fmt"
	"net"
	"strconv"
	"sync"
	"time"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/locks"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	"github.com/rimsky-ai/rimsky-core/lib/runtime/executor"
	"github.com/rimsky-ai/rimsky-core/lib/runtime/executor/builtin"
	"github.com/rimsky-ai/rimsky-core/lib/runtime/service"
)

type Config struct {
	SupervisorID      string
	Persist           persistence.Tables
	Queue             persistence.Queue
	AdvisoryLocker    persistence.AdvisoryLocker
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
	ClaimProducerRegistry  *locks.Registry
	NamedLocks             locks.NamedLocksConfig
	CallbackHost           string
	CallbackPort           int
	CallbackAdvertiseHost  string
	CallbackAdvertisePort  int

	// @concept: attribute
	ExpectedAttributesSchemaFor func(executorName string) (schema []byte, ok bool)
	// @concept: node
	DeclaredTagsFor func(executorName string) (tags []string, ok bool)
	Metrics         MetricsHook

	LateBindServiceProxies map[string]string

	// @decision: lifecycle-drain-per-role
	LifecycleKick func()

	// @decision: service-delivery-stall-signal
	ServiceDeliveryStallAfter time.Duration

	// @concept: data-processing
	DataProcessors DataProcessingRegistry

	// @concept: executor
	ExtraInprocHandlers map[string]executor.InProcessHandler

	ServiceAuth    string
	ServerIdentity *service.IdentityHolder
	ClientCAs      *x509.CertPool
}

type Handle struct {
	stop             chan struct{}
	stopOnce         sync.Once
	done             chan struct{}
	addr             string
	advertisedURL    string
	callbackReg      *CallbackRegistry
	callbackServeErr <-chan error
	wg               sync.WaitGroup
}

func (h *Handle) Shutdown(ctx context.Context) error {
	h.stopOnce.Do(func() { close(h.stop) })
	select {
	case <-h.done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (h *Handle) CallbackAddr() string { return h.addr }

func (h *Handle) CallbackRegistry() *CallbackRegistry { return h.callbackReg }

func (h *Handle) CallbackServeErr() <-chan error { return h.callbackServeErr }

const defaultClaimPollInterval = 200 * time.Millisecond

func resolveClaimPollInterval(configured time.Duration) time.Duration {
	if configured == 0 {
		return defaultClaimPollInterval
	}
	return configured
}

func Start(cfg Config) (*Handle, error) {
	if cfg.LivenessInterval == 0 {
		cfg.LivenessInterval = 5 * time.Second
	}
	cfg.ClaimPollInterval = resolveClaimPollInterval(cfg.ClaimPollInterval)
	if cfg.Concurrency < 1 {
		cfg.Concurrency = 1
	}
	if cfg.Logger == nil {
		cfg.Logger = shared.SilentLogger{}
	}
	if cfg.Clock == nil {
		cfg.Clock = shared.SystemClock{}
	}
	if cfg.Persist == nil || cfg.Queue == nil || cfg.Resolver == nil {
		return nil, errors.New("supervisor.Start: Persist, Queue, and Resolver are required")
	}
	if cfg.AdvisoryLocker == nil {
		return nil, errors.New("supervisor.Start: AdvisoryLocker is required")
	}
	if cfg.ClaimProducerRegistry == nil {
		return nil, errors.New("supervisor.Start: ClaimProducerRegistry is required")
	}

	claimHandles := cfg.Persist.ClaimHandles()

	var verbDispatcher *ProducerVerbDispatcher
	if p, ok := cfg.Persist.(producerVerbOutboxProvider); ok {
		verbDispatcher = NewProducerVerbDispatcher(
			p.ProducerVerbOutbox(), cfg.Persist,
			cfg.ClaimProducerRegistry, cfg.Clock, cfg.Logger, cfg.ServiceDeliveryStallAfter)
	}

	callbackReg := NewCallbackRegistry()
	baseSchemaHook := cfg.ExpectedAttributesSchemaFor
	cfg.ExpectedAttributesSchemaFor = func(name string) ([]byte, bool) {
		if schema, ok := builtin.SchemaFor(name); ok {
			return schema, true
		}
		if baseSchemaHook != nil {
			return baseSchemaHook(name)
		}
		return nil, false
	}
	baseTagsHook := cfg.DeclaredTagsFor
	cfg.DeclaredTagsFor = func(name string) ([]string, bool) {
		if tags, ok := builtin.DeclaredTagsFor(name); ok {
			return tags, true
		}
		if baseTagsHook != nil {
			return baseTagsHook(name)
		}
		return nil, false
	}
	callbackSrv := &CallbackServer{
		Registry:                    callbackReg,
		Persist:                     cfg.Persist,
		Queue:                       cfg.Queue,
		AdvisoryLocker:              cfg.AdvisoryLocker,
		ClaimHandles:                claimHandles,
		ClaimProducerRegistry:       cfg.ClaimProducerRegistry,
		Clock:                       cfg.Clock,
		Logger:                      cfg.Logger,
		SupervisorID:                cfg.SupervisorID,
		LivenessInterval:            cfg.LivenessInterval,
		ExpectedAttributesSchemaFor: cfg.ExpectedAttributesSchemaFor,
		DeclaredTagsFor:             cfg.DeclaredTagsFor,
		Metrics:                     cfg.Metrics,
		LateBindServiceProxies:      cfg.LateBindServiceProxies,
		LifecycleKick:               cfg.LifecycleKick,
		DataProcessors:              cfg.DataProcessors,
		ServiceAuth:                 cfg.ServiceAuth,
		ServerIdentity:              cfg.ServerIdentity,
		ClientCAs:                   cfg.ClientCAs,
		ProducerVerbKick:            verbDispatcher.Kick,
	}
	addr, err := callbackSrv.Start(cfg.CallbackHost, cfg.CallbackPort)
	if err != nil {
		return nil, err
	}

	// @concept: executor
	// @decision: inproc-registry
	inprocReg := executor.NewInProcessRegistry()
	if err := builtin.RegisterAllInProcessHandlers(inprocReg); err != nil {
		_ = callbackSrv.Close(context.Background())
		return nil, fmt.Errorf("supervisor: %w", err)
	}

	// @concept: executor
	for url, h := range cfg.ExtraInprocHandlers {
		if err := inprocReg.Register(url, h); err != nil {
			_ = callbackSrv.Close(context.Background())
			return nil, fmt.Errorf("supervisor: register extra inproc handler %q: %w", url, err)
		}
		if !seedInprocExecutorAlias(cfg.Resolver, url, executor.Endpoint{Transport: "inproc", URL: url}) {
			cfg.Logger.Warn("SUPERVISOR.INPROCEXECUTORALIAS.SEEDSKIPPED", "detail", "the resolver shape is unrecognised",
				"alias", url,
				"resolver_type", fmt.Sprintf("%T", cfg.Resolver))
		}
	}

	for alias, endpoint := range builtin.BuiltinExecutorAliases() {
		if !seedInprocExecutorAlias(cfg.Resolver, alias, endpoint) {
			cfg.Logger.Warn("SUPERVISOR.BUILTININPROCEXECUTORALIAS.SEEDSKIPPED", "detail", "the resolver shape is unrecognised",
				"alias", alias,
				"resolver_type", fmt.Sprintf("%T", cfg.Resolver))
		}
	}

	host, port, err := effectiveCallbackHostPort(addr, cfg.CallbackAdvertiseHost, cfg.CallbackAdvertisePort)
	if err != nil {
		_ = callbackSrv.Close(context.Background())
		return nil, err
	}
	if err := cfg.Persist.Transaction(context.Background(), func(ctx context.Context, tx persistence.Tx) error {
		return cfg.Persist.Supervisors().Register(ctx, persistence.SupervisorRegisterInput{
			ID:           cfg.SupervisorID,
			Concurrency:  cfg.Concurrency,
			CallbackHost: host,
			CallbackPort: port,
		}, tx)
	}); err != nil {
		_ = callbackSrv.Close(context.Background())
		return nil, err
	}

	persistCap := cfg.Persist
	newHctx := executor.HandlerContextFactory(func(ctx context.Context, _, nodeID shared.UUID) executor.HandlerContext {
		hctx := executor.HandlerContext{}
		if extras, ok := executor.DispatchExtrasFromContext(ctx); ok && extras.SendMessageType != "" {
			msgType := extras.SendMessageType
			instanceID := extras.InstanceID
			frameID := extras.FrameID
			hctx.SendCascadeMessage = func(ctx context.Context, body []byte) (shared.UUID, bool, error) {
				return sendCascadeMessage(ctx, persistCap, instanceID, nodeID, frameID, msgType, body, nil)
			}
		}
		return hctx
	})
	clientPool := executor.NewClientPoolWithInProcess(inprocReg, newHctx)
	advertised := advertisedCallbackURL(host, port, cfg.ServiceAuth)
	h := &Handle{stop: make(chan struct{}), done: make(chan struct{}), addr: addr, advertisedURL: advertised, callbackReg: callbackReg, callbackServeErr: callbackSrv.ServeErr()}
	fanOutSems := NewFanOutSemaphoreRegistry()
	go runLoop(cfg, h, callbackSrv, callbackReg, clientPool, claimHandles, verbDispatcher, fanOutSems)
	return h, nil
}

// @concept: executor
func seedInprocExecutorAlias(r executor.Resolver, alias string, ep executor.Endpoint) bool {
	for {
		switch s := r.(type) {
		case *executor.StaticResolver:
			s.Register(alias, ep)
			return true
		case *executor.LateBindResolver:
			r = s.Unwrap()
			if r == nil {
				return false
			}
		case *executor.AddressBookResolver:
			r = s.Unwrap()
			if r == nil {
				return false
			}
		default:
			return false
		}
	}
}

func isWildcardHost(host string) bool {
	switch host {
	case "", "0.0.0.0", "::", "[::]":
		return true
	}
	return false
}

// @concept: supervisor
func effectiveCallbackHostPort(listenerAddr, advertiseHost string, advertisePort int) (string, int, error) {
	bindHost, bindPort := splitHostPort(listenerAddr)
	if advertiseHost == "" {
		if isWildcardHost(bindHost) {
			return "", 0, fmt.Errorf(
				"supervisor: callback advertise host is unset while the callback listener binds the wildcard address %q — refusing to stamp an unreachable callback URL; set callback.advertise_host (or RIMSKY_SUPERVISOR_CALLBACK_ADVERTISE_HOST) to a hostname executors can reach this supervisor at",
				bindHost)
		}
		return bindHost, bindPort, nil
	}
	if advertisePort == 0 {
		return advertiseHost, bindPort, nil
	}
	return advertiseHost, advertisePort, nil
}

func advertisedCallbackURL(host string, port int, serviceAuth string) string {
	scheme := "http://"
	if serviceAuth == service.ServiceAuthMTLS {
		scheme = "https://"
	}
	return scheme + net.JoinHostPort(host, strconv.Itoa(port))
}

func runLoop(
	cfg Config,
	h *Handle,
	srv *CallbackServer,
	reg *CallbackRegistry,
	pool *executor.ClientPool,
	claimHandles persistence.ClaimHandleTable,
	verbDispatcher *ProducerVerbDispatcher,
	fanOutSems *FanOutSemaphoreRegistry,
) {
	defer close(h.done)
	cfg.Logger.Info("SUPERVISOR.LOOP.STARTED",
		"supervisor_id", cfg.SupervisorID,
		"concurrency", cfg.Concurrency)

	dispatchCtx, cancelDispatch := context.WithCancel(context.Background())
	defer cancelDispatch()
	if verbDispatcher != nil {
		h.wg.Add(1)
		go func() {
			defer h.wg.Done()
			verbDispatcher.Run(dispatchCtx)
		}()
	}

	gate := newConcurrencyGate(cfg.Concurrency)

	claimTick := time.NewTicker(cfg.ClaimPollInterval)
	defer claimTick.Stop()

	tryClaim := func() {
		if !gate.tryAcquire() {
			return
		}

		ctx, cancel := context.WithCancel(context.Background())
		h.wg.Add(1)
		go func() {
			defer h.wg.Done()
			defer cancel()
			defer gate.release()
			result, runErr := RunNode(ctx, RunArgs{
				Persist:                     cfg.Persist,
				Queue:                       cfg.Queue,
				AdvisoryLocker:              cfg.AdvisoryLocker,
				ClaimHandles:                claimHandles,
				Clock:                       cfg.Clock,
				Logger:                      cfg.Logger,
				SupervisorID:                cfg.SupervisorID,
				Pool:                        pool,
				Resolver:                    cfg.Resolver,
				ClaimProducerRegistry:       cfg.ClaimProducerRegistry,
				NamedLocks:                  cfg.NamedLocks,
				CallbackURL:                 h.advertisedURL,
				LivenessInterval:            cfg.LivenessInterval,
				SyncRPCDeadlineDefault:      cfg.SyncRPCDeadlineDefault,
				MaxQuietPeriodDefault:       cfg.MaxQuietPeriodDefault,
				MaxRuntimeDefault:           cfg.MaxRuntimeDefault,
				ExpectedAttributesSchemaFor: cfg.ExpectedAttributesSchemaFor,
				DeclaredTagsFor:             cfg.DeclaredTagsFor,
				Metrics:                     cfg.Metrics,
				LateBindServiceProxies:      cfg.LateBindServiceProxies,
				LifecycleKick:               cfg.LifecycleKick,
				DataProcessors:              cfg.DataProcessors,
				ProducerVerbKick:            verbDispatcher.Kick,
				FanOutSemaphores:            fanOutSems,
			}, reg.Register)
			if runErr != nil {
				cfg.Logger.Warn("SUPERVISOR.RUNNODE.FAILED", "error", runErr.Error())
			}

			if result.Async {
				return
			}
			if result.Ran && result.NodeRunID != (shared.UUID{}) {
				if err := cfg.Queue.Complete(context.Background(), result.NodeRunID, cfg.SupervisorID); err != nil {
					cfg.Logger.Warn("SUPERVISOR.QUEUECOMPLETE.FAILED",
						"dispatch_id", result.NodeRunID.String(), "error", err.Error())
				}
			}
		}()
	}

	for {
		select {
		case <-h.stop:
			cancelDispatch()
			waitCtx, cancelWait := context.WithTimeout(context.Background(), 30*time.Second)
			waitDone := make(chan struct{})
			go func() {
				h.wg.Wait()
				close(waitDone)
			}()
			select {
			case <-waitDone:
			case <-waitCtx.Done():
				remaining := gate.activeCount()
				if remaining > 0 {
					cfg.Logger.Warn("SUPERVISOR.SHUTDOWN.TIMEDOUT", "detail", "runs were still active",
						"active", remaining)
				}
			}
			cancelWait()
			_ = srv.Close(context.Background())
			if err := cfg.Persist.Transaction(context.Background(), func(ctx context.Context, tx persistence.Tx) error {
				return cfg.Persist.Supervisors().Unregister(ctx, cfg.SupervisorID, tx)
			}); err != nil {
				cfg.Logger.Warn("SUPERVISOR.UNREGISTER.FAILED", "error", err.Error())
			}
			_ = pool.Close()
			cfg.Logger.Info("SUPERVISOR.LOOP.STOPPED", "supervisor_id", cfg.SupervisorID)
			return
		case <-claimTick.C:
			tryClaim()
		}
	}
}

func splitHostPort(addr string) (string, int) {
	host, portStr, err := net.SplitHostPort(addr)
	if err != nil {
		return "", 0
	}
	p, _ := strconv.Atoi(portStr)
	return host, p
}
