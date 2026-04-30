// Package attributes owns the per-node typed attribute object described in
// stores-redesign spec §5.7: substitution of `{{deps...}}` / `{{claim...}}`
// / `{{params...}}` directives at dispatch, JSON Schema validation at
// dispatch and at commit, the incremental writeback HTTP handler, and the
// `rimsky_node_attributes` postgres helpers.
//
// # Surface
//
//   - Substitute  — single-pass `{{...}}` resolution (substitution.go).
//   - Validate    — JSON Schema validation against the template's declared
//     schema (validate.go).
//   - Handler     — chi-compatible HTTP handler implementing the §12.5
//     incremental attributes callback (callback.go).
//   - Store       — postgres helpers for the `rimsky_node_attributes` table:
//     Get / Upsert / MergeDelta (store.go).
//
// # Boundaries
//
// Per spec §4.10 invariant 11, no code path in this package inspects, parses,
// substitutes, or validates `userdata`. The substitution grammar enumerates
// exactly three source kinds (deps / claim / params); userdata is not a
// source kind and never participates in resolution.
//
// Per spec §4.10 invariant 12, attributes are validated twice: once at
// dispatch (after substitution, before the executor sees the request) and
// once at commit (after the executor's writes are merged). Both gates are
// mandatory; this package exposes a single Validate entry point and the
// caller picks which gate it represents.
package attributes
