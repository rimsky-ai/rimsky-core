// Copyright © 2026 Fall Guy Consulting.
// Licensed under the Apache License, Version 2.0. See LICENSE.apache at the
// repo root, or http://www.apache.org/licenses/LICENSE-2.0.

// Package errorclasses exports the verifier-http executor's hierarchical
// error-class vocabulary advertised to operators per `concept:signal`.
//
// Extracted out of `package main` so the scenario-test layer can import
// the canonical list rather than duplicating it inline. A drift between
// the executor's advertised declared_error_classes and the test's
// expected list becomes a compile or test-time failure rather than a
// silently passing assertion.
package errorclasses

// Declared returns the hierarchical error vocabulary the verifier-http
// executor advertises via `ObservabilityCapabilities.DeclaredErrorClasses`.
// Patterns ending in `*` are prefix patterns; exact strings are fixed
// leaves.
//
// Leaves:
//   - verifier/attribute_invalid    — caller-supplied attribute bag was
//     missing required keys, malformed, or non-JSON-serialisable.
//   - verifier/network_error        — transport-layer failure dialing the
//     verifier endpoint (DNS, refused, reset, …) that is NOT a timeout.
//   - verifier/timeout              — request deadline exceeded or
//     transport-layer Timeout() error.
//   - verifier/check_failed         — the verifier endpoint responded
//     with a status outside the operator's expected set (the upstream
//     check itself failed). No `/<check_kind>` suffix because the
//     verifier-http response envelope does not carry a check-kind
//     identity — the executor only sees an HTTP status code.
func Declared() []string {
	return []string{
		"verifier/attribute_invalid",
		"verifier/network_error",
		"verifier/timeout",
		"verifier/check_failed",
	}
}
