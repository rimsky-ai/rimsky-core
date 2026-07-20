// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package runtime

import (
	"context"
	"strings"
	"testing"
)

func TestRejectDelegateRecursionInChain_GraphAlreadyOpenRejected(t *testing.T) {
	_, scopes := newFakes()
	mainScope := scopes.makeRootScope("main", newUUID())
	callerRun := newUUID()
	workerScope := scopes.makeChildScope(mainScope, callerRun, "", "worker")
	innerCallerRun := newUUID()
	nestedScope := scopes.makeChildScope(workerScope, innerCallerRun, "", "inner")

	err := rejectDelegateRecursionInChain(context.Background(), scopes, nil, nestedScope, "worker")
	if err == nil {
		t.Fatalf("dispatching sub-graph %q under a chain that already holds a %q scope must be rejected", "worker", "worker")
	}
	if !strings.Contains(err.Error(), "worker") {
		t.Fatalf("rejection must name the recurring graph, got: %v", err)
	}

	if err := rejectDelegateRecursionInChain(context.Background(), scopes, nil, nestedScope, "inner"); err == nil {
		t.Fatalf("dispatching sub-graph %q with an %q scope already in the chain must be rejected (self-recursion)", "inner", "inner")
	}
}

func TestRejectDelegateRecursionInChain_FreshGraphAccepted(t *testing.T) {
	_, scopes := newFakes()
	mainScope := scopes.makeRootScope("main", newUUID())
	callerRun := newUUID()
	workerScope := scopes.makeChildScope(mainScope, callerRun, "", "worker")

	if err := rejectDelegateRecursionInChain(context.Background(), scopes, nil, workerScope, "other"); err != nil {
		t.Fatalf("a sub-graph not present in the ancestor chain must dispatch: %v", err)
	}
}

func TestRejectDelegateRecursionInChain_PartitionScopesShareGraphName(t *testing.T) {
	_, scopes := newFakes()
	mainScope := scopes.makeRootScope("main", newUUID())
	fanRun := newUUID()
	partitionScope := scopes.makeChildScope(mainScope, fanRun, "a", "main")

	if err := rejectDelegateRecursionInChain(context.Background(), scopes, nil, partitionScope, "worker"); err != nil {
		t.Fatalf("a delegate dispatch under a fan-out partition scope must pass the chain walk: %v", err)
	}
}
