// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: Apache-2.0

package conformance

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"google.golang.org/protobuf/types/known/structpb"

	"github.com/rimsky-ai/rimsky-core/lib/protocols/conformance/stubmode"
	genv1 "github.com/rimsky-ai/rimsky-core/lib/protocols/proto/v1/gen"
)

type Result struct {
	Scenario     string
	Passed       bool
	Skipped      bool
	StubGateSkip bool
	Error        string
	Duration     time.Duration
}

type RunnerOpts struct {
	Endpoint     Endpoint
	AllowLive    bool
	Only         []string
	Skip         []string
	Timeout      time.Duration
	CallbackBind string
	CallbackHost string
}

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

	stubOK, probeErr := ProbeStubMode(ctx, env, opts.Timeout)
	if !opts.AllowLive {
		if probeErr != nil {
			return nil, fmt.Errorf("stub-mode probe failed: %w — refusing to run scenarios against an endpoint not proven to be in stub mode; pass --allow-live to run against a live executor (stub-requiring scenarios then skip)", probeErr)
		}
		if !stubOK {
			return nil, fmt.Errorf("executor not in stub mode — refusing to run scenarios against a live endpoint; pass --allow-live to override (stub-requiring scenarios then skip)")
		}
	}
	stubSkipReason := "stub mode required"
	if probeErr != nil {
		stubSkipReason = fmt.Sprintf("stub mode required (stub-mode probe failed: %v)", probeErr)
	}
	asyncSupport := probeAsyncSupport(ctx, env, opts.Timeout)

	all := All()
	if unknown := unknownFilterNames(opts.Only, opts.Skip, all); len(unknown) > 0 {
		return nil, fmt.Errorf("conformance: unknown scenario name(s) in --scenarios/--skip: %s", strings.Join(unknown, ", "))
	}

	results := []Result{}
	for _, sc := range all {
		if skipMatch(sc.Name, opts.Only, opts.Skip) {
			results = append(results, Result{Scenario: sc.Name, Skipped: true})
			continue
		}
		if sc.RequiresStub && !stubOK {
			results = append(results, Result{Scenario: sc.Name, Skipped: true, StubGateSkip: true, Error: stubSkipReason})
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

func ProbeStubMode(ctx context.Context, env Env, timeout time.Duration) (bool, error) {
	pctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	ud, _ := structpb.NewStruct(map[string]any{stubmode.ProbeAttribute: true})
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
		if stubmode.ConfirmsStub(succ.Success.GetAttributesDelta().AsMap()) {
			return true, nil
		}
	}
	return false, nil
}

func probeAsyncSupport(ctx context.Context, env Env, timeout time.Duration) bool {
	pctx, cancel := context.WithTimeout(ctx, timeout/3)
	defer cancel()
	ud, _ := structpb.NewStruct(map[string]any{stubmode.AsyncProbeAttribute: true})
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

func unknownFilterNames(only, skip []string, all []Scenario) []string {
	known := make(map[string]bool, len(all))
	for _, sc := range all {
		known[sc.Name] = true
	}
	var unknown []string
	for _, s := range append(append([]string{}, only...), skip...) {
		if !known[s] {
			unknown = append(unknown, s)
		}
	}
	return unknown
}

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
			if r.Error == "" {
				fmt.Fprintf(w, "[%s] %s\n", status, r.Scenario)
			} else {
				fmt.Fprintf(w, "[%s] %s (%s)\n", status, r.Scenario, r.Error)
			}
		} else {
			fmt.Fprintf(w, "[%s] %s (%.3fs) %s\n", status, r.Scenario, r.Duration.Seconds(), r.Error)
		}
	}
	fmt.Fprintf(w, "\n%d passed, %d failed, %d skipped\n", passed, failed, skipped)
}
