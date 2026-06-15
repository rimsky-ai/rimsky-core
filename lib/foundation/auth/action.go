// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package auth

import (
	"errors"
	"fmt"
	"strings"
)

// ActionMatches returns true if entryAction matches requestAction per
// the wildcard rules:
//
//   - "*" matches anything
//   - "<noun>:*" matches any requestAction starting with "<noun>:"
//   - "*:<verb>" matches any requestAction ending with ":<verb>"
//   - otherwise requires exact-string match
//
// The colon is always part of the match boundary — "auth:*" does NOT
// match "authority:create".
func ActionMatches(entryAction, requestAction string) bool {
	if entryAction == "*" {
		return true
	}
	if entryAction == requestAction {
		return true
	}
	if strings.HasSuffix(entryAction, ":*") {
		prefix := entryAction[:len(entryAction)-1]
		return strings.HasPrefix(requestAction, prefix)
	}
	if strings.HasPrefix(entryAction, "*:") {
		suffix := entryAction[1:]
		return strings.HasSuffix(requestAction, suffix)
	}
	return false
}

// ValidateActionString returns nil if entryAction is well-formed:
// exact "<noun>:<verb>", "*", "<noun>:*", or "*:<verb>". Infix
// wildcards ("foo:*:bar") and embedded asterisks ("foo*bar") are
// rejected.
func ValidateActionString(entryAction string) error {
	if entryAction == "" {
		return errors.New("action string is empty")
	}
	if entryAction == "*" {
		return nil
	}
	if !strings.Contains(entryAction, "*") {
		if !strings.Contains(entryAction, ":") {
			return fmt.Errorf("action %q must contain a ':' separator", entryAction)
		}
		return nil
	}
	if strings.HasSuffix(entryAction, ":*") {
		prefix := entryAction[:len(entryAction)-2]
		if prefix == "" || strings.Contains(prefix, "*") || strings.Contains(prefix, ":") {
			return fmt.Errorf("action %q: noun-prefix wildcard must be <noun>:* with no embedded ':' or '*'", entryAction)
		}
		return nil
	}
	if strings.HasPrefix(entryAction, "*:") {
		suffix := entryAction[2:]
		if suffix == "" || strings.Contains(suffix, "*") || strings.Contains(suffix, ":") {
			return fmt.Errorf("action %q: verb-suffix wildcard must be *:<verb> with no embedded ':' or '*'", entryAction)
		}
		return nil
	}
	return fmt.Errorf("action %q: unsupported wildcard shape (only '*', '<noun>:*', '*:<verb>' allowed)", entryAction)
}
