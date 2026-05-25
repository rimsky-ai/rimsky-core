// Copyright © 2026 Fall Guy Consulting.
// Licensed under the Apache License, Version 2.0. See LICENSE.apache at the
// repo root, or http://www.apache.org/licenses/LICENSE-2.0.

// Package conformance provides the rimsky node-executor protocol conformance
// suite. Any executor speaking the protocol (gRPC canonical + HTTP+JSON bridge)
// can be validated against this suite via `rimsky-executor-conformance` CLI.
package conformance

import (
	"context"

	"github.com/fallguyconsulting/rimsky/runtime/executor"
)

// Env is the per-run environment a Scenario receives. It bundles the dialed
// executor Client with the conformance-side CallbackReceiver so scenarios can
// validate both synchronous and async-handoff executors with one code path
// (see AwaitTerminal).
type Env struct {
	Client    executor.Client
	Callbacks *CallbackReceiver
}

// Scenario describes one protocol conformance check.
//
// Run performs the check against an Env that bundles the dialed executor
// Client and the conformance-side CallbackReceiver. A nil error means PASS;
// any error means FAIL (its message is surfaced in the CLI output).
// RequiresAsync and RequiresStub cause the Runner to skip the scenario when
// capability probing indicates the executor cannot satisfy the precondition.
type Scenario struct {
	Name          string
	RequiresAsync bool // skip if executor doesn't advertise async handoff
	RequiresStub  bool // skip if probe indicates executor not in stub mode
	Run           func(ctx context.Context, env Env) error
}

var registered []Scenario

// Register adds a scenario to the global registry. Intended to be called from
// init() in each scenario file.
func Register(s Scenario) { registered = append(registered, s) }

// All returns a copy of the registered scenarios in registration order.
func All() []Scenario { return append([]Scenario{}, registered...) }
