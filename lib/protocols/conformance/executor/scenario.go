// Copyright © 2026 Fall Guy Consulting.
// Licensed under the Apache License, Version 2.0. See LICENSE.apache at the
// repo root, or http://www.apache.org/licenses/LICENSE-2.0.

package conformance

import (
	"context"
)

type Env struct {
	Client    Client
	Callbacks *CallbackReceiver
}

type Scenario struct {
	Name          string
	RequiresAsync bool
	RequiresStub  bool
	Run           func(ctx context.Context, env Env) error
}

var registered []Scenario

func Register(s Scenario) { registered = append(registered, s) }

func All() []Scenario { return append([]Scenario{}, registered...) }
