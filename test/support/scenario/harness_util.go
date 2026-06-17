// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package scenario

import (
	"bytes"
	"io"
	"testing"

	"github.com/google/uuid"

	"github.com/rimsky-ai/rimsky-core/lib/graph/node"
	"github.com/rimsky-ai/rimsky-core/lib/runtime/executor"
	stubexec "github.com/rimsky-ai/rimsky-core/test/support/executors/stub"
	stubtest "github.com/rimsky-ai/rimsky-core/test/support/executors/stub/stubtest"
)

// bytesReader wraps a byte slice as an io.Reader for http.Post bodies.
func bytesReader(b []byte) io.Reader { return bytes.NewReader(b) }

// parseUUIDStr is a thin shim around uuid.Parse kept alongside the harness
// for readability in DeployTemplate / CreateInstance.
func parseUUIDStr(s string) (uuid.UUID, error) { return uuid.Parse(s) }

// ClaimRef returns a NodeStoreRef declaring a read-only claim against
// the named store with the given selector. Convenience wrapper for the
// common case in scenario tests.
func ClaimRef(storeName, selector string) node.NodeStoreRef {
	return node.NodeStoreRef{Name: storeName, Selector: selector, Intent: "r"}
}

// WriteClaimRef returns a NodeStoreRef declaring a read-write claim
// against the named store with the given selector.
func WriteClaimRef(storeName, selector string) node.NodeStoreRef {
	return node.NodeStoreRef{Name: storeName, Selector: selector, Intent: "rw"}
}

// AliasedClaimRef returns a NodeStoreRef with an explicit alias. Used
// when a node holds multiple claims against the same store (the alias
// disambiguates substitution paths).
func AliasedClaimRef(storeName, selector, intent, alias string) node.NodeStoreRef {
	return node.NodeStoreRef{Name: storeName, Selector: selector, Intent: intent, Alias: alias}
}

// MutexLock returns a NodeLockRef for a process-wide mutex (limit
// configured operator-side per spec §6.1).
func MutexLock(name string) node.NodeLockRef {
	return node.NodeLockRef{Name: name}
}

// CountingLock returns a NodeLockRef. Limit is operator-configured per
// spec §6.1; templates reference by name only.
func CountingLock(name string) node.NodeLockRef {
	return node.NodeLockRef{Name: name}
}

// StartStubExecutorWithSchema stands up a SECOND, standalone stub
// executor on its own OS-assigned gRPC listener whose observability
// Capabilities advertise the supplied expected_attributes_schema, and
// returns its endpoint. Tests wire it as a HarnessOpts.ExtraExecutors
// entry to get a constraint-advertising executor alongside the default
// permissive "stub" — e.g. an executor whose schema declares a
// `minimum:0` property so a node default of `-1` is a genuinely-invalid
// reference. The listener is reaped via t.Cleanup. The transport is
// always "grpc".
func StartStubExecutorWithSchema(t testing.TB, schema []byte) executor.Endpoint {
	t.Helper()
	s := stubexec.New()
	_, addr := stubtest.ListenWithSchema(t, s, schema)
	return executor.Endpoint{Transport: "grpc", URL: addr}
}

// StartStubModeExecutorWithSchema is StartStubExecutorWithSchema but the
// standalone stub runs in immediate-success "stub mode" — every Execute
// dispatch short-circuits to a Success Outcome. This lets a
// constraint-advertising executor both (a) advertise a constraining
// schema the registration/instantiation validators read and (b) actually
// settle a dispatched node to a terminal Complete verdict, without the
// test holding a reference to the stub to script per-node-type behavior.
// Used by the instantiation static-config gate acceptance, whose
// well-formed-instance leg must run a node referencing the constrained
// executor to terminal. The listener is reaped via t.Cleanup; transport
// is always "grpc".
func StartStubModeExecutorWithSchema(t testing.TB, schema []byte) executor.Endpoint {
	t.Helper()
	s := stubexec.New().EnableStubMode()
	_, addr := stubtest.ListenWithSchema(t, s, schema)
	return executor.Endpoint{Transport: "grpc", URL: addr}
}
