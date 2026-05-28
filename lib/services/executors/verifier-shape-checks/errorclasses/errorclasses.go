// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// Package errorclasses exports the verifier-shape-checks executor's
// hierarchical error-class vocabulary advertised to operators per
// `concept:signal`.
//
// Extracted out of `package main` so the scenario-test layer can import
// the canonical list rather than duplicating it inline. A drift between
// the executor's advertised declared_error_classes and the test's
// expected list becomes a compile or test-time failure rather than a
// silently passing assertion.
package errorclasses

// Declared returns the hierarchical error vocabulary the
// verifier-shape-checks executor advertises via
// `ObservabilityCapabilities.DeclaredErrorClasses`. Patterns ending in
// `*` are prefix patterns; exact strings are fixed leaves.
//
// Leaves:
//   - verifier/attribute_invalid — caller-supplied attribute bag was
//     missing required keys or malformed (`checks` missing/non-array,
//     `rows[*]` not an object, …).
//   - verifier/check_failed/*    — one or more shape checks failed; the
//     suffix carries the check's `kind` (e.g.
//     `verifier/check_failed/pk_unique`,
//     `verifier/check_failed/row_count_absolute`). The check-kind set is
//     parametrized by the operator's `attributes.checks` array, so the
//     declared pattern is the `<prefix>/*` wildcard the validator
//     recognises via `code:graph/node/template_validator.go::errorClassMatchesDeclared`.
func Declared() []string {
	return []string{
		"verifier/attribute_invalid",
		"verifier/check_failed/*",
	}
}
