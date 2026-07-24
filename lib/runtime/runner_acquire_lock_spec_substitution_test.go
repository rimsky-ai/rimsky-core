// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package runtime

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	attributes "github.com/rimsky-ai/rimsky-core/lib/graph/attribute"
	"github.com/rimsky-ai/rimsky-core/lib/graph/node"
	"github.com/rimsky-ai/rimsky-core/test/support/pgdbtest"
)

// @decision: substitution-failure-routes-with-substitution
func TestTryAcquire_LockNameSubstitutionFailureReturnsSentinelWithLockNameSite(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	d := pgdbtest.OpenDriver(ctx, t)
	backend := d.Tables()

	tmpl := insertDeployedTemplateInternal(ctx, t, backend, node.TemplateSpec{
		Name: "lock-name-substitution-failed-tmpl", Version: "1",
		Nodes: []node.TemplateNodeDef{{
			Type:     "acquirer",
			Executor: "stub",
			Locks:    []node.NodeLockRef{{Name: "{{deps.missing.field}}"}},
		}},
	})
	nodeID, frameID, nodeRunID, _, _ := seedStaleCandidateInternal(ctx, t, backend, d.Queue(), tmpl.ID, "acquirer")

	cand := persistence.Candidate{NodeID: nodeID, NodeRunID: nodeRunID, FrameID: frameID}
	args := RunArgs{Persist: backend, Logger: shared.SilentLogger{}}

	var acq acquisition
	var ok bool
	var acquireErr error
	require.NoError(t, backend.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		acq, ok, acquireErr = tryAcquire(ctx, args, cand, 5*time.Second, tx)
		return nil
	}))
	if !errors.Is(acquireErr, errAcquireLockSpecSubstitutionFailed) {
		t.Fatalf("expected errAcquireLockSpecSubstitutionFailed sentinel; got ok=%v err=%v", ok, acquireErr)
	}
	if acq.LockSpecSubstitutionSite != substitutionSiteLockName {
		t.Fatalf("LockSpecSubstitutionSite = %q, want %q", acq.LockSpecSubstitutionSite, substitutionSiteLockName)
	}
	if acq.LockSpecSubstitutionErr == "" {
		t.Fatal("expected LockSpecSubstitutionErr to carry the underlying substitution failure")
	}
}

// @decision: substitution-failure-routes-with-substitution
func TestTryAcquire_ScopeSubstitutionFailureReturnsSentinelWithScopeSite(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	d := pgdbtest.OpenDriver(ctx, t)
	backend := d.Tables()

	tmpl := insertDeployedTemplateInternal(ctx, t, backend, node.TemplateSpec{
		Name: "scope-substitution-failed-tmpl", Version: "1",
		Nodes: []node.TemplateNodeDef{{
			Type:     "acquirer",
			Executor: "stub",
			ClaimProducers: []node.NodeClaimProducerRef{
				{Name: "some-store", Selector: "/x/{{deps.missing.field}}", Intent: "rw"},
			},
		}},
	})
	nodeID, frameID, nodeRunID, _, _ := seedStaleCandidateInternal(ctx, t, backend, d.Queue(), tmpl.ID, "acquirer")

	cand := persistence.Candidate{NodeID: nodeID, NodeRunID: nodeRunID, FrameID: frameID}
	args := RunArgs{Persist: backend, Logger: shared.SilentLogger{}}

	var acq acquisition
	var ok bool
	var acquireErr error
	require.NoError(t, backend.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		acq, ok, acquireErr = tryAcquire(ctx, args, cand, 5*time.Second, tx)
		return nil
	}))
	if !errors.Is(acquireErr, errAcquireLockSpecSubstitutionFailed) {
		t.Fatalf("expected errAcquireLockSpecSubstitutionFailed sentinel; got ok=%v err=%v", ok, acquireErr)
	}
	if acq.LockSpecSubstitutionSite != substitutionSiteScope {
		t.Fatalf("LockSpecSubstitutionSite = %q, want %q", acq.LockSpecSubstitutionSite, substitutionSiteScope)
	}
	if acq.LockSpecSubstitutionErr == "" {
		t.Fatal("expected LockSpecSubstitutionErr to carry the underlying substitution failure")
	}
}

// @concept: error-policy
// @decision: substitution-failure-routes-with-substitution
func TestHandleAcquireLockSpecSubstitutionFailed_TerminalizesAndEmitsTemplateResolutionFailedEvent(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	d := pgdbtest.OpenDriver(ctx, t)
	backend := d.Tables()
	q := d.Queue()

	tmpl := insertDeployedTemplateInternal(ctx, t, backend, node.TemplateSpec{
		Name: "lock-spec-substitution-terminalize-tmpl", Version: "1",
		Nodes: []node.TemplateNodeDef{{Type: "acquirer", Executor: "stub"}},
	})
	nodeID, _, nodeRunID, runScopeID, instanceID := seedStaleCandidateInternal(ctx, t, backend, q, tmpl.ID, "acquirer")

	if got := runStateInternal(ctx, t, backend, nodeRunID); got != "stale" {
		t.Fatalf("precondition: expected a freshly-enqueued candidate to be 'stale', got %q", got)
	}

	nodeDef := &node.TemplateNodeDef{Type: "acquirer", Executor: "stub"}
	acq := acquisition{
		NodeID: nodeID, NodeRunID: nodeRunID, InstanceID: instanceID, NodeType: "acquirer",
		NodeDef: nodeDef, RunScopeID: runScopeID,
		LockSpecSubstitutionSite:      substitutionSiteLockName,
		LockSpecSubstitutionDirective: "{{deps.missing.field}}",
		LockSpecSubstitutionErr:       "attributes: missing source deps.missing.field",
	}
	cand := persistence.Candidate{NodeID: nodeID, NodeRunID: nodeRunID}
	args := RunArgs{
		Persist: backend, Queue: q, Logger: shared.SilentLogger{}, SupervisorID: "sup-lock-spec-substitution",
		Clock: shared.SystemClock{},
	}

	decision := handleAcquireLockSpecSubstitutionFailed(ctx, args, acq, cand)
	if decision != nil && decision.IsRetry() {
		t.Fatalf("expected a give-up resolution (no configured retry policy for a lock-name substitution failure), got retry decision: %+v", decision)
	}

	if got := runStateInternal(ctx, t, backend, nodeRunID); got != "failed" {
		t.Fatalf("expected the lock-spec substitution failure to settle the run to 'failed', got %q", got)
	}

	require.NoError(t, backend.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		cands, err := q.SelectCandidates(ctx, persistence.SelectCandidatesRequest{
			AcceptedExecutors: []string{"stub"}, AcceptedClaimProducers: []string{}, Limit: 16,
		}, tx)
		if err != nil {
			return err
		}
		for _, c := range cands {
			if c.NodeRunID == nodeRunID {
				t.Fatalf("candidate %s with a persistent lock-spec substitution failure is still selectable; "+
					"it must terminalize instead of hot-looping every sweep", nodeRunID)
			}
		}
		return nil
	}))

	assertTemplateResolutionFailedEventWithSite(ctx, t, backend, nodeID, substitutionSiteLockName)
}

// @concept: error-policy
// @decision: substitution-failure-routes-with-substitution
func TestHandleAcquireLockSpecSubstitutionFailed_EmitsScopeSiteForSelectorFailure(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	d := pgdbtest.OpenDriver(ctx, t)
	backend := d.Tables()
	q := d.Queue()

	tmpl := insertDeployedTemplateInternal(ctx, t, backend, node.TemplateSpec{
		Name: "scope-substitution-terminalize-tmpl", Version: "1",
		Nodes: []node.TemplateNodeDef{{Type: "acquirer", Executor: "stub"}},
	})
	nodeID, _, nodeRunID, runScopeID, instanceID := seedStaleCandidateInternal(ctx, t, backend, q, tmpl.ID, "acquirer")

	nodeDef := &node.TemplateNodeDef{Type: "acquirer", Executor: "stub"}
	acq := acquisition{
		NodeID: nodeID, NodeRunID: nodeRunID, InstanceID: instanceID, NodeType: "acquirer",
		NodeDef: nodeDef, RunScopeID: runScopeID,
		LockSpecSubstitutionSite:      substitutionSiteScope,
		LockSpecSubstitutionDirective: "/x/{{deps.missing.field}}",
		LockSpecSubstitutionErr:       "attributes: missing source deps.missing.field",
	}
	cand := persistence.Candidate{NodeID: nodeID, NodeRunID: nodeRunID}
	args := RunArgs{
		Persist: backend, Queue: q, Logger: shared.SilentLogger{}, SupervisorID: "sup-scope-substitution",
		Clock: shared.SystemClock{},
	}

	if decision := handleAcquireLockSpecSubstitutionFailed(ctx, args, acq, cand); decision != nil && decision.IsRetry() {
		t.Fatalf("expected give-up resolution, got retry decision: %+v", decision)
	}
	if got := runStateInternal(ctx, t, backend, nodeRunID); got != "failed" {
		t.Fatalf("expected settle to 'failed', got %q", got)
	}
	assertTemplateResolutionFailedEventWithSite(ctx, t, backend, nodeID, substitutionSiteScope)
}

// @decision: substitution-failure-routes-with-substitution
func TestApplyAttributeFailure_EmitsTemplateResolutionFailedEventWithAttributeSite(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	d := pgdbtest.OpenDriver(ctx, t)
	backend := d.Tables()
	q := d.Queue()

	tmpl := insertDeployedTemplateInternal(ctx, t, backend, node.TemplateSpec{
		Name: "attribute-substitution-failed-tmpl", Version: "1",
		Nodes: []node.TemplateNodeDef{{Type: "leaf", Executor: "stub"}},
	})
	nodeID, _, nodeRunID, runScopeID, instanceID := seedStaleCandidateInternal(ctx, t, backend, q, tmpl.ID, "leaf")

	nodeDef := &node.TemplateNodeDef{Type: "leaf", Executor: "stub"}
	acq := &acquisition{
		NodeID: nodeID, NodeRunID: nodeRunID, InstanceID: instanceID, NodeType: "leaf",
		NodeDef: nodeDef, RunScopeID: runScopeID,
	}
	args := RunArgs{
		Persist: backend, Queue: q, Logger: shared.SilentLogger{}, SupervisorID: "sup-attribute-substitution",
		Clock: shared.SystemClock{},
	}

	subErr := &attributes.ErrMissingSource{Directive: "deps.missing.field", Reason: "no upstream"}
	if err := applyAttributeFailure(ctx, args, acq, subErr); err != nil {
		t.Fatalf("applyAttributeFailure: %v", err)
	}
	assertTemplateResolutionFailedEventWithSite(ctx, t, backend, nodeID, substitutionSiteAttribute)
}

func assertTemplateResolutionFailedEventWithSite(
	ctx context.Context, t *testing.T, backend persistence.Tables, nodeID shared.UUID, wantSite string,
) {
	t.Helper()
	var found bool
	require.NoError(t, backend.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		res, err := backend.Events().List(ctx, persistence.EventListFilter{
			NodeID: &nodeID,
			KindIn: []string{"template_resolution_failed"},
		}, persistence.ListPagination{Limit: 16}, tx)
		if err != nil {
			return err
		}
		for _, ev := range res.Events {
			site, _ := ev.Payload["site"].(string)
			if site == wantSite {
				found = true
				return nil
			}
		}
		return nil
	}))
	if !found {
		t.Fatalf("no template_resolution_failed event with site=%q for node %s", wantSite, nodeID)
	}
}
