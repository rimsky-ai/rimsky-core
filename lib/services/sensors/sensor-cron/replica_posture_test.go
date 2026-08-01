// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

// @concept: sensor
// @concept: replica

package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	genv1 "github.com/rimsky-ai/rimsky-core/lib/protocols/proto/v1/gen"
)

func TestSensorCronReplicaPostureAccuracy(t *testing.T) {
	registerTime := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	fireWindow := func() time.Time { return registerTime.Add(6 * time.Minute) }

	t.Run("single_replica_fires_once_per_window", func(t *testing.T) {
		var bodies []map[string]any
		var bodiesMu sync.Mutex
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			raw, _ := io.ReadAll(r.Body)
			var body map[string]any
			_ = json.Unmarshal(raw, &body)
			bodiesMu.Lock()
			bodies = append(bodies, body)
			bodiesMu.Unlock()
			w.WriteHeader(http.StatusCreated)
		}))
		defer srv.Close()

		s := NewSensorService(srv.URL, noopLogger{})
		s.clock = func() time.Time { return registerTime }
		cfg := map[string]any{"cron": "*/5 * * * *"}
		raw, _ := json.Marshal(cfg)
		if _, err := s.Subscribe(context.Background(), &genv1.SubscribeRequest{
			PublisherSubscriptionId: "w1", InstanceId: "i1", Kind: "cron", ResolvedConfig: raw,
			MessageType: "invalidate",
		}); err != nil {
			t.Fatal(err)
		}

		s.clock = fireWindow
		s.Tick(context.Background())

		bodiesMu.Lock()
		defer bodiesMu.Unlock()
		if len(bodies) != 1 {
			t.Fatalf("envelopes POSTed: got %d, want exactly 1 per fire window", len(bodies))
		}
		if sub, _ := bodies[0]["publisher_subscription_id"].(string); sub == "" {
			t.Errorf("envelope.publisher_subscription_id: missing or empty (auth path discriminator)")
		}
	})

	t.Run("two_replicas_fan_out_but_dedup_collapses_same_window", func(t *testing.T) {
		srv, recv := newDedupingReceiver()
		defer srv.Close()

		replicaA := NewSensorService(srv.URL, noopLogger{})
		replicaB := NewSensorService(srv.URL, noopLogger{})
		replicas := []*SensorService{replicaA, replicaB}

		cfg := map[string]any{"cron": "*/5 * * * *"}
		raw, _ := json.Marshal(cfg)
		for _, s := range replicas {
			s.clock = func() time.Time { return registerTime }
			if _, err := s.Subscribe(context.Background(), &genv1.SubscribeRequest{
				PublisherSubscriptionId: "w1", InstanceId: "i1", Kind: "cron", ResolvedConfig: raw,
				MessageType: "invalidate",
			}); err != nil {
				t.Fatal(err)
			}
		}
		for _, s := range replicas {
			s.clock = fireWindow
			s.Tick(context.Background())
		}

		total, accepted := recv.snapshot()
		if total != 2 {
			t.Errorf("envelopes POSTed across two replicas: got %d, want exactly 2 "+
				"(honest N× fan-out per concept:replica; no cross-replica coordination)", total)
		}
		if accepted != 1 {
			t.Errorf("dedup-accepted envelopes: got %d, want 1 "+
				"(same subscription + same fire window collapses to one enqueued message at "+
				"the control API; the window-deterministic idempotency key makes this deliberate, "+
				"not a silent leader election)", accepted)
		}
	})

	t.Run("no_coordination_primitive_in_source", func(t *testing.T) {
		assertNoCoordinationPrimitive(t)
	})

	t.Run("package_doc_omits_retired_divergence_prose", func(t *testing.T) {
		doc := readPackageSourceFile(t, "sensor.go")
		if strings.Contains(doc, "deliberate divergence") {
			t.Errorf("sensor.go package doc still carries the retired " +
				"\"deliberate divergence\" advisory-lock-era prose; the single-" +
				"replica posture is the durable contract")
		}
	})
}

func readPackageSourceFile(t *testing.T, name string) string {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller(0) failed to resolve this test file's path")
	}
	dir := filepath.Dir(thisFile)
	raw, err := os.ReadFile(filepath.Join(dir, name))
	if err != nil {
		t.Fatalf("read %s: %v", name, err)
	}
	return string(raw)
}

var coordinationPrimitiveTokens = []string{
	"pg_advisory",
	"advisorylock",
	"get_lock",
	"leaderelect",
	"electleader",
}

func assertNoCoordinationPrimitive(t *testing.T) {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller(0) failed to resolve this test file's path")
	}
	dir := filepath.Dir(thisFile)

	err := filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		raw, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		lower := strings.ToLower(string(raw))
		for _, tok := range coordinationPrimitiveTokens {
			if strings.Contains(lower, tok) {
				t.Errorf("%s names coordination primitive %q; the single-"+
					"replica posture forbids cross-replica advisory-lock / "+
					"leader-election coordination (concept:replica)",
					filepath.Base(path), tok)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk sensor-cron package source: %v", err)
	}
}
