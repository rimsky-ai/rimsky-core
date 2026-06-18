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

func checkTerminals(ctx context.Context, c claimproducer.ClaimProducer) []CheckResult {
	out := make([]CheckResult, 0, 4)
	out = append(out, checkTerminalVerb(ctx, c, "Commit", func(id claimproducer.ClaimID, scope, addr []byte) error {
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

type terminalFn func(id claimproducer.ClaimID, scope, addr []byte) error

func checkTerminalVerb(ctx context.Context, c claimproducer.ClaimProducer, name string, verb terminalFn) CheckResult {
	claimID := claimproducer.ClaimID(uuid.New().String())
	out, err := c.Open(ctx, claimID, terminalProbeSpec(name))
	if err != nil {
		return CheckResult{Name: name, Err: fmt.Errorf("Open for %s probe failed: %w", name, err)}
	}
	if !out.Available {
		return CheckResult{Name: name + "Skipped"}
	}
	if err := verb(claimID, out.Result.ClaimScope, out.Result.Address); err != nil {
		return CheckResult{Name: name, Err: fmt.Errorf("%s on Open'd claim failed: %w", name, err)}
	}
	return CheckResult{Name: name}
}

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

func terminalProbeSpec(probe string) claimproducer.ClaimSpec {
	return claimproducer.ClaimSpec{
		ProducerName: "conformance-target",
		Selector:     "rimsky/conformance/terminal/" + probe + "/" + uuid.New().String(),
		Intent:       claimproducer.IntentRead,
		Alias:        "conformance-terminal",
	}
}
