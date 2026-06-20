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

func bytesReader(b []byte) io.Reader { return bytes.NewReader(b) }

func parseUUIDStr(s string) (uuid.UUID, error) { return uuid.Parse(s) }

func ClaimRef(storeName, selector string) node.NodeClaimProducerRef {
	return node.NodeClaimProducerRef{Name: storeName, Selector: selector, Intent: "r"}
}

func WriteClaimRef(storeName, selector string) node.NodeClaimProducerRef {
	return node.NodeClaimProducerRef{Name: storeName, Selector: selector, Intent: "rw"}
}

func AliasedClaimRef(storeName, selector, intent, alias string) node.NodeClaimProducerRef {
	return node.NodeClaimProducerRef{Name: storeName, Selector: selector, Intent: intent, Alias: alias}
}

func MutexLock(name string) node.NodeLockRef {
	return node.NodeLockRef{Name: name}
}

func CountingLock(name string) node.NodeLockRef {
	return node.NodeLockRef{Name: name}
}

func StartStubExecutorWithSchema(t testing.TB, schema []byte) executor.Endpoint {
	t.Helper()
	s := stubexec.New()
	_, addr := stubtest.ListenWithSchema(t, s, schema)
	return executor.Endpoint{Transport: "grpc", URL: addr}
}

func StartStubModeExecutorWithSchema(t testing.TB, schema []byte) executor.Endpoint {
	t.Helper()
	s := stubexec.New().EnableStubMode()
	_, addr := stubtest.ListenWithSchema(t, s, schema)
	return executor.Endpoint{Transport: "grpc", URL: addr}
}
