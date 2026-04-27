// Placeholder for the supervisor error-handling test fixture under
// stores-redesign-v2.
//
// The pre-v2 on_error_test.go exercised the policy chain
// (discard_then_retry / give_up / invalidate(targets)) against the old
// release surface. The policy chain itself is unchanged but now
// integrates with auto-terminal release per spec §14.4; coverage
// belongs in the scenario suite where the full Open/Commit/Abandon
// flow is reachable.

package supervisor_test
