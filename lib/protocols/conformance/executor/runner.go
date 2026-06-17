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

	genv1 "github.com/rimsky-ai/rimsky-core/lib/protocols/proto/v1/gen"
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
	Endpoint        Endpoint
	RequireStubMode bool
	Only            []string
	Skip            []string
	Timeout         time.Duration
	CallbackBind    string
	CallbackHost    string
}

// Run dials the endpoint, starts a CallbackReceiver, probes
// capabilities (stub mode and async-callback support), and executes
// every scenario registered via All() — subject to the Only/Skip
// filters and the per-scenario RequiresStub / RequiresAsync gates.
// Returns one Result per scenario carrying pass / fail / skipped
// state plus the failure error string when failed.
//
// @concept: conformance
func Run(ctx context.Context, opts RunnerOpts) ([]Result, error) {
	if opts.Timeout == 0 {
		opts.Timeout = 30 * time.Second
	}
	pool := NewClientPool()
	defer pool.Close()
	client, err := pool.GetOrCreate(opts.Endpoint)
	if err != nil {
		return nil, fmt.Errorf("dial: %w", err)
	}
	receiver, err := StartCallbackReceiver(ReceiverOptions{
		BindHost:      opts.CallbackBind,
		AdvertiseHost: opts.CallbackHost,
	})
	if err != nil {
		return nil, err
	}
	defer func() { _ = receiver.Close() }()
	env := Env{Client: client, Callbacks: receiver}

	stubOK, err := probeStubMode(ctx, env, opts.Timeout)
	if opts.RequireStubMode {
		if err != nil {
			return nil, fmt.Errorf("stub-mode probe failed: %w", err)
		}
		if !stubOK {
			return nil, fmt.Errorf("executor not in stub mode")
		}
	}
	asyncSupport := probeAsyncSupport(ctx, env, opts.Timeout)

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
		err := sc.Run(scenarioCtx, env)
		cancel()
		r := Result{Scenario: sc.Name, Duration: time.Since(start), Passed: err == nil}
		if err != nil {
			r.Error = err.Error()
		}
		results = append(results, r)
	}
	return results, nil
}

// probeStubMode sends a stub-probe Execute and returns true iff the
// resulting Success outcome carries `attributes_delta = {stub: true}`.
func probeStubMode(ctx context.Context, env Env, timeout time.Duration) (bool, error) {
	pctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	ud, _ := structpb.NewStruct(map[string]any{"stub_probe": true})
	req := &genv1.ExecuteRequest{
		NodeId: "probe", InstanceId: "probe", NodeType: "conformance-probe",
		Attributes:  ud,
		CallbackUrl: env.Callbacks.URL(),
	}
	outcome, err := env.Client.Execute(pctx, req)
	if err != nil {
		return false, err
	}
	settled, err := AwaitTerminal(pctx, outcome, env)
	if err != nil {
		return false, err
	}
	if succ, ok := settled.GetOutcome().(*genv1.Outcome_Success); ok {
		m := succ.Success.GetAttributesDelta().AsMap()
		if v, ok := m["stub"].(bool); ok && v {
			return true, nil
		}
	}
	return false, nil
}

// probeAsyncSupport sends an Execute with attributes.probe_async=true
// and returns true iff the executor responds with AwaitAsyncCallback.
func probeAsyncSupport(ctx context.Context, env Env, timeout time.Duration) bool {
	pctx, cancel := context.WithTimeout(ctx, timeout/3)
	defer cancel()
	ud, _ := structpb.NewStruct(map[string]any{"probe_async": true})
	req := &genv1.ExecuteRequest{
		NodeId: "probe-async", InstanceId: "probe", NodeType: "conformance-probe-async",
		Attributes:  ud,
		CallbackUrl: env.Callbacks.URL(),
	}
	outcome, err := env.Client.Execute(pctx, req)
	if err != nil {
		return false
	}
	_, isAsync := outcome.GetOutcome().(*genv1.Outcome_AwaitAsync)
	return isAsync
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
