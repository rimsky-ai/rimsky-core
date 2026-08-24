// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package observability_test

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/rimsky-ai/rimsky-core/lib/control/observability"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
)

// @decision: service-delivery-stall-signal
// @concept: lifecycle-subscriber
func TestHandler_ServiceStatusReportsWhatASubscribingServiceIsOwed(t *testing.T) {
	d := newSQLiteDriver(t)
	tables := d.Tables()
	ctx := context.Background()
	staged := time.Now().UTC().Truncate(time.Second)
	for _, service := range []string{"worker", "store"} {
		if err := tables.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
			outbox := tables.LifecycleOutbox()
			if err := outbox.Stage(ctx, persistence.LifecycleOutboxRow{
				ClaimProducerName: service,
				ScopeKind:         persistence.LifecycleScopeInstance,
				ScopeID:           "11111111-1111-1111-1111-111111111111",
				Event:             "instance_terminated",
				Payload:           []byte(`{}`),
				StagedAt:          staged,
				NextAttemptAt:     staged,
			}, tx); err != nil {
				return err
			}
			rows, err := outbox.ListPendingForService(ctx, service, persistence.DefaultServiceOutboxPageSize, tx)
			if err != nil {
				return err
			}
			return outbox.RecordAttempt(ctx, rows[0].Seq, staged, "dial tcp: connection refused", tx)
		}); err != nil {
			t.Fatalf("stage a failed delivery for %s: %v", service, err)
		}
	}

	deps := observability.Deps{
		Tables:         tables,
		Queue:          d.Queue(),
		Discovery:      observability.NewDiscovery(&nopProber{}),
		Executors:      []observability.ServiceSpec{{Name: "worker", Endpoint: "exec:9000"}},
		ClaimProducers: []observability.ServiceSpec{{Name: "store", Endpoint: "store:9000"}},
	}
	r := newRouter(t, deps)

	for _, tc := range []struct{ name, path string }{
		{"executor", "/v1/observability/executors/worker"},
		{"claim producer", "/v1/observability/claim-producers/store"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest("GET", tc.path, nil)
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)
			if w.Code != 200 {
				t.Fatalf("status = %d, want 200; body = %s", w.Code, w.Body.String())
			}
			var body struct {
				Pending []observability.PendingLifecycleDelivery `json:"pending_lifecycle_deliveries"`
			}
			if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
				t.Fatalf("decode: %v; body = %s", err, w.Body.String())
			}
			if len(body.Pending) != 1 {
				t.Fatalf("pending_lifecycle_deliveries = %+v, want the one staged row a subscribing service still owes", body.Pending)
			}
			got := body.Pending[0]
			if got.Event != "instance_terminated" || got.AttemptCount != 1 || got.LastError != "dial tcp: connection refused" {
				t.Fatalf("pending delivery = %+v, want the staged event with its attempt count and last error", got)
			}
		})
	}
}
