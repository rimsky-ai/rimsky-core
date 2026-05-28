// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// Package attributes owns the per-node typed attribute object described in
// stores-redesign spec §5.7: substitution of `{{nodes...}}` / `{{claim...}}`
// / `{{params...}}` directives at dispatch, JSON Schema validation at
// dispatch and at commit, and the incremental writeback HTTP handler.
//
// Note: the `{{deps...}}` substitution prefix retired post-2026-05-14;
// `{{nodes.<node>.attribute.<key>}}` is the canonical form. The change is
// observable to template authors but produces no functional difference to
// resolution; the inferred-subscription side-effect lives in the template
// validator.
//
// Persistence for `rimsky_node_attributes` lives in
// `foundation/persistence/postgres/node_attributes.go` and is exposed via
// `persistence.NodeAttributeTable` from `foundation/persistence/store.go`.
// The HTTP handler in `callback.go` accepts a narrower `NodeAttributeTable`
// interface so test fakes don't have to implement the full persistence shape.
//
// # Surface
//
//   - Substitute  — single-pass `{{...}}` resolution (substitution.go).
//   - Validate    — JSON Schema validation against the template's declared
//     schema (validate.go).
//   - Handler     — chi-compatible HTTP handler implementing the §12.5
//     incremental attributes callback (callback.go).
//
// # Boundaries
//
// Post-2026-05-21 userdata collapse: userdata is no longer a distinct
// concept. The unified attribute schema covers both rimsky-resolved
// inputs and template-author config (static defaults). The substitution
// grammar enumerates source kinds (nodes / claim / params / trigger /
// child); attribute values themselves are inert to rimsky under the
// structural-inertness discipline (concept:inertness).
//
// Per spec §4.10 invariant 12, attributes are validated twice: once at
// dispatch (after substitution, before the executor sees the request) and
// once at commit (after the executor's writes are merged). Both gates are
// mandatory; this package exposes a single Validate entry point and the
// caller picks which gate it represents.
package attributes
