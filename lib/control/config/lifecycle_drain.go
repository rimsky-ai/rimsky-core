// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

// @decision: lifecycle-drain-per-role
// @concept: lifecycle-subscriber

package config

import (
	"context"
	"fmt"
	"time"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	"github.com/rimsky-ai/rimsky-core/lib/runtime"
)

type SharedLifecycleDrainConfig struct {
	Driver         persistence.Database
	Clock          shared.Clock
	Logger         shared.Logger
	ClaimProducers RemoteClaimProducersConfig
	Executors      ExecutorsConfig
	Publishers     RemotePublishersConfig

	// @decision: lifecycle-subscriber-at-least-once-delivery
	StallAfter time.Duration
}

// @decision: lifecycle-drain-per-role
func StartSharedLifecycleDrain(cfg SharedLifecycleDrainConfig) (*runtime.LifecycleReconciler, func(), error) {
	if cfg.Driver == nil {
		return nil, nil, fmt.Errorf("StartSharedLifecycleDrain: Driver is required")
	}
	subscribers, err := DialLifecycleSubscribers(context.Background(), cfg.ClaimProducers, cfg.Executors, cfg.Publishers)
	if err != nil {
		return nil, nil, fmt.Errorf("StartSharedLifecycleDrain: dial lifecycle subscribers: %w", err)
	}
	drain := runtime.NewLifecycleReconciler(runtime.LifecycleReconcilerConfig{
		Persist:        cfg.Driver.Tables(),
		AdvisoryLocker: cfg.Driver.AdvisoryLocker(),
		Subscribers:    subscribers,
		Clock:          cfg.Clock,
		Logger:         cfg.Logger,
		StallAfter:     cfg.StallAfter,
	})
	drainCtx, cancel := context.WithCancel(context.Background())
	go drain.Run(drainCtx)
	stop := func() {
		drain.Stop()
		cancel()
		subscribers.Close()
	}
	return drain, stop, nil
}
