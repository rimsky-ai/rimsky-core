// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// Package smoke is the lib/services-side smoke fixture for the
// production bundled stores + sensors + subscribers + executors.
//
// Smoke tests here drive rimsky from outside via testcontainers-go
// using the shared `test/harness` package — the pre-2026-05-24
// in-process `BringUpStack` is no longer reachable (the rimsky-
// internal harness is unreachable under the
// `consumption-side-isolation` depguard).
//
// See spec `2026-05-24-repo-reorganization-design` phase P3 and the
// `pkg:test/harness` package for the bring-up shape.
package smoke
