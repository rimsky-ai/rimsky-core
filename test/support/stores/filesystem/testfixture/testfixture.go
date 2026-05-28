// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// Package testfixture stands up an in-process loopback claim-producer
// store-service for tests that want to drive the filesystem-store
// surface over the wire.
//
// The production filesystem-store implementation is not part of this
// repo. To preserve the in-rimsky test-fixture surface without dragging
// in production code, this testfixture wraps `pkg:stores/stub`
// (deterministic in-memory) while exposing the same `Start` signature
// callers had against the filesystem-backed fixture. Filesystem-specific
// behaviour (pick-policy folder semantics, queue-vs-ring on-disk
// dynamics, sync-strategy timing) is out of scope for this fixture.
package testfixture

import (
	"context"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"sort"
	"testing"
	"time"

	"github.com/rimsky-ai/rimsky-core/lib/protocols/action"
	stubserver "github.com/rimsky-ai/rimsky-core/test/support/stores/stub/server"
	stubstore "github.com/rimsky-ai/rimsky-core/test/support/stores/stub/store"
)

// PickPolicy mirrors the configuration shape that the production
// filesystem-store's PickPolicy used to expose, so call-site test
// code can construct fixture configs without importing the
// production-side store package. Only `Root`, `OnCommit`, and
// `OnGiveUp` are honoured by the stub-backed translation; the timing
// and sync-strategy fields are accepted for source compatibility and
// ignored. Filesystem-specific tests that rely on these timings are
// out of scope for this fixture.
type PickPolicy struct {
	Root              string
	OnCommit          action.Action
	OnGiveUp          action.Action
	VisibilityTimeout time.Duration
	SyncStrategy      string
}

// Config configures the loopback store-service. Root is honoured as
// the filesystem root scanned for auto-discovered folders (one queue
// item per direct child of `<Root>/<PickPolicy.Root>`); the stub
// translation walks the directory once at Start and seeds the
// resulting items into the stub-store's queue. Tests asserting
// dynamic filesystem behaviour (mid-run folder additions,
// sync-strategy timing) are out of scope for this fixture.
type Config struct {
	Root          string
	PickPolicies  map[string]*PickPolicy
	SweepInterval time.Duration
	WithAdmin     bool
}

// Start spawns the loopback store-service on ephemeral listeners.
// Returns (grpcEndpoint, adminEndpoint, teardown). adminEndpoint is
// empty — the stub-backed fixture exposes no admin surface; tests
// that need admin seeding use the stub's `*stubstore.Store` handle
// directly (see `pkg:stores/stub/testfixture` for that surface).
func Start(t *testing.T, cfg Config) (grpcEndpoint, adminEndpoint string, teardown func()) {
	t.Helper()
	grpcLis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("filesystem testfixture: grpc listen: %v", err)
	}
	httpLis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		_ = grpcLis.Close()
		t.Fatalf("filesystem testfixture: http listen: %v", err)
	}

	stubCfg := translatePickPolicies(t, cfg)
	st := stubstore.New(stubCfg)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		_ = stubserver.RunWithStore(ctx, stubserver.Config{
			Substrate: stubCfg,
		}, st, grpcLis, httpLis)
		close(done)
	}()
	return grpcLis.Addr().String(), "", func() {
		cancel()
		<-done
	}
}

// translatePickPolicies walks `cfg.Root` under each pick policy's
// `Root` subdirectory, listing direct children (excluding dot-files)
// and seeding one queue item per discovered folder. Each item's
// payload is `{"Folder":"<name>"}` — the shape the existing
// fs-pick test asserts against. Selectors map verbatim from
// `cfg.PickPolicies` keys to stub pick-policy keys.
func translatePickPolicies(t *testing.T, cfg Config) stubstore.Config {
	t.Helper()
	out := stubstore.Config{
		PickPolicies: make(map[string]stubstore.PickPolicyConfig, len(cfg.PickPolicies)),
	}
	for selector, pp := range cfg.PickPolicies {
		if pp == nil {
			continue
		}
		sub := filepath.Join(cfg.Root, pp.Root)
		folders := listFolders(t, sub)
		items := make([]json.RawMessage, 0, len(folders))
		for _, name := range folders {
			payload, _ := json.Marshal(struct {
				Folder string `json:"Folder"`
			}{Folder: name})
			items = append(items, payload)
		}
		out.PickPolicies[selector] = stubstore.PickPolicyConfig{
			OnCommit:     pp.OnCommit,
			OnGiveUp:     pp.OnGiveUp,
			InitialItems: items,
		}
	}
	return out
}

// listFolders returns the direct child directory names of dir,
// excluding dot-prefixed entries. Returns an empty slice if dir does
// not exist — callers may pre-create the root in a test and rely on
// the testfixture not pre-existing.
func listFolders(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		t.Fatalf("filesystem testfixture: read dir %s: %v", dir, err)
	}
	out := make([]string, 0, len(entries))
	for _, e := range entries {
		name := e.Name()
		if len(name) == 0 || name[0] == '.' {
			continue
		}
		if !e.IsDir() {
			continue
		}
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}
