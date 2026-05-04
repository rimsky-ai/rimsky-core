// Placeholder package for the claim_stores scenario suite under the
// stores redesign.
//
// Pre-redesign tests in this directory exercised the deprecated
// `core/store/claimstorepg` package (now `stores/postgres/store`) plus
// the first-delete-wins / last-released-wins held-claim resolution
// algorithm that was retired by the redesign (replaced by
// auto-terminal aggregate-outcome resolution — spec §4.10 invariant
// 13).
//
// This file keeps the package buildable. New scenarios for pick
// policies + auto-terminal resolution belong in this directory but
// must be designed against the new shape (see
// docs/history/2026-04-27-stores-redesign-v3-design.md).

package claim_stores
