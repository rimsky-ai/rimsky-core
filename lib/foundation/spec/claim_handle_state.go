// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package spec

import "errors"

// @concept: claim-handle
type ClaimHandleState string

const (
	ClaimHandleStateActive    ClaimHandleState = "active"
	ClaimHandleStateCommitted ClaimHandleState = "committed"
	ClaimHandleStateAbandoned ClaimHandleState = "abandoned"
)

var ErrIllegalClaimHandleTransition = errors.New("rimsky: illegal claim-handle state transition")
