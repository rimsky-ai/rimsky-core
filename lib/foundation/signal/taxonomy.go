// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package signal

import (
	"fmt"
	"strings"
)

var canonicalEmitPatterns = []string{
	"terminal/success",
	"terminal/error/*",
	"terminal/park/snooze",
	"terminal/park/await_callback",
	"terminal/infra/*",
	"transient/retry/*",
	"transient/await_async",
	"attribute/*/changed",
}

func ValidateTypePath(t TypePath) error {
	s := string(t)
	if s == "" {
		return fmt.Errorf("invalid signal type-path: empty")
	}
	if matchesCanonical(s, false) {
		return nil
	}
	return fmt.Errorf("invalid signal type-path: %q (not in canonical taxonomy)", s)
}

func ValidateSubscriptionType(t TypePath) error {
	s := string(t)
	if s == "" {
		return fmt.Errorf("invalid subscription type: empty")
	}
	if positionalWildcard(s) {
		return fmt.Errorf(
			"invalid subscription type: %q (positional wildcards not supported; use trailing-*)", s)
	}
	if matchesCanonical(s, true) {
		return nil
	}
	return fmt.Errorf("invalid subscription type: %q (not in canonical taxonomy)", s)
}

func positionalWildcard(s string) bool {
	idx := strings.IndexByte(s, '*')
	if idx < 0 {
		return false
	}
	return idx != len(s)-1
}

func matchesCanonical(s string, allowTrailingWildcard bool) bool {
	if allowTrailingWildcard && strings.HasSuffix(s, "*") {
		prefix := strings.TrimSuffix(s, "*")
		if prefix == "" {
			return false
		}
		for _, p := range canonicalEmitPatterns {
			canon := strings.TrimSuffix(p, "*")
			if strings.HasPrefix(canon, prefix) || strings.HasPrefix(prefix, canon) {
				return true
			}
		}
		return false
	}

	for _, p := range canonicalEmitPatterns {
		if p == "attribute/*/changed" {
			const prefix = "attribute/"
			const suffix = "/changed"
			if len(s) < len(prefix)+len(suffix) {
				continue
			}
			if !strings.HasPrefix(s, prefix) || !strings.HasSuffix(s, suffix) {
				continue
			}
			middle := s[len(prefix) : len(s)-len(suffix)]
			if middle == "" || strings.Contains(middle, "/") {
				continue
			}
			return true
		}
		if !strings.HasSuffix(p, "*") {
			if s == p {
				return true
			}
			continue
		}
		canonPrefix := strings.TrimSuffix(p, "*")
		if !strings.HasPrefix(s, canonPrefix) {
			continue
		}
		tail := s[len(canonPrefix):]
		if tail == "" {
			continue
		}
		return true
	}
	return false
}
