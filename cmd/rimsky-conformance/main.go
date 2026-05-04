// rimsky-conformance runs the protocol conformance suite against a live
// node-executor endpoint. Any executor speaking gRPC (canonical) or the
// HTTP+JSON bridge can be validated.
//
// Two complementary modes (post-Phase-4):
//
//   - default: executor protocol scenarios.
//   - --check-lifecycle: lifecycle protocol six-RPC sanity probe against
//     a peer that implements LifecycleSubscriber (typically a producer
//     binary or an executor binary that opts in). Exits non-zero on
//     any RPC error.
//
// Usage:
//
//	rimsky-conformance --endpoint localhost:9091 --transport grpc \
//	                   [--require-stub-mode] [--scenarios name1,name2] \
//	                   [--skip nameA] [--timeout 30s]
//	rimsky-conformance --check-lifecycle --endpoint grpc://localhost:7100
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/fallguy/rimsky/conformance"
	_ "github.com/fallguy/rimsky/conformance/scenarios" // init() registration
	"github.com/fallguy/rimsky/modeling/executor"
)

func main() {
	endpoint := flag.String("endpoint", "", "endpoint URL (executor or lifecycle peer)")
	transport := flag.String("transport", "grpc", "grpc|http")
	requireStub := flag.Bool("require-stub-mode", false, "fail if executor not in stub mode")
	only := flag.String("scenarios", "", "comma-list of scenario names to run (default: all)")
	skip := flag.String("skip", "", "comma-list of scenario names to skip")
	timeout := flag.Duration("timeout", 30*time.Second, "per-scenario timeout")
	checkObs := flag.Bool("check-observability", false, "additionally probe ExecutorObservability per spec §6")
	retentionSec := flag.Int("retention-test-seconds", 0, "if >0, drive a canned dispatch then sleep this long and verify GetTrace returns evicted=true (spec §6 retention check)")
	checkLifecycle := flag.Bool("check-lifecycle", false, "probe LifecycleSubscriber six-RPC sanity instead of running executor scenarios")
	flag.Parse()
	obsRetentionTestSeconds = *retentionSec

	if *endpoint == "" {
		fmt.Fprintln(os.Stderr, "rimsky-conformance: --endpoint required")
		os.Exit(2)
	}

	ctx := context.Background()

	if *checkLifecycle {
		if err := runLifecycleCheck(ctx, *endpoint, *timeout); err != nil {
			fmt.Fprintf(os.Stderr, "lifecycle: %v\n", err)
			os.Exit(1)
		}
		fmt.Fprintln(os.Stdout, "lifecycle: ok")
		return
	}

	ep := executor.Endpoint{Transport: *transport, URL: *endpoint}

	onlyList := splitCSV(*only)
	skipList := splitCSV(*skip)

	results, err := conformance.Run(ctx, conformance.RunnerOpts{
		Endpoint:        ep,
		RequireStubMode: *requireStub,
		Only:            onlyList,
		Skip:            skipList,
		Timeout:         *timeout,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "conformance: %v\n", err)
		os.Exit(1)
	}

	conformance.Summary(results, os.Stdout)
	for _, r := range results {
		if !r.Passed && !r.Skipped {
			os.Exit(1)
		}
	}

	if *checkObs {
		if err := runObservabilityCheck(ctx, ep, *requireStub); err != nil {
			fmt.Fprintf(os.Stderr, "observability: %v\n", err)
			os.Exit(1)
		}
		fmt.Fprintln(os.Stdout, "observability: ok")
	}
}

func splitCSV(s string) []string {
	if s == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	for i := range parts {
		parts[i] = strings.TrimSpace(parts[i])
	}
	return parts
}
