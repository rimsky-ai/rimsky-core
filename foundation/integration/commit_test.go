// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// Placeholder for the supervisor commit-path test fixture.
//
// The pre-v2 commit_test.go exercised the old
// AcquireLock/OpenHandle/Commit/ReleaseLock surface; the new commit
// path runs through Store.Open inside the acquisition tx and
// Store.Commit/Abandon at terminal time, with auto-terminal aggregating
// per-claim resolution per spec §4.10 invariant 13. End-to-end commit-path coverage
// belongs in the scenario suite (test/scenarios/) where a real
// supervisor + stub executor + postgres store can drive the full flow.

package integration_test
