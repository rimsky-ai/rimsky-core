package scheduler

import "github.com/google/uuid"

// parseUUIDImpl wraps google/uuid.Parse so the invalidate path can parse a
// RestoreVersion string without leaking the dependency into the public API.
func parseUUIDImpl(s string) (uuid.UUID, error) { return uuid.Parse(s) }
