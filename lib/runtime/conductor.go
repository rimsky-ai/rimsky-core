// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package runtime

import (
	"context"
	"time"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/events"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
)

// @decision: three-dispatch-deadlines
type ConductorArgs struct {
	Persist               persistence.Tables
	Queue                 persistence.Queue
	Clock                 shared.Clock
	Logger                shared.Logger
	MaxQuietPeriodDefault time.Duration
	MaxRuntimeDefault     time.Duration
}

// @decision: three-dispatch-deadlines
func SweepExecutorDeadlines(ctx context.Context, args ConductorArgs) error {
	log := args.Logger
	if log == nil {
		log = shared.SilentLogger{}
	}
	now := args.Clock.Now()
	candidates, err := args.Queue.ListOrphanedClaims(ctx)
	if err != nil {
		return err
	}
	for _, o := range candidates {
		nodeID := o.NodeID
		prior := ""
		if o.ClaimedBy != nil {
			prior = *o.ClaimedBy
		}
		errorClass, lastProgress, releaseReason := decideExecutorDeadlineRelease(o, now)
		if errorClass == "" {
			continue
		}
		// @concept: node-run
		if err := args.Queue.ReleaseClaimWithDisposition(ctx, o.ID, prior, "stale_recovery"); err != nil {
			log.Warn("tick: release orphaned claim failed",
				"dispatch_id", o.ID.String(), "error", err.Error())
			continue
		}
		// @story: event-log-read
		var instancePtr *shared.UUID
		if err := args.Persist.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
			n, err := args.Persist.Nodes().Get(ctx, nodeID, tx)
			if err != nil || n == nil {
				return err
			}
			instID := n.InstanceID
			instancePtr = &instID
			return nil
		}); err != nil {
			log.Warn("tick: orphan node lookup failed; emit without instance_id",
				"node_id", nodeID.String(), "error", err.Error())
		}
		if err := args.Persist.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
			return args.Persist.Events().Append(ctx, persistence.EventAppendInput{
				InstanceID: instancePtr,
				NodeID:     &nodeID,
				Kind:       events.KindOrphanedClaimReleased(),
				Payload: map[string]any{
					"dispatch_id":      o.ID.String(),
					"prior_claimed_by": prior,
					"last_progress_at": lastProgress,
					"error_class":      errorClass,
					"reason":           releaseReason,
				},
			}, tx)
		}); err != nil {
			log.Warn("tick: append orphaned_claim_released failed",
				"dispatch_id", o.ID.String(), "error", err.Error())
		}
	}
	return nil
}

func decideExecutorDeadlineRelease(o persistence.DispatchRow, now time.Time) (string, time.Time, string) {
	var lastProgress time.Time
	if o.LastProgressAt != nil {
		lastProgress = *o.LastProgressAt
	}
	if o.EffectiveMaxRuntimeSeconds != nil && *o.EffectiveMaxRuntimeSeconds > 0 && o.ClaimedAt != nil {
		runtime := now.Sub(*o.ClaimedAt)
		if runtime > time.Duration(*o.EffectiveMaxRuntimeSeconds)*time.Second {
			return "max_runtime_exceeded", lastProgress,
				"dispatch runtime exceeded effective max_runtime"
		}
	}
	if o.EffectiveMaxQuietPeriodSeconds != nil && *o.EffectiveMaxQuietPeriodSeconds > 0 {
		ref := lastProgress
		if ref.IsZero() && o.ClaimedAt != nil {
			ref = *o.ClaimedAt
		}
		if !ref.IsZero() {
			quiet := now.Sub(ref)
			if quiet > time.Duration(*o.EffectiveMaxQuietPeriodSeconds)*time.Second {
				return "executor_quiet", lastProgress,
					"dispatch quiet-window exceeded effective max_quiet_period"
			}
		}
	}
	return "", lastProgress, ""
}
