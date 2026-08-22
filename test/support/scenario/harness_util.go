// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package scenario

import (
	"bytes"
	"io"
	"testing"

	"github.com/google/uuid"

	"github.com/rimsky-ai/rimsky-core/lib/graph/node"
	"github.com/rimsky-ai/rimsky-core/lib/protocols/claimproducer"
	"github.com/rimsky-ai/rimsky-core/lib/runtime/executor"
	stubexec "github.com/rimsky-ai/rimsky-core/test/support/executors/stub"
	stubtest "github.com/rimsky-ai/rimsky-core/test/support/executors/stub/stubtest"
)

func bytesReader(b []byte) io.Reader { return bytes.NewReader(b) }

func parseUUIDStr(s string) (uuid.UUID, error) { return uuid.Parse(s) }

func ClaimRef(producerName, selector string) node.NodeClaimProducerRef {
	return node.NodeClaimProducerRef{Name: producerName, Selector: selector, Intent: "r"}
}

func WriteClaimRef(producerName, selector string) node.NodeClaimProducerRef {
	return node.NodeClaimProducerRef{Name: producerName, Selector: selector, Intent: "rw"}
}

func AliasedClaimRef(producerName, selector string, intent claimproducer.Intent, alias string) node.NodeClaimProducerRef {
	return node.NodeClaimProducerRef{Name: producerName, Selector: selector, Intent: intent, Alias: alias}
}

// @concept: inertness
func ClaimRefWithData(producerName, selector string, data []byte) node.NodeClaimProducerRef {
	return node.NodeClaimProducerRef{Name: producerName, Selector: selector, Intent: "rw", Data: data}
}

func MutexLock(name string) node.NodeLockRef {
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
