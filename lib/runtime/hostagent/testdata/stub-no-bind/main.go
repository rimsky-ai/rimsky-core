// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// stub-no-bind is a fixture for the readiness-timeout reap path: it ignores
// RIMSKY_AGENT_PORT, never binds a listener, and sleeps long enough that any
// reasonable test timeout fires first. SpawnService should kill+reap this
// child on readiness timeout, leaving no leaked process behind.
package main

import "time"

func main() {
	time.Sleep(60 * time.Second)
}
