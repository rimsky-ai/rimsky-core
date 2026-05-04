// Package storetest provides an in-Go fake satisfying core/store.Store
// for unit tests in core/... where the wire isn't relevant. Per spec
// docs/history/2026-04-27-stores-redesign-v3-design.md §9.1.
//
// Scenario tests in test/scenarios/ use the loopback gRPC fixtures in
// stores/<kind>/testfixture/ instead — those exercise the real
// protocol; this package skips the wire to keep unit tests fast.
package storetest
