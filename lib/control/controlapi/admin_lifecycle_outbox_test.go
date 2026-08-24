// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

// @decision: service-delivery-stall-signal

package controlapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/auth"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	_ "github.com/rimsky-ai/rimsky-core/lib/foundation/persistence/sqlite"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
)

type lifecycleOutboxDiagnosticsFixture struct {
	tables    persistence.Tables
	srv       *httptest.Server
	permitted string
	forbidden string
	now       time.Time
}

func newLifecycleOutboxDiagnosticsFixture(t *testing.T) *lifecycleOutboxDiagnosticsFixture {
	t.Helper()
	ctx := context.Background()
	d, err := persistence.Open(ctx, persistence.Config{
		Driver: "sqlite",
		SQLite: &persistence.SQLiteConfig{Path: filepath.Join(t.TempDir(), "state.db")},
	})
	require.NoError(t, err)
	require.NoError(t, d.Migrate(ctx, shared.SilentLogger{}))
	t.Cleanup(func() { _ = d.Close() })

	now := time.Date(2026, 8, 23, 12, 0, 0, 0, time.UTC)
	clock := shared.NewControllableClock(now)
	tables := d.Tables()
	deps := AppDeps{
		Persist: tables,
		Queue:   d.Queue(),
		Logger:  shared.SilentLogger{},
		Clock:   clock,
		AuthState: &AuthState{
			Tables:   tables,
			Registry: BuildV1Registry(),
			Clock:    clock,
			Logger:   shared.SilentLogger{},
		},
	}
	seedKey := func(name string, id byte, permissions string) string {
		t.Helper()
		plaintext, hash, err := auth.Mint()
		require.NoError(t, err)
		require.NoError(t, tables.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
			return tables.APIKeys().Insert(ctx, persistence.APIKey{
				ID:          shared.UUID{id},
				Name:        name,
				KeyHash:     hash[:],
				Permissions: []byte(permissions),
				CreatedAt:   now,
			}, tx)
		}))
		return plaintext
	}
	f := &lifecycleOutboxDiagnosticsFixture{
		tables:    tables,
		permitted: seedKey("diagnostics-reader", 1, `[{"action":"diagnostics:read"}]`),
		forbidden: seedKey("event-reader", 2, `[{"action":"event:read"}]`),
		now:       now,
	}
	f.srv = httptest.NewServer(NewApp(deps))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *lifecycleOutboxDiagnosticsFixture) get(t *testing.T, apiKey string) (int, LifecycleOutboxResponse) {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, f.srv.URL+"/v1/admin/diagnostics/lifecycle-outbox", nil)
	require.NoError(t, err)
	req.Header.Set("Authorization", "Bearer "+apiKey)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	var out LifecycleOutboxResponse
	if resp.StatusCode == http.StatusOK {
		require.NoError(t, json.NewDecoder(resp.Body).Decode(&out))
	}
	return resp.StatusCode, out
}

// @decision: service-delivery-stall-signal
func TestAdminLifecycleOutbox_ReportsWhatEachServiceIsOwed(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := newLifecycleOutboxDiagnosticsFixture(t)

	stagedAt := f.now.Add(-9 * time.Minute)
	require.NoError(t, f.tables.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		for _, row := range []persistence.LifecycleOutboxRow{
			{ClaimProducerName: "alpha", ScopeKind: persistence.LifecycleScopeTemplate,
				ScopeID: "sha256-one", Event: "EventTemplateDeployed",
				Payload: []byte(`{"secret":"never in a diagnostics body"}`), StagedAt: stagedAt},
			{ClaimProducerName: "alpha", ScopeKind: persistence.LifecycleScopeInstance,
				ScopeID: "inst-1", Event: "EventInstanceCreated",
				Payload: []byte(`{}`), StagedAt: f.now.Add(-time.Minute)},
			{ClaimProducerName: "beta", ScopeKind: persistence.LifecycleScopeTemplate,
				ScopeID: "sha256-one", Event: "EventTemplateDeployed",
				Payload: []byte(`{}`), StagedAt: f.now.Add(-2 * time.Minute)},
		} {
			if err := f.tables.LifecycleOutbox().Stage(ctx, row, tx); err != nil {
				return err
			}
		}
		return nil
	}))
	var alphaHead int64
	require.NoError(t, f.tables.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		rows, err := f.tables.LifecycleOutbox().ListPendingForService(ctx, "alpha", 0, tx)
		if err != nil {
			return err
		}
		alphaHead = rows[0].Seq
		return f.tables.LifecycleOutbox().RecordAttempt(ctx, alphaHead,
			f.now.Add(time.Minute), "dial tcp: connection refused", tx)
	}))

	status, body := f.get(t, f.permitted)
	require.Equal(t, http.StatusOK, status)
	require.Len(t, body.Services, 2, "the route reports one entry per service with a pending row")

	byName := map[string]LifecycleOutboxService{}
	for _, s := range body.Services {
		byName[s.Service] = s
	}
	alpha := byName["alpha"]
	require.Equal(t, 2, alpha.Depth)
	require.Equal(t, stagedAt, alpha.OldestStagedAt.UTC())
	require.InDelta(t, (9 * time.Minute).Seconds(), alpha.OldestAgeSeconds, 0.001)
	require.Len(t, alpha.Entries, 2)
	require.Equal(t, alphaHead, alpha.Entries[0].Seq, "a service's rows come back oldest first")
	require.Equal(t, "template", alpha.Entries[0].ScopeKind)
	require.Equal(t, "sha256-one", alpha.Entries[0].ScopeID)
	require.Equal(t, 1, alpha.Entries[0].AttemptCount)
	require.Equal(t, "dial tcp: connection refused", alpha.Entries[0].LastError)
	require.Equal(t, f.now.Add(time.Minute), alpha.Entries[0].NextAttemptAt.UTC())
	require.Equal(t, 1, byName["beta"].Depth)

	raw, err := json.Marshal(body)
	require.NoError(t, err)
	require.NotContains(t, string(raw), "never in a diagnostics body",
		"a staged payload is the subscriber's business; the diagnostics body carries no payload bytes")
}

// @decision: service-delivery-stall-signal
// @concept: permission
func TestAdminLifecycleOutbox_RefusesAKeyWithoutDiagnosticsRead(t *testing.T) {
	t.Parallel()
	f := newLifecycleOutboxDiagnosticsFixture(t)

	status, _ := f.get(t, f.forbidden)
	require.Equal(t, http.StatusForbidden, status,
		"the lifecycle-outbox route is gated by diagnostics:read like the producer-outbox route beside it")

	status, body := f.get(t, f.permitted)
	require.Equal(t, http.StatusOK, status)
	require.Empty(t, body.Services, "an outbox owing nothing reports no service")
}
