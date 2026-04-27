// §19.1 — atomic acquisition: a forced Store.AcquireLock failure rolls
// back the entire §13.3 acquisition transaction.
//
// Verifies blessed invariant 10 (spec §18 invariant 10): "Lock
// acquisition is atomic with dispatch claim. The transaction in §13.3
// step 3 either claims dispatch and inserts all required
// rimsky_lock_holders rows AND completes all store AcquireLock
// mutations, or none of these."
//
// Mechanism: register a custom store factory whose AcquireLock returns
// an error unconditionally for region specs. Run RunNode against a node
// that requires that store. The runner reaches step 3e, calls
// Store.AcquireLock, gets an error. tryAcquireWithTx rolls back the tx.
// Assert:
//   - the dispatch row remains unclaimed (claimed_by IS NULL),
//   - no rimsky_lock_holders rows exist for the node,
//   - the runner returned Ran=false.
//
// The failing store delegates UnmarshalRegion / RegionsConflict /
// LockEligible / OpenHandle / Commit / ReleaseLock to the embedded stub
// filesystem store so the rest of the runner code path is identical to
// production region-lock behaviour. Only AcquireLock is overridden.
package locks

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/fallguy/rimsky/core/executor"
	"github.com/fallguy/rimsky/core/node"
	"github.com/fallguy/rimsky/core/scenario"
	"github.com/fallguy/rimsky/core/shared"
	"github.com/fallguy/rimsky/core/store"
	"github.com/fallguy/rimsky/core/store/stub"
	"github.com/fallguy/rimsky/core/supervisor"
)

// failingFactoryKind is the registry kind used by failingFactory. We
// register under a unique kind so the harness's default
// stub/filesystem registrations don't shadow this factory; the built
// store's Kind() return masquerades as "filesystem" so the template
// validator's region-on-filesystem rule accepts the node.
const failingFactoryKind = "failing_filesystem"

// storeKindFilesystem mirrors core/node/template_validator.go's private
// constant. Duplicated here so failingStore.Kind() can satisfy the
// validator's region-on-filesystem rule without importing an internal
// package.
const storeKindFilesystem = "filesystem"

// failingStore wraps a stub filesystem Store with a forced AcquireLock
// failure. The other Store methods are delegated to the inner stub so
// the rest of the runner code path (eligibility, region-conflict
// re-eval, OpenHandle / Commit / ReleaseLock) behaves identically to
// production region-lock semantics.
//
// Kind() returns "filesystem" so the template validator (which gates
// write-regions on storeKindFilesystem) accepts a node declaring a
// write region against this store.
type failingStore struct {
	inner store.Store
	name  string
}

func (s *failingStore) Kind() string                     { return storeKindFilesystem }
func (s *failingStore) Name() string                     { return s.name }
func (s *failingStore) Capabilities() store.Capabilities { return s.inner.Capabilities() }

func (s *failingStore) LockEligible(ctx context.Context, spec store.LockSpec) (bool, error) {
	return s.inner.LockEligible(ctx, spec)
}

func (s *failingStore) RegionsConflict(a, b any) bool { return s.inner.RegionsConflict(a, b) }

func (s *failingStore) UnmarshalRegion(raw []byte) (any, error) {
	return s.inner.UnmarshalRegion(raw)
}

func (s *failingStore) AcquireLock(_ context.Context, _ store.LockSpec) (store.LockHandle, store.ClaimResult, error) {
	return store.LockHandle{}, store.ClaimResult{},
		errors.New("failingStore: AcquireLock forced failure")
}

func (s *failingStore) OpenHandle(ctx context.Context, lh store.LockHandle, resumed bool) (store.NativeHandle, error) {
	return s.inner.OpenHandle(ctx, lh, resumed)
}

func (s *failingStore) Commit(ctx context.Context, lh store.LockHandle) (store.CommitResult, error) {
	return s.inner.Commit(ctx, lh)
}

func (s *failingStore) ReleaseLock(ctx context.Context, lh store.LockHandle, action store.ReleaseAction) error {
	return s.inner.ReleaseLock(ctx, lh, action)
}

// failingFactory builds *failingStore instances by composing a stub
// filesystem store as the inner. The cfg map is passed through to the
// inner factory unchanged.
type failingFactory struct{}

func (failingFactory) Kind() string { return failingFactoryKind }

func (failingFactory) Build(name string, cfg map[string]any) (store.Store, error) {
	inner, err := stub.FilesystemFactory().Build(name, cfg)
	if err != nil {
		return nil, fmt.Errorf("failingFactory: build inner: %w", err)
	}
	return &failingStore{inner: inner, name: name}, nil
}

func TestLockAtomicAcquisition(t *testing.T) {
	t.Parallel()
	h := scenario.Start(t, scenario.HarnessOpts{
		NoSupervisor:        true,
		NoScheduler:         true,
		ExtraStoreFactories: []store.Factory{failingFactory{}},
		StoresConfig: store.StoresConfig{
			Stores: map[string]map[string]any{
				"failing": {"kind": failingFactoryKind},
			},
		},
	})

	h.Stub.WhenType("a").Complete(map[string]any{}, true, "ok")

	tid := h.DeployTemplate(node.TemplateSpec{
		Name: "atomic-acq", Version: "1",
		Nodes: []node.TemplateNodeDef{
			scenario.MakeNode(
				node.TemplateNodeDef{Type: "a", Executor: "stub"},
				scenario.WithStores(scenario.RegionRef("failing", "x.md")),
			),
		},
	})
	iid := h.CreateInstance(tid, "ck-atomic", map[string]any{})

	a := h.FindNode(iid, "a")
	require.NotNil(t, a)

	// Force the dispatch row eligible.
	_, err := h.Pool.Exec(h.Ctx,
		`UPDATE rimsky_dispatch
		    SET executor_name = 'stub',
		        required_stores = '{"failing"}',
		        claimed_by = NULL,
		        claimed_at = NULL,
		        last_heartbeat_at = NULL,
		        enqueued_at = NOW() - INTERVAL '5 seconds'
		  WHERE node_id = $1`,
		a.ID,
	)
	require.NoError(t, err)

	pool := executor.NewClientPool()
	t.Cleanup(func() { _ = pool.Close() })

	args := supervisor.RunArgs{
		Storage:           h.Storage,
		Queue:             h.Queue,
		QueuePool:         h.Pool,
		LockHolders:       store.NewLockHoldersClient(h.Pool),
		StoreRegistry:     h.Stores,
		Clock:             shared.SystemClock{},
		Logger:            shared.SilentLogger{},
		SupervisorID:      "scenario-runner",
		AcceptedExecutors: []string{"stub"},
		AcceptedStores:    []string{"failing"},
		Pool:              pool,
		Resolver: executor.NewStaticResolver(map[string]executor.Endpoint{
			"stub": {Transport: "grpc", URL: h.StubAddr},
		}),
		HeartbeatInterval: 100 * time.Millisecond,
	}

	// AcquireLock errors during step 3e → tryAcquireWithTx rolls back.
	// The runner surfaces the wrapped error to the caller as (Ran=false,
	// err) per runner_acquire.go's tryAcquire contract.
	out, err := supervisor.RunNode(h.Ctx, args, nil)
	require.Error(t, err, "AcquireLock failure should surface as a runner error")
	require.False(t, out.Ran)

	// Atomicity assertions follow:
	//
	// (a) the dispatch row is still unclaimed (rolled back).
	var claimedBy *string
	require.NoError(t, h.Pool.QueryRow(h.Ctx,
		`SELECT claimed_by FROM rimsky_dispatch WHERE node_id = $1`, a.ID,
	).Scan(&claimedBy))
	require.Nil(t, claimedBy, "AcquireLock failure must roll back the claim")

	// (b) no rimsky_lock_holders rows exist for this node.
	holders, err := h.Storage.LockHolders().ListByHolderNode(h.Ctx, a.ID, nil)
	require.NoError(t, err)
	require.Empty(t, holders, "no lock-holder row may persist after a failed acquisition")

	// (c) the node is still stale (no fresh→running transition).
	got, err := h.Storage.Nodes().Get(h.Ctx, a.ID, nil)
	require.NoError(t, err)
	require.Equal(t, shared.NodeStateStale, got.State)
}
