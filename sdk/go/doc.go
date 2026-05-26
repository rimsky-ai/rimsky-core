// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: Apache-2.0

// Package sdk is the canonical Go-side implementer-facing surface for building
// services that rimsky talks to.
//
// Houses server scaffolding (claim-producer, executor, lifecycle-subscriber,
// blob-backend, publisher), publisher-side message-emit helpers, a conformance
// library invocable from service authors' own Go tests, a testcontainer helper,
// and operational glue (slog setup, healthcheck endpoint, DSN env-var parser).
//
// Does NOT contain calling-side wire code: rimsky-internal infrastructure
// (supervisor, terminal-resolution, discovery-cache) stays in rimsky's
// runtime/peer/. See concept:sdk in .ok-planner/design/concepts/sdk.md.
//
// @concept: sdk
package sdk
