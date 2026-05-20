// Copyright © 2026 Fall Guy Consulting.
// Licensed under the Apache License, Version 2.0. See LICENSE.apache at the
// repo root, or http://www.apache.org/licenses/LICENSE-2.0.

// Package sqlchecks compiles a declarative check vocabulary into
// aggregate-only SQL queries for verifier-style read-only checks
// against a SQL substrate. The vocabulary mirrors
// executors/verifier-shape-checks/checks/ where semantics carry over,
// so SQL-side and shape-side check configurations stay coherent.
//
// Per spec
// .ok-planner/specs/2026-05-19-multi-instance-template-ergonomics-design.md
// §Item 6.
//
// @concept: executor
package sqlchecks

// CheckSpec is one declarative check, decoded from userdata.
type CheckSpec struct {
	Kind   string         `json:"kind" yaml:"kind"`
	Config map[string]any `json:"config" yaml:"config"`
}

// Result is the per-check outcome surfaced upstream. Pass=false rolls
// up into a `verifier_failed` terminal at the executor boundary.
type Result struct {
	// Kind echoes the check kind for log/payload aggregation.
	Kind string `json:"kind"`
	// Pass is true when the check's compiled query produced a
	// result consistent with the check's pass semantics.
	Pass bool `json:"pass"`
	// Counts holds the relevant numeric diagnostics for the kind
	// (e.g. row count for row_count_absolute).
	Counts map[string]any `json:"counts,omitempty"`
	// Message is a human-readable summary; empty on pass for low-noise
	// success logs.
	Message string `json:"message,omitempty"`
}
