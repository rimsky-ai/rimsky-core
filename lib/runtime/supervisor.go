// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package runtime

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strconv"
	"sync"
	"time"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/locks"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	"github.com/rimsky-ai/rimsky-core/lib/graph/node"
	"github.com/rimsky-ai/rimsky-core/lib/runtime/executor"
	"github.com/rimsky-ai/rimsky-core/lib/runtime/executor/builtin"
)

type Config struct {
	SupervisorID          string
	Persist               persistence.Tables
	Queue                 persistence.Queue
	AdvisoryLocker        persistence.AdvisoryLocker
	Clock                 shared.Clock
	Logger                shared.Logger
	Concurrency           int
	LivenessInterval      time.Duration
	ClaimPollInterval     time.Duration
	Resolver              executor.Resolver
	StoreRegistry         *locks.Registry
	NamedLocks            locks.NamedLocksConfig
	CallbackHost          string
	CallbackPort          int
	CallbackAdvertiseHost string
	CallbackAdvertisePort int

	Blob                             persistence.BlobBackend
	BlobSpillThreshold               int
	MaxRetriesWithoutProgressDefault int
	// @concept: attribute
	ExpectedAttributesSchemaFor func(executorName string) (schema []byte, ok bool)
	// @concept: node
	DeclaredTagsFor func(executorName string) (tags []string, ok bool)
	Metrics         MetricsHook
	MaxParkDuration map[string]time.Duration

	LifecycleSubs          *locks.LifecycleRegistry
	LifecyclePeersForSpec  func(tplSpec node.TemplateSpec) []string
	LateBindServiceProxies map[string]string

	// @concept: data-processing
	DataProcessors DataProcessingRegistry

	// @concept: executor
	ExtraInprocHandlers map[string]executor.InProcessHandler
}

type Handle struct {
	stop             chan struct{}
	done             chan struct{}
	addr             string
	advertisedURL    string
	callbackReg      *CallbackRegistry
	callbackServeErr <-chan error
	wg               sync.WaitGroup
}

func (h *Handle) Shutdown(ctx context.Context) error {
	select {
	case <-h.stop:
	default:
		close(h.stop)
	}
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

func Start(cfg Config) (*Handle, error) {
	if cfg.LivenessInterval == 0 {
		cfg.LivenessInterval = 5 * time.Second
	}
	if cfg.ClaimPollInterval == 0 {
		cfg.ClaimPollInterval = 1 * time.Second
	}
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
	if cfg.StoreRegistry == nil {
		return nil, errors.New("supervisor.Start: StoreRegistry is required")
	}

	lockHolders := cfg.Persist.ClaimHandles()

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
		Registry:                         callbackReg,
		Persist:                          cfg.Persist,
		Queue:                            cfg.Queue,
		AdvisoryLocker:                   cfg.AdvisoryLocker,
		ClaimHandles:                     lockHolders,
		Clock:                            cfg.Clock,
		Logger:                           cfg.Logger,
		SupervisorID:                     cfg.SupervisorID,
		Blob:                             cfg.Blob,
		BlobSpillThreshold:               cfg.BlobSpillThreshold,
		MaxRetriesWithoutProgressDefault: cfg.MaxRetriesWithoutProgressDefault,
		ExpectedAttributesSchemaFor:      cfg.ExpectedAttributesSchemaFor,
		Metrics:                          cfg.Metrics,
		LifecycleSubs:                    cfg.LifecycleSubs,
		LifecyclePeersForSpec:            cfg.LifecyclePeersForSpec,
		DataProcessors:                   cfg.DataProcessors,
	}
	addr, err := callbackSrv.Start(cfg.CallbackHost, cfg.CallbackPort)
	if err != nil {
		return nil, err
	}

	// @concept: executor
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
			cfg.Logger.Warn("supervisor: inproc executor alias seed skipped: resolver shape unrecognised",
				"alias", url,
				"resolver_type", fmt.Sprintf("%T", cfg.Resolver))
		}
	}

	for alias, endpoint := range builtin.BuiltinExecutorAliases() {
		if !seedInprocExecutorAlias(cfg.Resolver, alias, endpoint) {
			cfg.Logger.Warn("supervisor: builtin inproc executor alias seed skipped: resolver shape unrecognised",
				"alias", alias,
				"resolver_type", fmt.Sprintf("%T", cfg.Resolver))
		}
	}

	host, port := effectiveCallbackHostPort(addr, cfg.CallbackAdvertiseHost, cfg.CallbackAdvertisePort)
	accepted := cfg.Resolver.AcceptedNames()
	// @story: cascade-emit
	// @concept: message-emitter-node
	accepted = append(accepted, EmitMessageDispatchName)
	acceptedStores := storeRegistryNames(cfg.StoreRegistry)
	if err := cfg.Persist.Transaction(context.Background(), func(ctx context.Context, tx persistence.Tx) error {
		return cfg.Persist.Supervisors().Register(ctx, persistence.SupervisorRegisterInput{
			ID:                cfg.SupervisorID,
			AcceptedExecutors: accepted,
			AcceptedStores:    acceptedStores,
			Concurrency:       cfg.Concurrency,
			CallbackHost:      host,
			CallbackPort:      port,
		}, tx)
	}); err != nil {
		_ = callbackSrv.Close(context.Background())
		return nil, err
	}

	persistCap := cfg.Persist
	queueCap := cfg.Queue
	blobCap := cfg.Blob
	spillCap := cfg.BlobSpillThreshold
	loggerCap := cfg.Logger
	newHctx := executor.HandlerContextFactory(func(dispatchID, nodeID shared.UUID) executor.HandlerContext {
		return executor.HandlerContext{
			Scratch: &executor.ScratchWriter{
				Persist:        persistCap,
				Queue:          queueCap,
				Blob:           blobCap,
				SpillThreshold: spillCap,
				DispatchID:     dispatchID,
				NodeID:         nodeID,
				Logger:         loggerCap,
			},
		}
	})
	clientPool := executor.NewClientPoolWithInProcess(inprocReg, newHctx)
	advertised := advertisedCallbackURL(addr, cfg.CallbackAdvertiseHost, cfg.CallbackAdvertisePort)
	h := &Handle{stop: make(chan struct{}), done: make(chan struct{}), addr: addr, advertisedURL: advertised, callbackReg: callbackReg, callbackServeErr: callbackSrv.ServeErr()}
	go runLoop(cfg, h, callbackSrv, callbackReg, clientPool, accepted, acceptedStores, lockHolders)
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
		default:
			return false
		}
	}
}

func storeRegistryNames(reg *locks.Registry) []string {
	producers := reg.Producers()
	if len(producers) == 0 {
		return nil
	}
	out := make([]string, 0, len(producers))
	for name := range producers {
		out = append(out, name)
	}
	return out
}

func effectiveCallbackHostPort(listenerAddr, advertiseHost string, advertisePort int) (string, int) {
	bindHost, bindPort := splitHostPort(listenerAddr)
	if advertiseHost == "" {
		return bindHost, bindPort
	}
	if advertisePort == 0 {
		return advertiseHost, bindPort
	}
	return advertiseHost, advertisePort
}

func advertisedCallbackURL(listenerAddr, advertiseHost string, advertisePort int) string {
	host, port := effectiveCallbackHostPort(listenerAddr, advertiseHost, advertisePort)
	return "http://" + net.JoinHostPort(host, strconv.Itoa(port))
}

func runLoop(
	cfg Config,
	h *Handle,
	srv *CallbackServer,
	reg *CallbackRegistry,
	pool *executor.ClientPool,
	accepted []string,
	acceptedStores []string,
	lockHolders persistence.ClaimHandleTable,
) {
	defer close(h.done)
	cfg.Logger.Info("supervisor started",
		"supervisor_id", cfg.SupervisorID,
		"accepts", accepted,
		"concurrency", cfg.Concurrency)

	var activeMu sync.Mutex
	activeCount := 0

	livenessTick := time.NewTicker(cfg.LivenessInterval)
	defer livenessTick.Stop()
	claimTick := time.NewTicker(cfg.ClaimPollInterval)
	defer claimTick.Stop()

	tickLiveness := func() {
		tickCtx := context.Background()
		var running int
		if err := cfg.Persist.Transaction(tickCtx, func(ctx context.Context, tx persistence.Tx) error {
			rows, err := cfg.Persist.Nodes().ListRunning(ctx, tx)
			if err != nil {
				return err
			}
			for _, r := range rows {
				if r.AssignedSupervisorID == cfg.SupervisorID {
					running++
				}
			}
			return nil
		}); err != nil {
			cfg.Logger.Warn("supervisor: list running nodes failed", "error", err.Error())
			running = 0
		}
		if err := cfg.Persist.Transaction(tickCtx, func(ctx context.Context, tx persistence.Tx) error {
			return cfg.Persist.Supervisors().UpdateActiveNodeCount(ctx, cfg.SupervisorID, running, tx)
		}); err != nil {
			cfg.Logger.Warn("supervisor: supervisors.UpdateActiveNodeCount failed", "error", err.Error())
		}
	}

	tryClaim := func() {
		activeMu.Lock()
		if activeCount >= cfg.Concurrency {
			activeMu.Unlock()
			return
		}
		activeCount++
		activeMu.Unlock()

		ctx, cancel := context.WithCancel(context.Background())
		h.wg.Add(1)
		go func() {
			defer h.wg.Done()
			defer cancel()
			result, runErr := RunNode(ctx, RunArgs{
				Persist:                          cfg.Persist,
				Queue:                            cfg.Queue,
				AdvisoryLocker:                   cfg.AdvisoryLocker,
				ClaimHandles:                     lockHolders,
				Clock:                            cfg.Clock,
				Logger:                           cfg.Logger,
				SupervisorID:                     cfg.SupervisorID,
				AcceptedExecutors:                accepted,
				AcceptedStores:                   acceptedStores,
				Pool:                             pool,
				Resolver:                         cfg.Resolver,
				StoreRegistry:                    cfg.StoreRegistry,
				NamedLocks:                       cfg.NamedLocks,
				CallbackURL:                      h.advertisedURL,
				LivenessInterval:                 cfg.LivenessInterval,
				Blob:                             cfg.Blob,
				BlobSpillThreshold:               cfg.BlobSpillThreshold,
				MaxRetriesWithoutProgressDefault: cfg.MaxRetriesWithoutProgressDefault,
				ExpectedAttributesSchemaFor:      cfg.ExpectedAttributesSchemaFor,
				DeclaredTagsFor:                  cfg.DeclaredTagsFor,
				Metrics:                          cfg.Metrics,
				LifecycleSubs:                    cfg.LifecycleSubs,
				LifecyclePeersForSpec:            cfg.LifecyclePeersForSpec,
				LateBindServiceProxies:           cfg.LateBindServiceProxies,
				DataProcessors:                   cfg.DataProcessors,
			}, reg.Register)
			if runErr != nil {
				cfg.Logger.Warn("supervisor: RunNode failed", "error", runErr.Error())
			}

			defer func() {
				activeMu.Lock()
				activeCount--
				activeMu.Unlock()
			}()

			if result.Async {
				return
			}
			if result.Ran && result.DispatchID != (shared.UUID{}) {
				_ = cfg.Queue.Complete(context.Background(), result.DispatchID, cfg.SupervisorID)
			}
		}()
	}

	for {
		select {
		case <-h.stop:
			waitCtx, cancelWait := context.WithTimeout(context.Background(), 30*time.Second)
			waitDone := make(chan struct{})
			go func() {
				h.wg.Wait()
				close(waitDone)
			}()
			select {
			case <-waitDone:
			case <-waitCtx.Done():
				activeMu.Lock()
				remaining := activeCount
				activeMu.Unlock()
				if remaining > 0 {
					cfg.Logger.Warn("supervisor shutdown timed out with active runs",
						"active", remaining)
				}
			}
			cancelWait()
			_ = srv.Close(context.Background())
			if err := cfg.Persist.Transaction(context.Background(), func(ctx context.Context, tx persistence.Tx) error {
				return cfg.Persist.Supervisors().Unregister(ctx, cfg.SupervisorID, tx)
			}); err != nil {
				cfg.Logger.Warn("supervisor: Unregister failed", "error", err.Error())
			}
			_ = pool.Close()
			cfg.Logger.Info("supervisor stopped", "supervisor_id", cfg.SupervisorID)
			return
		case <-livenessTick.C:
			tickLiveness()
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
