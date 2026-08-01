// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package runtime

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/locks"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/locks/storetest"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	_ "github.com/rimsky-ai/rimsky-core/lib/foundation/persistence/sqlite"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	tmplspec "github.com/rimsky-ai/rimsky-core/lib/foundation/spec"
	"github.com/rimsky-ai/rimsky-core/lib/protocols/claimproducer"
	"github.com/rimsky-ai/rimsky-core/lib/runtime/peer"
)

type acquisitionMetricsSpy struct {
	noopMetrics
	incCalls  []string
	obsCalls  []string
	producers []string
}

func (m *acquisitionMetricsSpy) IncClaimAcquisition(producer, outcome string) {
	m.incCalls = append(m.incCalls, outcome)
	m.producers = append(m.producers, producer)
}

func (m *acquisitionMetricsSpy) ObserveClaimAcquisitionLatency(producer string, _ float64) {
	m.obsCalls = append(m.obsCalls, producer)
}

func TestAcquireClaim_ProducerCallErrorOnOpenRecordsErroredMetrics(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	d, err := persistence.Open(ctx, persistence.Config{
		Driver: "sqlite",
		SQLite: &persistence.SQLiteConfig{Path: filepath.Join(t.TempDir(), "state.db")},
	})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := d.Migrate(ctx, shared.SilentLogger{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	t.Cleanup(func() { _ = d.Close() })
	tables := d.Tables()

	const (
		producer = "errored-open-store"
		supMe    = "sup-errored-open"
	)
	templateHash := "sha256-" + uuid.NewString()
	instanceID := shared.UUID(uuid.New())
	mainScopeID := shared.UUID(uuid.New())
	nodeID := shared.UUID(uuid.New())
	runID := shared.UUID(uuid.New())

	if err := tables.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		if err := tables.Templates().Insert(ctx, persistence.TemplateInsertInput{
			ID: templateHash, Spec: tmplspec.TemplateSpec{Name: "errored-open-fixture", Version: "1"},
			State: persistence.TemplateStateRegistered, Source: "direct",
		}, tx); err != nil {
			return err
		}
		if err := tables.RunScopes().Create(ctx, persistence.RunScopeRow{
			ID: mainScopeID, GraphName: tmplspec.MainGraphName, InstanceID: instanceID,
		}, tx); err != nil {
			return err
		}
		if _, err := tables.Instances().Create(ctx, persistence.InstanceCreateInput{TargetRoutingIdentity: "test-agent",
			ID: instanceID, TemplateHash: templateHash,
		}, tx); err != nil {
			return err
		}
		if _, err := tables.Nodes().Create(ctx, persistence.NodeCreateInput{
			ID: nodeID, InstanceID: instanceID, NodeType: "worker", Executor: "stub",
		}, tx); err != nil {
			return err
		}
		msgID := shared.UUID(uuid.New())
		if err := tables.Messages().Insert(ctx, persistence.EnqueueMessageRequest{
			ID: msgID, InstanceID: instanceID, Type: "test/seed", Sender: "test", SenderKind: "operator",
		}, tx); err != nil {
			return err
		}
		frameID, err := tables.Frames().InsertRunningFrame(ctx, instanceID, msgID, mainScopeID, tx)
		if err != nil {
			return err
		}
		return tables.NodeRunTree().CreateRootNodeRun(ctx, persistence.CreateRootNodeRunInput{
			NodeRunID: runID, NodeID: nodeID, FrameID: frameID, RunScopeID: mainScopeID, ExecutorName: "stub",
		}, tx)
	}); err != nil {
		t.Fatalf("seed fixture: %v", err)
	}

	fake := storetest.NewFake(producer, claimproducer.Capabilities{})
	fake.OpenFunc = func(_ claimproducer.ClaimID, spec claimproducer.ClaimSpec) (claimproducer.OpenOutcome, error) {
		return claimproducer.OpenOutcome{}, peer.NewProducerCallError(producer, "Open", errors.New("upstream unreachable"))
	}
	reg := locks.NewRegistry()
	reg.Add(producer, fake)

	spy := &acquisitionMetricsSpy{}
	args := RunArgs{
		Persist:               tables,
		Queue:                 d.Queue(),
		AdvisoryLocker:        d.AdvisoryLocker(),
		ClaimHandles:          tables.ClaimHandles(),
		ClaimProducerRegistry: reg,
		Clock:                 shared.SystemClock{},
		Logger:                shared.SilentLogger{},
		SupervisorID:          supMe,
		Metrics:               spy,
	}
	spec := claimproducer.ClaimSpec{ProducerName: producer, Selector: "/errored-open-selector", Intent: "rw", Alias: "data"}
	cand := persistence.Candidate{NodeRunID: runID, NodeID: nodeID, NodeType: "worker"}

	var res openResult
	err = tables.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		var aErr error
		_, res, aErr = acquireClaim(ctx, args, instanceID, spec, cand, 5*time.Second, nil, nil, tx)
		return aErr
	})
	if err == nil {
		t.Fatal("acquireClaim: expected a ProducerCallError, got nil")
	}
	var pcErr *peer.ProducerCallError
	if !errors.As(err, &pcErr) {
		t.Fatalf("acquireClaim error = %v, want a *peer.ProducerCallError", err)
	}
	if res != openResultErrored {
		t.Fatalf("acquireClaim result = %v, want openResultErrored", res)
	}

	if len(spy.incCalls) != 1 || spy.incCalls[0] != "errored" {
		t.Fatalf("IncClaimAcquisition calls = %v, want exactly one call with outcome %q", spy.incCalls, "errored")
	}
	if len(spy.producers) != 1 || spy.producers[0] != producer {
		t.Fatalf("IncClaimAcquisition producer = %v, want %q", spy.producers, producer)
	}
	if len(spy.obsCalls) != 1 || spy.obsCalls[0] != producer {
		t.Fatalf("ObserveClaimAcquisitionLatency calls = %v, want exactly one call for producer %q", spy.obsCalls, producer)
	}
}
