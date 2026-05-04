// Package conformance provides the rimsky node-executor protocol conformance
// suite. Any executor speaking the protocol (gRPC canonical + HTTP+JSON bridge)
// can be validated against this suite via `rimsky-conformance` CLI.
package conformance

import (
	"context"

	"github.com/fallguy/rimsky/modeling/executor"
)

// Scenario describes one protocol conformance check.
//
// Run performs the check against a dialed executor Client. A nil error means
// PASS; any error means FAIL (its message is surfaced in the CLI output).
// RequiresAsync and RequiresStub cause the Runner to skip the scenario when
// capability probing indicates the executor cannot satisfy the precondition.
type Scenario struct {
	Name          string
	RequiresAsync bool // skip if executor doesn't advertise async handoff
	RequiresStub  bool // skip if probe indicates executor not in stub mode
	Run           func(ctx context.Context, client executor.Client) error
}

var registered []Scenario

// Register adds a scenario to the global registry. Intended to be called from
// init() in each scenario file.
func Register(s Scenario) { registered = append(registered, s) }

// All returns a copy of the registered scenarios in registration order.
func All() []Scenario { return append([]Scenario{}, registered...) }
