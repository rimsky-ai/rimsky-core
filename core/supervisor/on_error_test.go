// Placeholder for the supervisor error-handling test fixture under
// the stores redesign.
//
// The pre-redesign on_error_test.go exercised the policy chain
// (discard_then_retry / give_up / invalidate(targets)) against the
// old release surface. The policy chain itself is unchanged but now
// integrates with auto-terminal release (spec §4.10 invariant 13);
// coverage belongs in the scenario suite where the full
// Open/Commit/Abandon flow is reachable.

package supervisor_test
