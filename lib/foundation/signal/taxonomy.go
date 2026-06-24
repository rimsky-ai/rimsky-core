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
	"transient/park/snooze",
	"transient/park/await_callback",
	"transient/retry/*",
	"transient/infra/*",
	"transient/release_and_requeue/*",
	"transient/await_async",
	"attribute/*/changed",
}

var canonicalSubscriptionPatterns = []string{
	"terminal/success",
	"terminal/error/*",
	"transient/retry/*",
	"transient/infra/*",
	"transient/release_and_requeue/*",
	"transient/await_async",
	"attribute/*/changed",
}

func ValidateTypePath(t TypePath) error {
	s := string(t)
	if s == "" {
		return fmt.Errorf("invalid signal type-path: empty")
	}
	if matchesPatterns(canonicalEmitPatterns, s, false) {
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
	if isExplicitParkSubscription(s) {
		return fmt.Errorf(
			"invalid subscription type: %q (transient/park/* signals are audit-only — "+
				"subscribers cannot fire on park settles; subscribe to terminal/success or "+
				"terminal/error/<class> for the eventual run-terminating settlement)", s)
	}
	if matchesPatterns(canonicalSubscriptionPatterns, s, true) {
		return nil
	}
	return fmt.Errorf("invalid subscription type: %q (not in canonical taxonomy)", s)
}

func isExplicitParkSubscription(s string) bool {
	switch s {
	case "transient/park/snooze",
		"transient/park/await_callback",
		"transient/park/*":
		return true
	}
	return false
}

func positionalWildcard(s string) bool {
	idx := strings.IndexByte(s, '*')
	if idx < 0 {
		return false
	}
	return idx != len(s)-1
}

func matchesPatterns(patterns []string, s string, allowTrailingWildcard bool) bool {
	if allowTrailingWildcard && strings.HasSuffix(s, "*") {
		prefix := strings.TrimSuffix(s, "*")
		if prefix == "" {
			return false
		}
		for _, p := range patterns {
			canon := strings.TrimSuffix(p, "*")
			if strings.HasPrefix(canon, prefix) || strings.HasPrefix(prefix, canon) {
				return true
			}
		}
		return false
	}

	for _, p := range patterns {
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
