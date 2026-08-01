// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: Apache-2.0

//go:build !unix

package store

func assertSameFilesystem(a, b string) error { return nil }
