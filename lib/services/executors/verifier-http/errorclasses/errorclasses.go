// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

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
//     check itself failed). Emitted when the upstream's 4xx/5xx body
//     does not carry a parseable `class_field` token (the stable
//     subscribable fallback so `verifier/check_failed/*` policies
//     still match taxonomy-less upstreams without collapsing to the
//     catch-all).
//   - verifier/check_failed/*       — wildcard cover for the typed
//     leaves the executor populates from the upstream's
//     `class_field` JSON token (default `class`, per
//     `attributes.class_field`). The suffix is the upstream's
//     verbatim class string so policy / subscriber matching keys on
//     the upstream's taxonomy. Mirrors http-node's
//     `http/request_invalid/*` discipline.
func Declared() []string {
	return []string{
		"verifier/attribute_invalid",
		"verifier/network_error",
		"verifier/timeout",
		"verifier/check_failed",
		"verifier/check_failed/*",
	}
}
