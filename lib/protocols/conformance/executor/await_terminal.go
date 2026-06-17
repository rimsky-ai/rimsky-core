// Copyright © 2026 Fall Guy Consulting.
// Licensed under the Apache License, Version 2.0. See LICENSE.apache at the
// repo root, or http://www.apache.org/licenses/LICENSE-2.0.

package conformance

import (
	"context"
	"errors"
	"fmt"

	genv1 "github.com/rimsky-ai/rimsky-core/lib/protocols/proto/v1/gen"
)

// AwaitTerminal turns a unary Execute call into a settling Outcome
// (Success / Error / Park). When the unary outcome is
// AwaitAsyncCallback AND env.Callbacks is configured, it registers the
// executor-minted async_ack_id with the receiver and waits on the
// resulting channel for the eventual callback POST. The returned
// Outcome reflects the settling verdict.
//
// Returns the AwaitAsyncCallback outcome verbatim when env.Callbacks
// is nil.
func AwaitTerminal(ctx context.Context, outcome *genv1.Outcome, env Env) (*genv1.Outcome, error) {
	if outcome == nil {
		return nil, errors.New("AwaitTerminal: nil outcome")
	}
	await, isAsync := outcome.GetOutcome().(*genv1.Outcome_AwaitAsync)
	if !isAsync {
		return outcome, nil
	}
	if env.Callbacks == nil {
		return outcome, nil
	}
	ackID := await.AwaitAsync.GetAsyncAckId()
	if ackID == "" {
		return nil, errors.New("AwaitAsyncCallback with empty async_ack_id; cannot route callback")
	}
	ch := env.Callbacks.Register(ackID)
	select {
	case cb := <-ch:
		if cb == nil {
			return outcome, nil
		}
		return cb, nil
	case <-ctx.Done():
		return nil, fmt.Errorf("await callback for %s: %w", ackID, ctx.Err())
	}
}
