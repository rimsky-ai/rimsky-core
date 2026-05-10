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
	CallbackBind    string        // BindHost for the callback receiver (default "127.0.0.1")
	CallbackHost    string        // AdvertiseHost for the callback receiver (default same as BindHost)
}

// Run dials the endpoint, starts a CallbackReceiver, probes capabilities, and
// executes every registered scenario (subject to Only/Skip filters). Returns
// one Result per scenario.
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
			return nil, fmt.Errorf("executor not in stub mode (probe returned non-stub response)")
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

// probeStubMode sends a stub-probe Execute and returns true iff the resulting
// terminal carries `attributes_delta = {stub: true}`. AwaitTerminal handles
// async executors by following the callback when AsyncAccepted is observed.
//
// Callers that depend on a definite stub-mode answer (e.g.
// `--require-stub-mode`) MUST inspect the returned error. A nil error +
// false means "executor responded but did not advertise stub mode"; a
// non-nil error means we never got a clean answer (connection failure,
// timeout) and the result is indeterminate.
func probeStubMode(ctx context.Context, env Env, timeout time.Duration) (bool, error) {
	pctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	ud, _ := structpb.NewStruct(map[string]any{"stub_probe": true})
	req := &genv1.ExecuteRequest{
		NodeId: "probe", InstanceId: "probe", NodeType: "conformance-probe",
		Userdata:    ud,
		CallbackUrl: env.Callbacks.URL(),
	}
	stream, err := env.Client.Execute(pctx, req)
	if err != nil {
		return false, err
	}
	defer stream.Close()
	ev, err := AwaitTerminal(pctx, stream, env)
	if err != nil {
		// AwaitTerminal failures are indeterminate — the caller
		// (`--require-stub-mode`) MUST treat this as a probe failure
		// rather than "executor isn't stubbed". Returning (false, nil)
		// here would let a non-stub executor pass the require gate when
		// the probe RPC merely timed out.
		return false, err
	}
	if ce, ok := ev.Event.(*genv1.ExecuteEvent_Complete); ok {
		m := ce.Complete.GetAttributesDelta().AsMap()
		if v, ok := m["stub"].(bool); ok && v {
			return true, nil
		}
	}
	return false, nil
}

// probeAsyncSupport sends an Execute with userdata.probe_async=true and
// returns true iff the executor responds with AsyncAccepted on the gRPC
// stream. Unlike the regular terminal-await flow, this probe deliberately
// stops at the gRPC terminal — receipt of AsyncAccepted IS the signal we are
// looking for.
func probeAsyncSupport(ctx context.Context, env Env, timeout time.Duration) bool {
	pctx, cancel := context.WithTimeout(ctx, timeout/3)
	defer cancel()
	ud, _ := structpb.NewStruct(map[string]any{"probe_async": true})
	req := &genv1.ExecuteRequest{
		NodeId: "probe-async", InstanceId: "probe", NodeType: "conformance-probe-async",
		Userdata:    ud,
		CallbackUrl: env.Callbacks.URL(),
	}
	stream, err := env.Client.Execute(pctx, req)
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
