// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// Supervisor-side run-scope lifecycle fan-out. The supervisor closes
// sub-graph and fanout-partition RunScopes at the runtime/ terminal
// sites (subgraph_dispatch.go, auto_terminal_chain.go); when it does, it
// fires OnRunScopeTerminal to the lifecycle subscribers that match the
// late-bind-extended peer filter for the instance's template. Per spec
// 2026-05-24-host-agent-and-proxy-design.md §"Firing sites for
// OnRunScopeTerminal".
package runtime

import (
	"context"
	"log/slog"

	"github.com/fallguyconsulting/rimsky/foundation/locks"
	"github.com/fallguyconsulting/rimsky/foundation/persistence"
	"github.com/fallguyconsulting/rimsky/foundation/shared"
	"github.com/fallguyconsulting/rimsky/graph/node"
)

// FanOutRunScopeEvent fires OnRunScopeTerminal to subscribers matching
// the peer filter for the given template. Called by the supervisor's
// sub-graph and fanout-partition close sites synchronously after the
// close commits, inside the caller's tx. Idempotency-protected via
// rimsky_lifecycle_idempotencies (scope_kind="run_scope",
// state="run_scope_terminal").
//
// Parallel to control-api's FanOutRunScopeEvent (which handles main
// scopes). Each layer has its own LifecycleRegistry; the two close
// disjoint scope kinds, so there is no double-fire risk. This helper
// takes explicit parameters (no AppDeps) because runtime/ cannot import
// control/ — the late-bind-aware peer filter crosses the layer boundary
// as the peersForSpec function pointer, populated at the cmd/ entrypoint.
//
// No-op when the supervisor was not wired with lifecycle outbound
// (lifecycleSubs == nil || peersForSpec == nil) — most unit tests and
// the lifecycle-free deployments fall in this case.
//
// Returns nothing: the run-scope close is the load-bearing write and must
// not roll back on a fan-out failure. Subscriber errors and lifecycle-
// idempotency Get/Upsert errors are all logged-and-skipped (a later close
// re-attempts; subscribers are idempotent). Per spec
// 2026-05-24-host-agent-and-proxy-design.md.
func FanOutRunScopeEvent(
	ctx context.Context,
	persist persistence.Tables,
	lifecycleSubs *locks.LifecycleRegistry,
	peersForSpec func(tplSpec node.TemplateSpec) []string,
	tplSpec node.TemplateSpec,
	runScopeID shared.UUID,
	instanceID shared.UUID,
	terminalReason string,
	tx persistence.Tx,
) {
	if lifecycleSubs == nil || peersForSpec == nil {
		return // supervisor wasn't wired with lifecycle outbound
	}
	peers := peersForSpec(tplSpec)
	scopeID := runScopeID.String()

	for _, name := range peers {
		existing, err := persist.LifecycleIdempotency().Get(
			ctx, name,
			persistence.LifecycleIdempotencyScopeRunScope,
			scopeID, tx,
		)
		if err != nil {
			// Non-fatal: the close is the load-bearing write and must not
			// roll back on a fan-out bookkeeping failure. Skip this peer; a
			// later close re-attempts (subscribers are idempotent). Mirrors
			// the per-peer subscriber-error convention below.
			slog.Warn("FanOutRunScopeEvent: lifecycle row lookup failed; skipping peer",
				"peer", name, "run_scope_id", scopeID, "error", err)
			continue
		}
		if existing != nil && existing.State == persistence.LifecycleIdempotencyStateRunScopeTerminal {
			continue
		}

		sub, ok := lifecycleSubs.Get(name)
		if !ok {
			continue
		}

		req := locks.OnRunScopeTerminalRequest{
			RunScopeID:     scopeID,
			TerminalReason: terminalReason,
			InstanceID:     instanceID.String(),
		}
		if err := sub.OnRunScopeTerminal(ctx, req); err != nil {
			// Non-fatal at the per-peer level: a subscriber error must not
			// roll back the close it observed. Skip the bookkeeping upsert
			// (so a later close re-attempts) and continue to the next peer.
			// Mirrors control-api FanOutRunScopeEvent's per-peer
			// continue-on-error convention.
			continue
		}

		if err := persist.LifecycleIdempotency().Upsert(ctx,
			persistence.LifecycleIdempotencyRow{
				StoreRegistrationName: name,
				ScopeKind:             persistence.LifecycleIdempotencyScopeRunScope,
				ScopeID:               scopeID,
				State:                 persistence.LifecycleIdempotencyStateRunScopeTerminal,
			}, tx,
		); err != nil {
			// Non-fatal: same rationale as the lookup above. The subscriber
			// already fired successfully; a missed upsert only means a later
			// close may re-fire (idempotent on the subscriber side).
			slog.Warn("FanOutRunScopeEvent: lifecycle row upsert failed; close stands",
				"peer", name, "run_scope_id", scopeID, "error", err)
			continue
		}
	}
}
