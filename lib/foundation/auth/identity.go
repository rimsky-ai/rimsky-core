// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package auth

import "github.com/rimsky-ai/rimsky-core/lib/foundation/shared"

// IdentityKind tags audit records.
type IdentityKind string

const (
	// IdentityAPIKey is the kind for a request authenticated by an
	// API key row.
	IdentityAPIKey IdentityKind = "api_key"

	// IdentityAnonymous is the kind for requests served under
	// implicit anonymous mode (the database has zero active keys).
	IdentityAnonymous IdentityKind = "anonymous"
)

// AnonymousKeyName is the synthetic key_name carried in audit
// records for requests served under implicit anonymous mode.
const AnonymousKeyName = "anonymous"

// Identity describes the caller of a request, as resolved by the
// auth middleware. KeyID is nil for anonymous identities.
//
// @concept: api-key
// @concept: anonymous-mode
type Identity struct {
	KeyID       *shared.UUID
	KeyName     string
	Kind        IdentityKind
	Permissions Grant
}

// AnonymousIdentity returns the synthetic identity used when no
// active keys exist. Carries the admin grant `{ "action": "*" }`.
//
// @concept: anonymous-mode
func AnonymousIdentity() Identity {
	return Identity{
		KeyID:       nil,
		KeyName:     AnonymousKeyName,
		Kind:        IdentityAnonymous,
		Permissions: Grant{{Action: "*"}},
	}
}
