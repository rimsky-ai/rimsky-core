// Stores scenario suite under the stores redesign.
//
// Pre-redesign tests in this directory used scenario.RegionRef and
// node.NodeStoreRef.{Write, Read} — both removed. Under v3 scope
// conflict is byte-equal (Store.RegionsConflict / Store.UnmarshalRegion
// retired per spec §11.1, §7.7); coexistence is governed by
// locks.ModeCoexists, the store's WriteSemantics, and the
// claim's intent. Tests that recreate the prior coverage
// (overlapping write blocks, disjoint regions concurrent, read+write
// concurrent only on staged_async) drive the loopback gRPC fixture
// (see regional_claim_test.go for the scaffolding).

package stores
