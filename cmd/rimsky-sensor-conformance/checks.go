// Copyright © 2026 Fall Guy Consulting.
// Licensed under the Apache License, Version 2.0. See LICENSE.apache at the
// repo root, or http://www.apache.org/licenses/LICENSE-2.0.

// checks.go carries the Sensor conformance check battery.

package main

import (
	"context"
	"fmt"
	"sync"
	"time"

	"google.golang.org/protobuf/types/known/emptypb"

	genv1 "github.com/fallguy/rimsky/protocols/proto/v1/gen"
)

// CheckResult is one row of conformance output.
type CheckResult struct {
	Name string
	Err  error
}

// RunOpts bundles the per-run inputs.
type RunOpts struct {
	// Kind names the sensor kind (cron, http, ...). Required.
	Kind string
	// ResolvedConfig is the per-watch config bytes the sensor parses
	// in StartWatch. Sensor-kind-specific.
	ResolvedConfig []byte
	// ObservationReceiver, when non-nil, is consulted by the
	// observation-push check. The receiver's WaitForObservation
	// method blocks up to the supplied timeout for the sensor to
	// POST. Out-of-process conformance runs don't set this (the
	// sensor's HTTP target is baked at startup, outside the
	// conformance binary's control); the in-process self-test sets
	// it.
	ObservationReceiver *ObservationReceiver
	// WatchID is the watch_id passed to StartWatch / StopWatch.
	// Defaults to a stable conformance value when empty.
	WatchID string
	// InstanceID echoed on StartWatch.
	InstanceID string
	// ObservationTimeout bounds the wait for the observation-push
	// check. Defaults to 5s.
	ObservationTimeout time.Duration
}

// RunSensorConformance drives the lifecycle + (optional) observation
// push checks against the supplied Sensor client.
func RunSensorConformance(ctx context.Context, c genv1.SensorClient, opts RunOpts) []CheckResult {
	if opts.WatchID == "" {
		opts.WatchID = "conformance-watch"
	}
	if opts.InstanceID == "" {
		opts.InstanceID = "conformance-instance"
	}
	if opts.ObservationTimeout == 0 {
		opts.ObservationTimeout = 5 * time.Second
	}
	results := make([]CheckResult, 0, 6)
	results = append(results, checkCapabilities(ctx, c, opts.Kind))
	results = append(results, checkStartWatch(ctx, c, opts))
	results = append(results, checkListWatches(ctx, c, opts))
	results = append(results, checkStartWatchIdempotent(ctx, c, opts))
	if opts.ObservationReceiver != nil {
		results = append(results, checkObservationPush(c, opts))
	}
	results = append(results, checkStopWatch(ctx, c, opts))
	results = append(results, checkStopWatchIdempotent(ctx, c, opts))
	return results
}

// checkCapabilities probes the Capabilities RPC and asserts the
// requested kind is in the supported set.
func checkCapabilities(ctx context.Context, c genv1.SensorClient, kind string) CheckResult {
	caps, err := c.Capabilities(ctx, &emptypb.Empty{})
	if err != nil {
		return CheckResult{Name: "Capabilities", Err: err}
	}
	if len(caps.GetSupportedKinds()) == 0 {
		return CheckResult{Name: "Capabilities", Err: fmt.Errorf("supported_kinds is empty")}
	}
	found := false
	for _, k := range caps.GetSupportedKinds() {
		if k.GetKind() == kind {
			found = true
			break
		}
	}
	if !found {
		return CheckResult{
			Name: "Capabilities",
			Err:  fmt.Errorf("kind %q not advertised in supported_kinds", kind),
		}
	}
	return CheckResult{Name: "Capabilities"}
}

// checkStartWatch fires StartWatch with the supplied opts.
func checkStartWatch(ctx context.Context, c genv1.SensorClient, opts RunOpts) CheckResult {
	if _, err := c.StartWatch(ctx, &genv1.StartWatchRequest{
		WatchId:        opts.WatchID,
		InstanceId:     opts.InstanceID,
		Kind:           opts.Kind,
		ResolvedConfig: opts.ResolvedConfig,
	}); err != nil {
		return CheckResult{Name: "StartWatch", Err: err}
	}
	return CheckResult{Name: "StartWatch"}
}

// checkListWatches pins that the just-started watch appears in
// ListWatches.
func checkListWatches(ctx context.Context, c genv1.SensorClient, opts RunOpts) CheckResult {
	resp, err := c.ListWatches(ctx, &emptypb.Empty{})
	if err != nil {
		return CheckResult{Name: "ListWatches", Err: err}
	}
	for _, w := range resp.GetWatches() {
		if w.GetWatchId() == opts.WatchID {
			if w.GetInstanceId() != opts.InstanceID {
				return CheckResult{
					Name: "ListWatches",
					Err:  fmt.Errorf("watch %q instance_id %q != expected %q", opts.WatchID, w.GetInstanceId(), opts.InstanceID),
				}
			}
			if w.GetKind() != opts.Kind {
				return CheckResult{
					Name: "ListWatches",
					Err:  fmt.Errorf("watch %q kind %q != expected %q", opts.WatchID, w.GetKind(), opts.Kind),
				}
			}
			return CheckResult{Name: "ListWatches"}
		}
	}
	return CheckResult{
		Name: "ListWatches",
		Err:  fmt.Errorf("watch %q not present in ListWatches after StartWatch", opts.WatchID),
	}
}

// checkStartWatchIdempotent re-fires StartWatch and asserts no
// error. Sensors are expected to treat duplicate StartWatch on a
// live watch_id as a no-op.
func checkStartWatchIdempotent(ctx context.Context, c genv1.SensorClient, opts RunOpts) CheckResult {
	if _, err := c.StartWatch(ctx, &genv1.StartWatchRequest{
		WatchId:        opts.WatchID,
		InstanceId:     opts.InstanceID,
		Kind:           opts.Kind,
		ResolvedConfig: opts.ResolvedConfig,
	}); err != nil {
		return CheckResult{Name: "StartWatchIdempotent", Err: err}
	}
	return CheckResult{Name: "StartWatchIdempotent"}
}

// checkObservationPush blocks for an observation arriving at the
// in-process receiver. Skipped when no receiver is configured.
func checkObservationPush(c genv1.SensorClient, opts RunOpts) CheckResult {
	ok := opts.ObservationReceiver.WaitForObservation(opts.WatchID, opts.ObservationTimeout)
	if !ok {
		return CheckResult{
			Name: "ObservationPush",
			Err: fmt.Errorf("no observation arrived at the receiver within %s for watch_id=%q",
				opts.ObservationTimeout, opts.WatchID),
		}
	}
	_ = c // observation arrival is enough; client unused beyond lifetime tracking
	return CheckResult{Name: "ObservationPush"}
}

// checkStopWatch fires StopWatch.
func checkStopWatch(ctx context.Context, c genv1.SensorClient, opts RunOpts) CheckResult {
	if _, err := c.StopWatch(ctx, &genv1.StopWatchRequest{WatchId: opts.WatchID}); err != nil {
		return CheckResult{Name: "StopWatch", Err: err}
	}
	return CheckResult{Name: "StopWatch"}
}

// checkStopWatchIdempotent re-fires StopWatch and asserts no error.
func checkStopWatchIdempotent(ctx context.Context, c genv1.SensorClient, opts RunOpts) CheckResult {
	if _, err := c.StopWatch(ctx, &genv1.StopWatchRequest{WatchId: opts.WatchID}); err != nil {
		return CheckResult{Name: "StopWatchIdempotent", Err: err}
	}
	return CheckResult{Name: "StopWatchIdempotent"}
}

// ObservationReceiver is the small in-process HTTP receiver the
// conformance harness uses to assert observations land. Sensors POST
// to ${endpoint}/sensors/{watch_id}/observations; the receiver
// records the arrival keyed by watch_id and unblocks waiters.
type ObservationReceiver struct {
	mu          sync.Mutex
	cond        *sync.Cond
	observedSet map[string]bool
}

// NewObservationReceiver constructs an empty receiver.
func NewObservationReceiver() *ObservationReceiver {
	r := &ObservationReceiver{observedSet: make(map[string]bool)}
	r.cond = sync.NewCond(&r.mu)
	return r
}

// NoteObservation records that watch_id fired and wakes any waiter.
func (r *ObservationReceiver) NoteObservation(watchID string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.observedSet[watchID] = true
	r.cond.Broadcast()
}

// WaitForObservation blocks up to timeout for an observation for
// watchID. Returns true on arrival, false on timeout.
func (r *ObservationReceiver) WaitForObservation(watchID string, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	r.mu.Lock()
	defer r.mu.Unlock()
	for !r.observedSet[watchID] {
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return false
		}
		// sync.Cond doesn't support a native deadline; run a watchdog
		// goroutine that wakes everyone after remaining.
		done := make(chan struct{})
		go func() {
			t := time.NewTimer(remaining)
			defer t.Stop()
			<-t.C
			r.mu.Lock()
			r.cond.Broadcast()
			r.mu.Unlock()
			close(done)
		}()
		r.cond.Wait()
		select {
		case <-done:
			// watchdog wake — re-check the predicate; if still
			// unsatisfied and the deadline passed, return false.
		default:
		}
	}
	return r.observedSet[watchID]
}
