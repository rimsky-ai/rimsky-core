// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

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
	stubserver "github.com/rimsky-ai/rimsky-core/test/support/claim_producers/stub/server"
	stubstore "github.com/rimsky-ai/rimsky-core/test/support/claim_producers/stub/store"
)

type PickPolicy struct {
	Root              string
	OnCommit          action.Action
	OnGiveUp          action.Action
	VisibilityTimeout time.Duration
	SyncStrategy      string
}

type Config struct {
	Root          string
	PickPolicies  map[string]*PickPolicy
	SweepInterval time.Duration
	WithAdmin     bool
}

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
