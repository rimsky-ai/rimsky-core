// Copyright © 2026 Fall Guy Consulting.
// Licensed under the Apache License, Version 2.0. See LICENSE.apache at the
// repo root, or http://www.apache.org/licenses/LICENSE-2.0.

package claimproducer

import (
	"context"
	"fmt"

	"github.com/google/uuid"

	"github.com/rimsky-ai/rimsky-core/lib/protocols/claimproducer"
)

// checkTerminals drives the full claim lifecycle past Open — Commit, Abandon,
// and Release on real claims the suite itself Open'd — plus a retried-terminal
// idempotency probe, each reported as its own pass/fail row. Per spec story
// S-conformance-claimproducer-terminals: the suite MUST actually exercise the
// terminal verbs against a claim it Open'd, not merely the Open handshake.
//
// Each verb gets a fresh claim (synthetic selector + fresh claim_id) so the
// verbs never alias one another's producer-internal state. A failure on one
// verb does not short-circuit the others — the suite reports the full terminal
// surface, matching runner.go's existing every-check-is-independent pattern.
//
// SKIP carve-out: when the producer returns Unavailable for the synthetic Open
// (e.g. a drained pick-policy queue with nothing to give), the row degrades to
// a `<Verb>Skipped` SKIP marker (Err == nil), mirroring SplitScopeSkipped and
// the 9b Available:false skip, so the suite stays runnable against queue-shaped
// producers. The real bundled producer, which Opens on any synthetic selector,
// produces the concrete passing rows the gate asserts.
func checkTerminals(ctx context.Context, c claimproducer.ClaimProducer) []CheckResult {
	out := make([]CheckResult, 0, 4)
	out = append(out, checkTerminalVerb(ctx, c, "Commit", func(id claimproducer.ClaimID, scope, addr []byte) error {
		// The base CommitResponse body (version_id / producer_metadata)
		// is optional producer output; the conformance probe asserts
		// only that the verb is accepted.
		_, err := c.Commit(ctx, id, scope, addr)
		return err
	}))
	out = append(out, checkTerminalVerb(ctx, c, "Abandon", func(id claimproducer.ClaimID, scope, addr []byte) error {
		return c.Abandon(ctx, id, scope, addr)
	}))
	out = append(out, checkTerminalVerb(ctx, c, "Release", func(id claimproducer.ClaimID, scope, addr []byte) error {
		return c.Release(ctx, id, scope, addr)
	}))
	out = append(out, checkTerminalIdempotency(ctx, c))
	return out
}

// terminalFn invokes one terminal verb (Commit | Abandon | Release) on a claim
// the suite Open'd, keyed by claim_id + the producer-supplied scope/address.
type terminalFn func(id claimproducer.ClaimID, scope, addr []byte) error

// checkTerminalVerb Opens a fresh claim and drives the named terminal verb on
// it. The verb is invoked with the Open'd claim's own scope/address — the same
// bytes rimsky threads from Open's response into the terminal call on the live
// path — so the producer sees a coherent (claim_id, scope, address) tuple, not
// a synthetic one it never issued.
func checkTerminalVerb(ctx context.Context, c claimproducer.ClaimProducer, name string, verb terminalFn) CheckResult {
	claimID := claimproducer.ClaimID(uuid.New().String())
	out, err := c.Open(ctx, claimID, terminalProbeSpec(name))
	if err != nil {
		return CheckResult{Name: name, Err: fmt.Errorf("Open for %s probe failed: %w", name, err)}
	}
	if !out.Available {
		// A drained / pick-policy producer with nothing to give cannot be
		// driven through the terminal — SKIP rather than fail so the suite
		// stays runnable against queue-shaped producers.
		return CheckResult{Name: name + "Skipped"}
	}
	if err := verb(claimID, out.Result.ClaimScope, out.Result.Address); err != nil {
		return CheckResult{Name: name, Err: fmt.Errorf("%s on Open'd claim failed: %w", name, err)}
	}
	return CheckResult{Name: name}
}

// checkTerminalIdempotency Opens a claim and issues the SAME terminal verb
// twice with the SAME (claimID + scope + address) tuple, asserting the second
// (retried) call is accepted without error. Per S-conformance-claimproducer-
// terminals: a producer's terminal verbs must be idempotent under retry so a
// lost-ack redelivery on the live callback path does not corrupt producer
// state. Commit is the canonical terminal to retry; a producer whose duplicate
// Commit errors fails this row.
func checkTerminalIdempotency(ctx context.Context, c claimproducer.ClaimProducer) CheckResult {
	const name = "TerminalIdempotency"
	claimID := claimproducer.ClaimID(uuid.New().String())
	out, err := c.Open(ctx, claimID, terminalProbeSpec(name))
	if err != nil {
		return CheckResult{Name: name, Err: fmt.Errorf("Open for idempotency probe failed: %w", err)}
	}
	if !out.Available {
		return CheckResult{Name: name + "Skipped"}
	}
	scope, addr := out.Result.ClaimScope, out.Result.Address
	if _, err := c.Commit(ctx, claimID, scope, addr); err != nil {
		return CheckResult{Name: name, Err: fmt.Errorf("first Commit failed: %w", err)}
	}
	if _, err := c.Commit(ctx, claimID, scope, addr); err != nil {
		return CheckResult{
			Name: name,
			Err: fmt.Errorf("retried (duplicate) Commit on the same claim rejected with %v: "+
				"terminal verbs must be idempotent under retry so a lost-ack redelivery "+
				"does not corrupt producer state", err),
		}
	}
	return CheckResult{Name: name}
}

// terminalProbeSpec builds a synthetic read claim spec for a terminal probe.
// The probe name is folded into the selector so each verb's claim is distinct,
// keeping the verbs from aliasing one another's producer-internal state.
func terminalProbeSpec(probe string) claimproducer.ClaimSpec {
	return claimproducer.ClaimSpec{
		ProducerName: "conformance-target",
		Selector:     "rimsky/conformance/terminal/" + probe + "/" + uuid.New().String(),
		Intent:       claimproducer.IntentRead,
		Alias:        "conformance-terminal",
	}
}
