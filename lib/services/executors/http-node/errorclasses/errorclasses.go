// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// Package errorclasses exports the http-node executor's hierarchical
// error-class vocabulary advertised to operators per `concept:signal`.
//
// Extracted out of `package main` so the scenario-test layer can import
// the canonical list rather than duplicating it inline. The drift-test
// in `test/scenarios/bundled_executor_vocab_test.go` reads this list
// through the import, so a divergence between the executor's advertised
// declared_error_classes and the test's expected list becomes a compile
// or test-time failure rather than a silently passing assertion.
package errorclasses

// Declared returns the hierarchical error vocabulary the http-node
// executor advertises via `ObservabilityCapabilities.DeclaredErrorClasses`.
// Patterns ending in `*` are prefix patterns; exact strings are fixed
// leaves.
func Declared() []string {
	return []string{
		"http/network_error",
		"http/timeout",
		"http/request_invalid/*",
		"http/server_error/*",
		"http/expectation_mismatch",
		"http/response_unparseable",
		"http/attribute_invalid",
		"http/internal_error",
	}
}
