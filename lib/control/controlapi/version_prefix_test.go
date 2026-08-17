// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package controlapi

import (
	"context"
	"net/http"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	"github.com/rimsky-ai/rimsky-core/lib/runtime"
)

// @decision: protocol-version-v1-namespaced
func TestEveryMountedRouteSitsUnderTheVersionPrefix(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	d, err := persistence.Open(ctx, persistence.Config{
		Driver: "sqlite",
		SQLite: &persistence.SQLiteConfig{Path: filepath.Join(dir, "state.db")},
	})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if err := d.Migrate(ctx, shared.SilentLogger{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	t.Cleanup(func() { _ = d.Close() })
	clock := shared.NewControllableClock(time.Date(2026, 8, 17, 12, 0, 0, 0, time.UTC))

	app := NewApp(AppDeps{
		Persist:        d.Tables(),
		AdvisoryLocker: d.AdvisoryLocker(),
		Queue:          d.Queue(),
		Clock:          clock,
		Logger:         shared.SilentLogger{},
		AuthState: &AuthState{
			Tables:   d.Tables(),
			Registry: BuildV1Registry(),
			Clock:    clock,
			Logger:   shared.SilentLogger{},
		},
	})

	callback := &runtime.CallbackServer{
		Persist: d.Tables(),
		Queue:   d.Queue(),
		Clock:   clock,
		Logger:  shared.SilentLogger{},
	}

	surfaces := []struct {
		name   string
		routes chi.Routes
	}{
		{"control API", app.(chi.Routes)},
		{"supervisor callback listener", callback.Routes()},
	}

	var bare []string
	for _, s := range surfaces {
		walked := 0
		walkErr := chi.Walk(s.routes, func(method, route string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
			walked++
			if !strings.HasPrefix(route, "/v1/") {
				bare = append(bare, s.name+": "+method+" "+route)
			}
			return nil
		})
		if walkErr != nil {
			t.Fatalf("chi.Walk %s: %v", s.name, walkErr)
		}
		if walked == 0 {
			t.Fatalf("%s mounted no routes: the walk checked nothing", s.name)
		}
		t.Logf("%s: checked %d mounted routes", s.name, walked)
	}
	if len(bare) > 0 {
		sort.Strings(bare)
		t.Fatalf("routes mounted outside the /v1 prefix:\n  %s\n"+
			"the whole control-plus-callback surface sits under one version namespace, with no bare-path carve-outs",
			strings.Join(bare, "\n  "))
	}
}
