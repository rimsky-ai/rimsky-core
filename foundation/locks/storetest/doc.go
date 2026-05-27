// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// Package storetest provides an in-Go fake satisfying both
// foundation/locks.ClaimProducer and foundation/locks.LifecycleSubscriber
// for unit tests where the wire isn't relevant.
//
// Scenario tests in test/scenarios/ use the loopback gRPC fixtures in
// stores/<kind>/testfixture/ instead — those exercise the real
// protocol; this package skips the wire to keep unit tests fast.
package storetest
