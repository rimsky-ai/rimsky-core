// Shared helpers for the scenarios package.
package scenarios

import "time"

// timeNow centralizes time.Now for tests that need to pass EnqueuedAt into
// the dispatch queue with the real wall clock.
func timeNow() time.Time { return time.Now() }
