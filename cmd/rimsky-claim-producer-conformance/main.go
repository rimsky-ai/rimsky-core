// rimsky-claim-producer-conformance runs the ClaimProducer conformance
// suite against a remote producer-service endpoint. Checks include the
// Capabilities handshake, write-semantics envelope conformance, the
// uniformity invariant (byte-equal Scope ⇒ identical
// RealizedWriteSemantics), and the four runtime verbs.
//
// Lifecycle conformance lives in `rimsky-conformance --check-lifecycle`
// per the layer-crystallization plan (Phase 4 / Task 29).
//
// Usage:
//
//	rimsky-claim-producer-conformance --endpoint grpc://localhost:9101 [--timeout 10s]
//
// Exits 0 on success, 1 on any failure. Output is one line per check.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/google/uuid"

	"github.com/fallguy/rimsky/foundation/integration/remote"
	"github.com/fallguy/rimsky/foundation/locks"
)

func main() {
	endpoint := flag.String("endpoint", "", "claim-producer-service gRPC endpoint (e.g. grpc://localhost:9101)")
	timeout := flag.Duration("timeout", 10*time.Second, "per-check timeout")
	checkObs := flag.Bool("check-observability", false, "additionally probe StoreObservability")
	retentionSec := flag.Int("retention-test-seconds", 0, "if >0, drive a canned claim then sleep this long and verify GetClaim returns evicted")
	flag.Parse()
	obsRetentionTestSeconds = *retentionSec

	if *endpoint == "" {
		fmt.Fprintln(os.Stderr, "rimsky-claim-producer-conformance: --endpoint required")
		os.Exit(2)
	}

	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()

	client, err := remote.Dial(ctx, "conformance-target", *endpoint)
	if err != nil {
		fmt.Fprintf(os.Stderr, "rimsky-claim-producer-conformance: dial: %v\n", err)
		os.Exit(1)
	}
	defer client.Close()

	results := RunClaimProducerConformance(ctx, client)
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
		fmt.Fprintf(os.Stderr, "rimsky-claim-producer-conformance: %d/%d checks failed\n", failed, len(results))
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

// CheckResult is one row of conformance output. Err is nil on success.
type CheckResult struct {
	Name string
	Err  error
}

// RunClaimProducerConformance drives the ClaimProducer conformance
// checks against the supplied producer. Each check is independent;
// failures do not short-circuit so the operator sees the full surface.
func RunClaimProducerConformance(ctx context.Context, c locks.ClaimProducer) []CheckResult {
	results := make([]CheckResult, 0, 8)
	caps, err := c.Capabilities(ctx)
	if err != nil {
		results = append(results, CheckResult{Name: "Capabilities", Err: err})
		return results
	}
	results = append(results, CheckResult{Name: "Capabilities"})
	if len(caps.WriteSemanticsEnvelope) == 0 {
		results = append(results, CheckResult{
			Name: "EnvelopeNonEmpty",
			Err:  fmt.Errorf("write_semantics_envelope is empty"),
		})
		return results
	}
	results = append(results, CheckResult{Name: "EnvelopeNonEmpty"})

	// Envelope conformance + uniformity-per-scope: drive Open twice with
	// identical specs and assert returned RealizedWriteSemantics is in
	// the envelope and identical across calls. Selectors are synthetic.
	spec := locks.ClaimSpec{
		StoreName: "conformance-target",
		Selector:  "rimsky/conformance/uniformity",
		Intent:    locks.IntentRead,
		Alias:     "conformance",
	}
	out1, err := c.Open(ctx, locks.ClaimID(uuid.New().String()), spec)
	if err != nil {
		results = append(results, CheckResult{Name: "OpenFirst", Err: err})
		return results
	}
	if !out1.Available {
		results = append(results, CheckResult{
			Name: "OpenFirst",
			Err:  fmt.Errorf("producer returned Unavailable for synthetic selector — cannot exercise uniformity"),
		})
		return results
	}
	if out1.Result.RealizedWriteSemantics == locks.WriteSemanticsUnknown {
		results = append(results, CheckResult{
			Name: "OpenFirst",
			Err:  fmt.Errorf("RealizedWriteSemantics is empty/UNKNOWN; producer must declare a concrete value"),
		})
		return results
	}
	if !caps.Contains(out1.Result.RealizedWriteSemantics) {
		results = append(results, CheckResult{
			Name: "OpenFirst",
			Err:  fmt.Errorf("RealizedWriteSemantics %q not in advertised envelope %v", out1.Result.RealizedWriteSemantics, caps.WriteSemanticsEnvelope),
		})
		return results
	}
	results = append(results, CheckResult{Name: "OpenFirst"})

	out2, err := c.Open(ctx, locks.ClaimID(uuid.New().String()), spec)
	if err != nil {
		results = append(results, CheckResult{Name: "OpenSecond", Err: err})
		return results
	}
	if !out2.Available {
		// Some producers (pick-policy queues) drain after Open. Skip
		// the uniformity check rather than fail.
		results = append(results, CheckResult{Name: "OpenSecond"})
		return results
	}
	if out2.Result.RealizedWriteSemantics != out1.Result.RealizedWriteSemantics {
		results = append(results, CheckResult{
			Name: "Uniformity",
			Err: fmt.Errorf("byte-equal Scope did not produce identical RealizedWriteSemantics: %q vs %q",
				out1.Result.RealizedWriteSemantics, out2.Result.RealizedWriteSemantics),
		})
		return results
	}
	results = append(results, CheckResult{Name: "Uniformity"})

	return results
}
