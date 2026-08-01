// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package store

import (
	sharedledger "github.com/rimsky-ai/rimsky-core/lib/services/claim_producers/shared/claimledger"
)

type ClaimState = sharedledger.ClaimState

const (
	ClaimStateOpen      = sharedledger.ClaimStateOpen
	ClaimStateCommitted = sharedledger.ClaimStateCommitted
	ClaimStateAbandoned = sharedledger.ClaimStateAbandoned
	ClaimStateReleased  = sharedledger.ClaimStateReleased
	ClaimStateUnknown   = sharedledger.ClaimStateUnknown
)

type ClaimEvent = sharedledger.ClaimEvent

type ClaimRecord = sharedledger.ClaimRecord

type ClaimLedger = sharedledger.ClaimLedger

func NewClaimLedger(max int) *ClaimLedger {
	return sharedledger.NewClaimLedger(max)
}
