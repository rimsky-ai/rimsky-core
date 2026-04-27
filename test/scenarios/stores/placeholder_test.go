// Placeholder package for the stores scenario suite under
// stores-redesign-v2.
//
// The pre-v2 tests in this directory used scenario.RegionRef and
// node.NodeStoreRef.{Write, Read} — both removed. Region conflict
// semantics under v2 derive from Store.RegionsConflict + the §8.5
// ModeCoexists matrix; tests that recreate the prior coverage
// (overlapping write blocks, disjoint regions concurrent, read+write
// concurrent only on staged_async) belong here but must use
// scenario.ClaimRef / WriteClaimRef with explicit selectors.

package stores
