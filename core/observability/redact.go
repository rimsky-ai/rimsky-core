package observability

// Redaction is now handled by JSON tags on the persistence Row types
// themselves: `LockHolderRow.Address` carries `json:"-"` so it is
// never serialised, satisfying spec §1.3 / blessed-invariant 20.
//
// This file is intentionally near-empty; the observability handlers
// pass `persistence.LockHolderRow` and `persistence.NodeRow` directly
// to `writeJSON` and rely on the struct tags for the wire shape.
