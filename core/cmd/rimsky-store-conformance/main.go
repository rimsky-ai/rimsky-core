// rimsky-store-conformance runs the six lifecycle conformance checks
// against a remote store-service endpoint. Each check sends one of the
// lifecycle RPCs with synthetic IDs and asserts the call returns
// without error and with an empty response.
//
// This is the rimsky-side dual of `rimsky-conformance` (which targets
// node-executors). Per docs/specs/2026-05-01-control-plane-and-store-
// lifecycle-design.md §4.1: every store-service implements all six
// lifecycle methods. Stores that have nothing to do at one or more
// scopes return success immediately. The conformance suite verifies
// that uniform-no-op contract.
//
// Usage:
//
//	rimsky-store-conformance --endpoint grpc://localhost:9101 [--timeout 10s]
//
// Exits 0 on success, 1 on any failure. Output is one line per check.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/fallguy/rimsky/core/store/remote"
)

func main() {
	endpoint := flag.String("endpoint", "", "store-service gRPC endpoint (e.g. grpc://localhost:9101)")
	timeout := flag.Duration("timeout", 10*time.Second, "per-check timeout")
	checkObs := flag.Bool("check-observability", false, "additionally probe StoreObservability per spec §6")
	retentionSec := flag.Int("retention-test-seconds", 0, "if >0, drive a canned claim then sleep this long and verify GetClaim returns evicted (spec §6 retention check)")
	flag.Parse()
	obsRetentionTestSeconds = *retentionSec

	if *endpoint == "" {
		fmt.Fprintln(os.Stderr, "rimsky-store-conformance: --endpoint required")
		os.Exit(2)
	}

	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()

	client, err := remote.Dial(ctx, "conformance-target", *endpoint)
	if err != nil {
		fmt.Fprintf(os.Stderr, "rimsky-store-conformance: dial: %v\n", err)
		os.Exit(1)
	}
	defer client.Close()

	results := RunLifecycleConformance(ctx, client)
	failed := 0
	for _, r := range results {
		if r.Err != nil {
			failed++
			fmt.Printf("FAIL  %s: %v\n", r.Name, r.Err)
			continue
		}
		fmt.Printf("ok    %s\n", r.Name)
	}
	if failed > 0 {
		fmt.Fprintf(os.Stderr, "rimsky-store-conformance: %d/%d checks failed\n", failed, len(results))
		os.Exit(1)
	}

	if *checkObs {
		if err := runObservabilityCheck(ctx, *endpoint); err != nil {
			fmt.Fprintf(os.Stderr, "observability: %v\n", err)
			os.Exit(1)
		}
		fmt.Fprintln(os.Stdout, "observability: ok")
	}
}

// LifecycleClient is the subset of store.Store required by the
// lifecycle conformance suite. The remote.Client satisfies it, as does
// any in-process implementation drivable from a test.
type LifecycleClient interface {
	OnTemplateRegistered(ctx context.Context, templateID string) error
	OnTemplateDeployed(ctx context.Context, templateID string) error
	OnTemplateUndeployed(ctx context.Context, templateID string) error
	OnTemplateDeregistered(ctx context.Context, templateID string) error
	OnInstanceCreated(ctx context.Context, templateID, instanceID string) error
	OnInstanceTerminated(ctx context.Context, templateID, instanceID string) error
}

// CheckResult is one row of conformance output. Err is nil on success.
type CheckResult struct {
	Name string
	Err  error
}

// Synthetic IDs used by every check. The 64-char-`a` template hash is
// shape-valid (sha256-<64-hex>) but not registered anywhere — stores
// that ignore lifecycle events accept it as opaque text. The instance
// UUID is a fixed-shape sentinel; conformance does not require the
// store to track these values across calls.
const (
	syntheticTemplateID = "sha256-" + strings64A
	syntheticInstanceID = "00000000-0000-0000-0000-000000000001"
	strings64A          = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
)

// RunLifecycleConformance drives all six lifecycle checks against the
// supplied client. Each check is independent; failures do not short-
// circuit subsequent checks so the operator sees the full surface.
func RunLifecycleConformance(ctx context.Context, c LifecycleClient) []CheckResult {
	results := make([]CheckResult, 0, 6)
	results = append(results, CheckResult{
		Name: "OnTemplateRegistered",
		Err:  wrapVerb("OnTemplateRegistered", c.OnTemplateRegistered(ctx, syntheticTemplateID)),
	})
	results = append(results, CheckResult{
		Name: "OnTemplateDeployed",
		Err:  wrapVerb("OnTemplateDeployed", c.OnTemplateDeployed(ctx, syntheticTemplateID)),
	})
	results = append(results, CheckResult{
		Name: "OnTemplateUndeployed",
		Err:  wrapVerb("OnTemplateUndeployed", c.OnTemplateUndeployed(ctx, syntheticTemplateID)),
	})
	results = append(results, CheckResult{
		Name: "OnTemplateDeregistered",
		Err:  wrapVerb("OnTemplateDeregistered", c.OnTemplateDeregistered(ctx, syntheticTemplateID)),
	})
	results = append(results, CheckResult{
		Name: "OnInstanceCreated",
		Err:  wrapVerb("OnInstanceCreated", c.OnInstanceCreated(ctx, syntheticTemplateID, syntheticInstanceID)),
	})
	results = append(results, CheckResult{
		Name: "OnInstanceTerminated",
		Err:  wrapVerb("OnInstanceTerminated", c.OnInstanceTerminated(ctx, syntheticTemplateID, syntheticInstanceID)),
	})
	return results
}

func wrapVerb(verb string, err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("%s: %w", verb, err)
}
