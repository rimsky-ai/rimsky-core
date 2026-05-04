// Package storetest provides an in-Go fake satisfying both
// foundation/locks.ClaimProducer and foundation/locks.LifecycleSubscriber
// for unit tests where the wire isn't relevant.
//
// Scenario tests in test/scenarios/ use the loopback gRPC fixtures in
// stores/<kind>/testfixture/ instead — those exercise the real
// protocol; this package skips the wire to keep unit tests fast.
package storetest
