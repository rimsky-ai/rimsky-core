// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// replica_posture_test.go is the accuracy gate for S-sensors-cron-replica-
// posture-accuracy: the documented sensor-cron replica posture must match the
// binary's actual firing behavior, so an operator reads one honest contract
// instead of being misled by a stale advisory-lock promise into running two
// replicas and silently getting double invalidations.
//
// TestSensorCronReplicaPostureAccuracy asserts the three acceptance facets
// against the REAL Publisher service (the real SensorService firing real
// message POSTs through an httptest receiver — never a stubbed firing path):
//
//  1. A single replica fires each cron window exactly once — observed at the
//     message-POST altitude (one envelope with sender_kind=="publisher"), not a
//     counter only.
//  2. Two independently-running SensorService instances sharing one
//     publisher_subscription_id, each ticked over the same window, together POST
//     exactly two envelopes — the honest N-times fan-out the `concept:replica`
//     contract documents (no cross-replica coordination suppresses the second).
//  3. No advisory-lock / DB-coordination primitive exists in the sensor-cron
//     source (no pg_advisory*, AdvisoryLock, GET_LOCK, leader election), so the
//     documented single-replica posture is provably the implemented behavior.
//     Facet 3 is enforced by assertNoCoordinationPrimitive, a source-scan helper
//     that walks every non-_test.go file under the package directory.
//
// Plus a package-doc accuracy assertion: the sensor.go package doc must not
// carry the retired "in-memory only — a deliberate divergence" advisory-lock-era
// prose, so the durable contract the operator reads is the single-replica one.
//
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
	"sync/atomic"
	"testing"
	"time"

	genv1 "github.com/rimsky-ai/rimsky-core/lib/protocols/proto/v1/gen"
)

func TestSensorCronReplicaPostureAccuracy(t *testing.T) {
	registerTime := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	fireWindow := func() time.Time { return registerTime.Add(6 * time.Minute) }

	// @constraint: Facet 1 — single replica fires once per window, observed at
	// the message-POST altitude: exactly one envelope, carrying sender_kind
	// "publisher".
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
			TargetNode: "tick", MessageType: "invalidate",
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
		if bodies[0]["sender_kind"] != "publisher" {
			t.Errorf("envelope.sender_kind: got %v, want \"publisher\"", bodies[0]["sender_kind"])
		}
	})

	// @constraint: Facet 2 — two independent replicas sharing one
	// publisher_subscription_id, each ticked over the same window, together
	// POST exactly two envelopes: the honest N-times fan-out per
	// concept:replica. No cross-replica coordination suppresses the second.
	t.Run("two_replicas_fan_out_twice", func(t *testing.T) {
		var fireCount int64
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			raw, _ := io.ReadAll(r.Body)
			var body map[string]any
			_ = json.Unmarshal(raw, &body)
			if body["sender_kind"] != "publisher" {
				t.Errorf("envelope.sender_kind: got %v, want \"publisher\"", body["sender_kind"])
			}
			atomic.AddInt64(&fireCount, 1)
			w.WriteHeader(http.StatusCreated)
		}))
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
				TargetNode: "tick", MessageType: "invalidate",
			}); err != nil {
				t.Fatal(err)
			}
		}
		for _, s := range replicas {
			s.clock = fireWindow
			s.Tick(context.Background())
		}

		if got := atomic.LoadInt64(&fireCount); got != 2 {
			t.Errorf("envelopes POSTed across two replicas: got %d, want exactly 2 "+
				"(honest N× fan-out per concept:replica; no cross-replica coordination)", got)
		}
	})

	// @constraint: Facet 3 — no coordination primitive in the package source.
	// assertNoCoordinationPrimitive walks every non-_test.go file under the
	// sensor-cron package directory and fails if any names an advisory-lock /
	// leader-election token, proving the documented single-replica posture is
	// the implemented one rather than a stale advisory-lock promise.
	t.Run("no_coordination_primitive_in_source", func(t *testing.T) {
		assertNoCoordinationPrimitive(t)
	})

	// @constraint: Package-doc accuracy — the sensor.go package doc must not
	// carry the retired "in-memory only — a deliberate divergence"
	// advisory-lock-era prose; the durable contract the operator reads is the
	// single-replica one.
	t.Run("package_doc_omits_retired_divergence_prose", func(t *testing.T) {
		doc := readPackageSourceFile(t, "sensor.go")
		if strings.Contains(doc, "deliberate divergence") {
			t.Errorf("sensor.go package doc still carries the retired " +
				"\"deliberate divergence\" advisory-lock-era prose; the single-" +
				"replica posture is the durable contract")
		}
	})
}

// readPackageSourceFile reads a named file from this package's source
// directory, locating the directory via runtime.Caller so the read is robust
// to the working directory the test runs under.
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

// coordinationPrimitiveTokens are the concrete cross-replica coordination
// primitives the single-replica posture forbids: the Postgres advisory-lock
// function family, a Go-identifier advisory-lock call, the MySQL named-lock
// function, and leader-election machinery. These are CALL/IDENTIFIER tokens,
// not the English phrase "advisory lock" — the package doc legitimately
// DISCLAIMS coordination ("there is no per-subscription advisory-lock...") and
// that honest negation must not trip the gate. Matching the spaced/hyphenated
// prose form would invert the property under test (it would punish accurate
// documentation), so the scan keys on the unspaced primitive tokens that only
// appear when a coordination primitive is actually wired in. Property
// protected: the gate fires on a real coordination primitive in code, never on
// prose that correctly states one is absent.
var coordinationPrimitiveTokens = []string{
	"pg_advisory",
	"advisorylock",
	"get_lock",
	"leaderelect",
	"electleader",
}

// assertNoCoordinationPrimitive walks every non-_test.go Go file under the
// sensor-cron package directory and fails if any names a cross-replica
// coordination primitive (advisory lock / named lock / leader election). It is
// the source-scan enforcer for facet 3 of S-sensors-cron-replica-posture-
// accuracy: the documented single-replica posture is provably the implemented
// behavior only if no coordination primitive lurks in the source. The match is
// case-insensitive against the unspaced primitive tokens so honest prose that
// disclaims coordination does not produce a false positive.
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
