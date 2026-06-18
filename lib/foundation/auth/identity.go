// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package auth

import "github.com/rimsky-ai/rimsky-core/lib/foundation/shared"

type IdentityKind string

const (
	IdentityAPIKey IdentityKind = "api_key"

	IdentityAnonymous IdentityKind = "anonymous"
)

const AnonymousKeyName = "anonymous"

// @concept: api-key
// @concept: anonymous-mode
type Identity struct {
	KeyID       *shared.UUID
	KeyName     string
	Kind        IdentityKind
	Permissions Grant
}

// @concept: anonymous-mode
func AnonymousIdentity() Identity {
	return Identity{
		KeyID:       nil,
		KeyName:     AnonymousKeyName,
		Kind:        IdentityAnonymous,
		Permissions: Grant{{Action: "*"}},
	}
}
