// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

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
