// Copyright © 2026 Fall Guy Consulting.
// Licensed under the Apache License, Version 2.0. See LICENSE.apache at the
// repo root, or http://www.apache.org/licenses/LICENSE-2.0.

//go:build !unix

package store

// assertSameFilesystem is a no-op on non-Unix substrates. The reference
// producer targets Unix; non-Unix builds compile for IDE/tooling
// reasons but the rename atomicity guarantee can't be verified the
// same way (e.g., Windows handles cross-volume renames differently).
func assertSameFilesystem(a, b string) error { return nil }
