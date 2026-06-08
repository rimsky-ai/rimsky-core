// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// Package rulesdoc hosts the accuracy gate for .claude/rules/rules.md.
//
// The rules document instructs contributors to run concrete build/verify
// commands against concrete filesystem paths. When those cited paths drift
// out of existence (e.g. a deleted deploy/ directory), a contributor acting
// on the documented step silently skips the check it was meant to perform.
//
// This package carries no runtime code; it exists only as a home for the
// accuracy test (rulesdoc_test.go) that scans rules.md and asserts every
// cited repo-relative path resolves against the current tree.
package rulesdoc
