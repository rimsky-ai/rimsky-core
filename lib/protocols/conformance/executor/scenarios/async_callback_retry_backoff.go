// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: Apache-2.0

package scenarios

import (
	"context"
	"fmt"
	"time"

	"google.golang.org/protobuf/types/known/structpb"

	conformance "github.com/rimsky-ai/rimsky-core/lib/protocols/conformance/executor"
	"github.com/rimsky-ai/rimsky-core/lib/protocols/conformance/stubmode"
	genv1 "github.com/rimsky-ai/rimsky-core/lib/protocols/proto/v1/gen"
)

const backoffAttemptsRequired = 3

const backoffGrowthRequired = 2

// @concept: executor
// @decision: async-callback-persistent-registry
func init() {
	conformance.Register(conformance.Scenario{
		Name:          "async_callback_retry_backoff",
		RequiresStub:  true,
		RequiresAsync: true,
		Run: func(ctx context.Context, env conformance.Env) error {
			attrs, err := structpb.NewStruct(map[string]any{stubmode.AsyncProbeAttribute: true})
			if err != nil {
				return fmt.Errorf("build attributes: %w", err)
			}
			req := &genv1.ExecuteRequest{
				NodeId:      "async-callback-retry-backoff",
				InstanceId:  "async-callback-retry-backoff",
				NodeType:    "conformance",
				Attributes:  attrs,
				CallbackUrl: env.Callbacks.URL(),
			}

			refused := env.Callbacks.SimulateRestart()

			outcome, err := env.Client.Execute(ctx, req)
			if err != nil {
				return fmt.Errorf("Execute: %w", err)
			}
			await, ok := outcome.GetOutcome().(*genv1.Outcome_AwaitAsync)
			if !ok {
				return fmt.Errorf("expected Outcome_AwaitAsync, got %T", outcome.GetOutcome())
			}
			ackID := await.AwaitAsync.GetAsyncAckId()
			if ackID == "" {
				return fmt.Errorf("AwaitAsyncCallback carried empty async_ack_id")
			}

			for seen := 0; seen < backoffAttemptsRequired; {
				select {
				case rejected := <-refused:
					if rejected == ackID {
						seen++
					}
				case <-ctx.Done():
					return fmt.Errorf("the receiver refused this dispatch's callback and saw fewer than %d "+
						"delivery attempts; an executor whose callback is refused must keep retrying",
						backoffAttemptsRequired)
				}
			}
			env.Callbacks.EndSimulatedRestart()

			if _, err := conformance.AwaitTerminal(ctx, outcome, env); err != nil {
				return fmt.Errorf("the executor never delivered its callback once the receiver accepted again: %w", err)
			}
			return checkRetryBackoff(env.Callbacks.AttemptTimes(ackID))
		},
	})
}

// @concept: executor
func checkRetryBackoff(attempts []time.Time) error {
	if len(attempts) < backoffAttemptsRequired {
		return fmt.Errorf("recorded %d callback attempts, want at least %d to read the retry cadence",
			len(attempts), backoffAttemptsRequired)
	}
	gaps := make([]time.Duration, 0, len(attempts)-1)
	for i := 1; i < len(attempts); i++ {
		gaps = append(gaps, attempts[i].Sub(attempts[i-1]))
	}
	for i := 1; i < len(gaps); i++ {
		if gaps[i] < gaps[i-1] {
			return fmt.Errorf("retry gap %d (%s) is shorter than gap %d (%s); a refused callback is retried "+
				"on a widening cadence, so the gap between attempts never shrinks",
				i+1, gaps[i], i, gaps[i-1])
		}
	}
	if gaps[len(gaps)-1] < time.Duration(backoffGrowthRequired)*gaps[0] {
		return fmt.Errorf("the last retry gap (%s) is under %dx the first (%s); a refused callback is retried "+
			"with backoff, not on a fixed cadence",
			gaps[len(gaps)-1], backoffGrowthRequired, gaps[0])
	}
	return nil
}
