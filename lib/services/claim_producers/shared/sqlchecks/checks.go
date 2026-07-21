// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// @concept: executor
package sqlchecks

type CheckSpec struct {
	Kind   string         `json:"kind" yaml:"kind"`
	Config map[string]any `json:"config" yaml:"config"`
}

type Result struct {
	Kind    string         `json:"kind"`
	Pass    bool           `json:"pass"`
	Counts  map[string]any `json:"counts,omitempty"`
	Message string         `json:"message,omitempty"`
	Error   string         `json:"error,omitempty"`
}
