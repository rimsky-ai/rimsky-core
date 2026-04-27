// Placeholder for the supervisor commit-path test fixture.
//
// The pre-v2 commit_test.go exercised the old
// AcquireLock/OpenHandle/Commit/ReleaseLock surface; the new commit
// path runs through Store.Open inside the acquisition tx and
// Store.Commit/Abandon at terminal time, with auto-terminal aggregating
// per-claim resolution per spec §14.4. End-to-end commit-path coverage
// belongs in the scenario suite (test/scenarios/) where a real
// supervisor + stub executor + postgres store can drive the full flow.

package supervisor_test
