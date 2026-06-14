// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// launcher.go — in-process role-runner orchestrator for
// `rimsky compose run`. Mirrors the all-in-one entrypoint's
// runUnified shape (start scheduler → supervisor → control-api in
// order, track stop functions, surface a combined role-failure
// channel, drain in reverse order on shutdown), but adds the verb's
// migration-direct step: the persistence driver's Migrate runs
// against the freshly-created sqlite database BEFORE any role runner
// opens it. See @decision: launch-integration,
// launch-config-injection, migration-direct, network-binding.
package compose

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/rimsky-ai/rimsky-core/lib/control/config"
	"github.com/rimsky-ai/rimsky-core/lib/control/launch"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
)

// RoleStack is the handle returned by StartRoleStack. It wraps the
// shared launch.UnifiedStack (which owns the per-role stop functions
// and merged role-failure channel) with the verb-specific extras: the
// pre-computed loopback endpoint and ownership of the shared
// persistence driver opened for the up-front Migrate. The caller (the
// compose-run verb's main loop) selects on FailCh() alongside a signal
// channel and the terminal-wait result, then calls Drain on whichever
// trigger fired.
//
// @agent-contract guarantees: StartRoleStack returns a RoleStack
// only when (a) migrations applied cleanly against the freshly-
// created database and (b) all three role runners started without
// error; on any earlier failure StartRoleStack drains the partial
// stack itself and returns the error. Drain is fully idempotent —
// the first call runs the underlying launch.UnifiedStack.Drain and
// the driver close exactly once; subsequent calls are no-ops. The
// fail channel is not touched (closing it would race the role-
// monitor goroutines). Does NOT manage signal handling, the
// terminal-wait loop, or the compose-apply step — those are the
// caller's concern.
type RoleStack struct {
	unified  *launch.UnifiedStack
	endpoint string
	// driver is the shared persistence handle that every role runner in
	// the stack was started with. The stack owns it: Drain closes it
	// after all role stops complete. One driver across all three role
	// runners is the invariant the launch/OpenDriverFromEnv helper
	// names; the launcher test pairs the slug.
	driver persistence.Database
	// drainOnce gates the entire Drain body so the underlying
	// UnifiedStack.Drain (which walks each StopFunc) and the driver
	// Close fire at most once. Mirrors ShutdownCoordinator.drainOnce
	// in shutdown.go — the second-signal escalator can race a natural-
	// completion drain.
	drainOnce sync.Once
}

// RoleFailure aliases launch.RoleFailure so callers of this package
// see one stable surface; the shared launch helper owns the canonical
// shape so the entrypoint's runUnified path and the compose-run verb
// agree on what a role failure looks like.
type RoleFailure = launch.RoleFailure

// MigratePersistence loads the synthetic rimsky.yml at configPath,
// opens the configured persistence driver, and runs Migrate against
// it. Returns the opened+migrated driver plus the parsed config so
// the caller can pass them to StartUnifiedRoleStack without re-loading.
//
// On any failure the driver is closed before returning (or never
// opened, depending on which step failed) — the caller never owns a
// half-initialised handle.
//
// @blessed-invariant: migrations-run-before-runners — split out into
// its own function so a sequencing-regression test
// (TestMigratePersistence_CompletesBeforeStartRoleStack) can assert
// MigratePersistence returned BEFORE the runner-start callback fires.
// The previous shape (one function calling Migrate then StartUnifiedStack)
// only let the post-condition (migration table populated) be
// verified, which holds for several wrong orderings the BI's name
// forbids; splitting the seam pins the ordering structurally.
func MigratePersistence(ctx context.Context, logger *slog.Logger, configPath string) (persistence.Database, config.RimskyConfig, error) {
	// @constraint: load the rimsky.yml once HERE so the persistence-
	// config used for the up-front Migrate is the same one the role
	// runners later load themselves. If we re-loaded inside the
	// runners' env-var path only, a transient FS issue between this
	// load and the runner's load could land us on different configs
	// — the migrate would have applied to one DB and the runners
	// would open another.
	cfg, err := config.LoadRimskyConfigYAML(configPath)
	if err != nil {
		return nil, config.RimskyConfig{}, fmt.Errorf("load synthetic rimsky.yml: %w", err)
	}
	driver, err := persistence.Open(ctx, cfg.Persistence)
	if err != nil {
		return nil, config.RimskyConfig{}, fmt.Errorf("open persistence for migrate: %w", err)
	}
	if err := driver.Migrate(ctx, shared.NewSlogLogger(logger.With("phase", "migrate"))); err != nil {
		_ = driver.Close()
		return nil, config.RimskyConfig{}, fmt.Errorf("migrate: %w", err)
	}
	return driver, cfg, nil
}

// startRoleStackFn is the launch-side seam StartRoleStack calls to
// boot the three role runners. Held as a package var so the BI test
// can substitute a fake that observes the call ordering relative to
// MigratePersistence's completion — see
// TestMigratePersistence_CompletesBeforeStartRoleStack.
var startRoleStackFn = launch.StartUnifiedStack

// StartRoleStack runs migrations against the synthetic rimsky.yml's
// configured persistence (sqlite, per @decision: persistence-driver),
// then delegates role startup to launch.StartUnifiedStack — the same
// shared helper the all-in-one entrypoint's runUnified path calls
// (@decision: launch-integration). RIMSKY_CONFIG,
// RIMSKY_SUPERVISOR_CONFIG, RIMSKY_PROCESS_ROLE,
// RIMSKY_CONTROL_API_HOST, and RIMSKY_CONTROL_API_PORT must be set
// in the process env BEFORE this is called: the role runners read
// them on startup. The caller pre-picks the control-api port via
// hostagent.FreeLocalPort and threads the resulting endpoint
// ("http://127.0.0.1:<port>") in as the endpoint argument.
//
// @blessed-invariant: migrations-run-before-runners — the persistence
// driver's Migrate completes successfully BEFORE any role runner
// opens the database file. Otherwise the supervisor's first claim-
// poll transaction would hit "no such table" against an empty schema
// (the falsifier the BI test exhibits). The migrate completes inside
// MigratePersistence; the StartUnifiedStack call is the runner-start
// side. The startRoleStackFn package var is the seam the BI test
// patches to observe the ordering directly.
//
// On a runner-start failure, StartUnifiedStack drains the role runners
// already started (reverse order, 5-second deadline) and returns the
// error; StartRoleStack additionally closes the driver and surfaces
// the wrapped error — the caller must not see a partial stack.
func StartRoleStack(ctx context.Context, logger *slog.Logger, configPath string, endpoint string) (*RoleStack, error) {
	driver, cfg, err := MigratePersistence(ctx, logger, configPath)
	if err != nil {
		return nil, err
	}
	unified, err := startRoleStackFn(ctx, logger, driver, &cfg)
	if err != nil {
		_ = driver.Close()
		return nil, err
	}
	return &RoleStack{
		unified:  unified,
		endpoint: endpoint,
		driver:   driver,
	}, nil
}

// Drain stops the role runners in reverse start order (delegated to
// the shared launch.UnifiedStack), bounded by deadline, then closes
// the shared persistence driver. Idempotent — the entire body runs
// at most once via drainOnce; subsequent calls return immediately.
// The combined fail channel is left for the caller to drain or
// abandon; closing it here would race against the monitor goroutines.
func (s *RoleStack) Drain(ctx context.Context, deadline time.Duration) {
	s.drainOnce.Do(func() {
		if s.unified != nil {
			s.unified.Drain(ctx, deadline)
		}
		if s.driver != nil {
			_ = s.driver.Close()
		}
	})
}

// FailCh exposes the merged role-failure channel for the caller's
// select loop. At most one failure per role lands here; subsequent
// failures from the same role are dropped (a single failure is
// enough to trigger shutdown).
func (s *RoleStack) FailCh() <-chan RoleFailure {
	if s.unified == nil {
		return nil
	}
	return s.unified.FailCh()
}

// Endpoint returns the loopback URL the control-api role binds to,
// pre-computed by the caller from the picked port. Returning it
// from the stack lets the apply step and the readiness poll consult
// a single source of truth rather than rebuild the URL.
func (s *RoleStack) Endpoint() string { return s.endpoint }

// WaitForControlAPIReady polls <endpoint>/v1/health until it returns
// HTTP 200, the deadline expires, or the supplied context is
// cancelled. The poll interval is 50ms — short enough for a tight
// loopback boot, long enough not to spin the CPU.
//
// The verb calls this immediately after StartRoleStack and before
// the compose-apply step so the existing compose engine's first
// request lands on a fully-started server rather than racing the
// bind. A failure here is non-recoverable from the verb's
// perspective; the caller drains the stack and exits non-zero.
func WaitForControlAPIReady(ctx context.Context, endpoint string, deadline time.Duration) error {
	healthURL := endpoint + "/v1/health"
	// Short timeout per attempt so a hung connection cannot eat the
	// whole budget on a single dial; the readiness deadline bounds
	// the overall wait via the for-loop check.
	client := &http.Client{Timeout: 500 * time.Millisecond}
	pollCtx, cancel := context.WithTimeout(ctx, deadline)
	defer cancel()
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	for {
		req, err := http.NewRequestWithContext(pollCtx, http.MethodGet, healthURL, nil)
		if err != nil {
			return fmt.Errorf("build health request: %w", err)
		}
		resp, err := client.Do(req)
		if err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return nil
			}
		}
		select {
		case <-pollCtx.Done():
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return fmt.Errorf("control-api %s not ready within %s", healthURL, deadline)
		case <-ticker.C:
		}
	}
}
