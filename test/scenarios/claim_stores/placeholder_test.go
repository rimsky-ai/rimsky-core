// Placeholder package for the claim_stores scenario suite under
// stores-redesign-v2.
//
// The pre-v2 tests in this directory exercised the deprecated
// `core/store/claimstorepg` package (now `core/store/postgres`) plus
// the first-delete-wins / last-released-wins held-claim resolution
// algorithm that was retired by the redesign (replaced by
// auto-terminal aggregate-outcome resolution per spec §14.4 / blessed
// invariant 13).
//
// This file keeps the package buildable. New scenarios for pick
// policies + auto-terminal resolution belong in this directory but
// must be designed against the new shape (see
// docs/specs/2026-04-27-stores-redesign-v2-design.md §14 and
// docs/plans/2026-04-27-stores-redesign-v2.md §T42/§T43).

package claim_stores
