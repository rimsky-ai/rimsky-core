// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package harness

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/testcontainers/testcontainers-go"
	tcnet "github.com/testcontainers/testcontainers-go/network"
	"github.com/testcontainers/testcontainers-go/wait"
)

const (
	stubExecutorImage  = "rimsky-test/stubexecutor"
	sharedStubAlias    = "executor-stub"
	sharedStubErrAlias = "executor-stub-erroring"
)

type stubRegistry struct {
	alias      string
	forceError bool

	mu       sync.Mutex
	launched map[string]error
}

func newStubRegistry(alias string, forceError bool) *stubRegistry {
	return &stubRegistry{alias: alias, forceError: forceError, launched: map[string]error{}}
}

func (r *stubRegistry) ensureOn(ctx context.Context, networkName string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	err, seen := r.launched[networkName]
	if !seen {
		err = launchExecutorStub(ctx, networkName, r.alias, r.forceError)
		r.launched[networkName] = err
	}
	return err
}

var (
	sharedStubs    = newStubRegistry(sharedStubAlias, false)
	sharedErrStubs = newStubRegistry(sharedStubErrAlias, true)
)

func StartExecutorStubOnNetwork(ctx context.Context, t testing.TB, networkName string) (endpoint string) {
	t.Helper()
	if err := sharedStubs.ensureOn(ctx, networkName); err != nil {
		t.Fatalf("harness: start executor-stub: %v", err)
	}
	return sharedStubAlias + ":9300"
}

func StartErroringExecutorStubOnNetwork(ctx context.Context, t testing.TB, networkName string) (endpoint string) {
	t.Helper()
	if err := sharedErrStubs.ensureOn(ctx, networkName); err != nil {
		t.Fatalf("harness: start erroring executor-stub: %v", err)
	}
	return sharedStubErrAlias + ":9300"
}

func launchExecutorStub(ctx context.Context, networkName, alias string, forceError bool) error {
	env := map[string]string{
		"EXECUTOR_STUB_BIND": "0.0.0.0:9300",
	}
	if forceError {
		env["EXECUTOR_STUB_FORCE_ERROR"] = "1"
	}
	_, err := runWithRetry(ctx, ImageRef(stubExecutorImage),
		tcnet.WithNetworkName([]string{alias}, networkName),
		testcontainers.WithEnv(env),
		testcontainers.WithExposedPorts("9300/tcp"),
		testcontainers.WithWaitStrategy(
			wait.ForListeningPort("9300/tcp").WithStartupTimeout(120*time.Second),
		),
	)
	return err
}
