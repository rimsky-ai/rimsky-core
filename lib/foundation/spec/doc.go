// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// Package spec defines the persistable row-type primitives — the
// concrete data shapes foundation/persistence stores and round-trips
// through Postgres and SQLite.
//
// This package contains pure data: types and constants only. Graph
// algorithms that operate on these types (template validation, the
// error-policy evaluator, holding-subgraph computation, inheritance
// resolution) live in graph/node.
//
// Why it exists: foundation must be self-contained — it cannot import
// graph/. Before this package existed, foundation/persistence imported
// graph/node to reach TemplateSpec / EvaluatorState / TemplateNodeDef
// for row-type definitions, creating a back-import (foundation →
// graph). The back-import was documented as a residual and exempted in
// depguard. Moving the row-type primitives here eliminates that
// residual: graph/node now imports foundation/spec (the canonical
// direction; layer order is foundation → graph → runtime → control).
//
// Graph still owns the algorithms that work on these types. Where
// existing graph/node consumers reference the moved types via the
// graph/node import path, graph/node re-exports them as type aliases
// for backward compatibility — `type TemplateSpec = spec.TemplateSpec`,
// etc. — so consumers don't need to immediately update their imports.
//
// Persisted JSON shape: the YAML / JSON struct tags on these types are
// load-bearing. Changing them changes the on-disk row shape for
// rimsky_templates.spec, rimsky_nodes (handler-evaluator state), and
// the like. Treat the tags as a wire-format commitment.
package spec
