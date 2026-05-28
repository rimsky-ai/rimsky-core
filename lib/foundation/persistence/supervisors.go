// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package persistence

import (
	"context"
	"time"
)

// SupervisorRow mirrors a row of rimsky_supervisors.
type SupervisorRow struct {
	ID                string    `json:"id"`
	AcceptedExecutors []string  `json:"accepted_executors"`
	AcceptedStores    []string  `json:"accepted_stores"`
	Concurrency       int       `json:"concurrency"`
	CallbackHost      string    `json:"callback_host"`
	CallbackPort      int       `json:"callback_port"`
	LastHeartbeatAt   time.Time `json:"last_heartbeat_at"`
	ActiveNodeCount   int       `json:"active_node_count"`
	RegisteredAt      time.Time `json:"registered_at"`
}

// SupervisorRegisterInput is the per-row input for Register.
type SupervisorRegisterInput struct {
	ID                string
	AcceptedExecutors []string
	AcceptedStores    []string
	Concurrency       int
	CallbackHost      string
	CallbackPort      int
}

// SupervisorTable is the rimsky_supervisors accessor.
type SupervisorTable interface {
	Register(ctx context.Context, in SupervisorRegisterInput, tx Tx) error
	Heartbeat(ctx context.Context, id string, activeNodeCount int, tx Tx) error
	Get(ctx context.Context, id string, tx Tx) (*SupervisorRow, error)
	List(ctx context.Context, tx Tx) ([]SupervisorRow, error)
	ListStale(ctx context.Context, cutoff time.Time, tx Tx) ([]SupervisorRow, error)
	Unregister(ctx context.Context, id string, tx Tx) error
}
