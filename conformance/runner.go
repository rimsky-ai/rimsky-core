// Copyright © 2026 Fall Guy Consulting.
// Licensed under the Apache License, Version 2.0. See LICENSE.apache at the
// repo root, or http://www.apache.org/licenses/LICENSE-2.0.

package conformance

import (
	"context"
	"fmt"
	"os"
	"time"

	"google.golang.org/protobuf/types/known/structpb"

	"github.com/fallguy/rimsky/modeling/executor"
	genv1 "github.com/fallguy/rimsky/protocols/proto/v1/gen"
)

// Result is the outcome of running a single Scenario.
type Result struct {
	Scenario string
	Passed   bool
	Skipped  bool
	Error    string
	Duration time.Duration
}

// RunnerOpts configures a single conformance run.
type RunnerOpts struct {
	Endpoint        executor.Endpoint
	RequireStubMode bool          // if true, probe must return {stub:true}; else fail
	Only            []string      // run only these scenario names
	Skip            []string      // skip these scenario names
	Timeout         time.Duration // per-scenario; default 30s
}

// Run dials the endpoint, probes capabilities, and executes every registered
// scenario (subject to Only/Skip filters). Returns one Result per scenario.
func Run(ctx context.Context, opts RunnerOpts) ([]Result, error) {
	if opts.Timeout == 0 {
		opts.Timeout = 30 * time.Second
	}
	pool := executor.NewClientPool()
	defer pool.Close()
	client, err := pool.GetOrCreate(opts.Endpoint)
	if err != nil {
		return nil, fmt.Errorf("dial: %w", err)
	}

	// Stub-mode probe.
	stubOK, err := probeStubMode(ctx, client, opts.Timeout)
	if opts.RequireStubMode {
		if err != nil {
			return nil, fmt.Errorf("stub-mode probe failed: %w", err)
		}
		if !stubOK {
			return nil, fmt.Errorf("executor not in stub mode (probe returned non-stub response)")
		}
	}

	// Detect async-handoff capability via a second probe.
	asyncSupport := probeAsyncSupport(ctx, client, opts.Timeout)

	results := []Result{}
	for _, sc := range All() {
		if skipMatch(sc.Name, opts.Only, opts.Skip) {
			results = append(results, Result{Scenario: sc.Name, Skipped: true})
			continue
		}
		if sc.RequiresStub && !stubOK {
			results = append(results, Result{Scenario: sc.Name, Skipped: true, Error: "stub mode required"})
			continue
		}
		if sc.RequiresAsync && !asyncSupport {
			results = append(results, Result{Scenario: sc.Name, Skipped: true, Error: "async handoff required"})
			continue
		}
		scenarioCtx, cancel := context.WithTimeout(ctx, opts.Timeout)
		start := time.Now()
		err := sc.Run(scenarioCtx, client)
		cancel()
		r := Result{Scenario: sc.Name, Duration: time.Since(start), Passed: err == nil}
		if err != nil {
			r.Error = err.Error()
		}
		results = append(results, r)
	}
	return results, nil
}

// probeStubMode sends a stub-probe Execute and returns true iff the result has
// a top-level {"stub": true} boolean. Returns false, nil if not in stub mode
// (i.e. executor answered but without stub:true). Returns err only on RPC failure.
func probeStubMode(ctx context.Context, c executor.Client, timeout time.Duration) (bool, error) {
	pctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	ud, _ := structpb.NewStruct(map[string]any{"stub_probe": true})
	req := &genv1.ExecuteRequest{NodeId: "probe", InstanceId: "probe", NodeType: "conformance-probe", Userdata: ud}
	stream, err := c.Execute(pctx, req)
	if err != nil {
		return false, err
	}
	defer stream.Close()
	for {
		ev, err := stream.Recv()
		if err != nil {
			break
		}
		if ce, ok := ev.Event.(*genv1.ExecuteEvent_Complete); ok {
			// Stub mode now signals via attributes_delta — Complete.Result
			// was removed in the §12 protocol rewrite (terminal-final
			// attribute writeback replaces the old result field).
			m := ce.Complete.GetAttributesDelta().AsMap()
			if v, ok := m["stub"].(bool); ok && v {
				return true, nil
			}
			return false, nil
		}
		if _, ok := ev.Event.(*genv1.ExecuteEvent_Errored); ok {
			// Not a stub-mode response; treat as non-stub.
			return false, nil
		}
	}
	return false, nil
}

// probeAsyncSupport sends an Execute with userdata.probe_async=true and
// returns true iff the executor responds with AsyncAccepted.
func probeAsyncSupport(ctx context.Context, c executor.Client, timeout time.Duration) bool {
	// Short-timeout probe that won't block on a real async flow.
	pctx, cancel := context.WithTimeout(ctx, timeout/3)
	defer cancel()
	ud, _ := structpb.NewStruct(map[string]any{"probe_async": true})
	req := &genv1.ExecuteRequest{NodeId: "probe-async", InstanceId: "probe", NodeType: "conformance-probe-async", Userdata: ud}
	stream, err := c.Execute(pctx, req)
	if err != nil {
		return false
	}
	defer stream.Close()
	for {
		ev, err := stream.Recv()
		if err != nil {
			break
		}
		if _, ok := ev.Event.(*genv1.ExecuteEvent_AsyncAccepted); ok {
			return true
		}
		if _, ok := ev.Event.(*genv1.ExecuteEvent_Complete); ok {
			return false
		}
		if _, ok := ev.Event.(*genv1.ExecuteEvent_Errored); ok {
			return false
		}
	}
	return false
}

func skipMatch(name string, only, skip []string) bool {
	for _, s := range skip {
		if s == name {
			return true
		}
	}
	if len(only) == 0 {
		return false
	}
	for _, s := range only {
		if s == name {
			return false
		}
	}
	return true
}

// Summary is a pretty-printing helper used by the CLI.
func Summary(results []Result, w *os.File) {
	passed, failed, skipped := 0, 0, 0
	for _, r := range results {
		status := "PASS"
		if r.Skipped {
			status = "SKIP"
			skipped++
		}
		if !r.Passed && !r.Skipped {
			status = "FAIL"
			failed++
		}
		if r.Passed {
			passed++
		}
		if r.Skipped {
			fmt.Fprintf(w, "[%s] %s (%s)\n", status, r.Scenario, r.Error)
		} else {
			fmt.Fprintf(w, "[%s] %s (%.3fs) %s\n", status, r.Scenario, r.Duration.Seconds(), r.Error)
		}
	}
	fmt.Fprintf(w, "\n%d passed, %d failed, %d skipped\n", passed, failed, skipped)
}
