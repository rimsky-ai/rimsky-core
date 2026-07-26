// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package persistence

import (
	"context"
	"time"
)

type SupervisorRow struct {
	ID           string    `json:"id"`
	Concurrency  int       `json:"concurrency"`
	CallbackHost string    `json:"callback_host"`
	CallbackPort int       `json:"callback_port"`
	RegisteredAt time.Time `json:"registered_at"`
}

type SupervisorRegisterInput struct {
	ID           string
	Concurrency  int
	CallbackHost string
	CallbackPort int
}

type SupervisorTable interface {
	Register(ctx context.Context, in SupervisorRegisterInput, tx Tx) error
	Get(ctx context.Context, id string, tx Tx) (*SupervisorRow, error)
	List(ctx context.Context, tx Tx) ([]SupervisorRow, error)
	Unregister(ctx context.Context, id string, tx Tx) error
}
