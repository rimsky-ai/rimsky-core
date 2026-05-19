// Copyright © 2026 Fall Guy Consulting.
// Licensed under the Apache License, Version 2.0. See LICENSE.apache at the
// repo root, or http://www.apache.org/licenses/LICENSE-2.0.

package auth

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
)

// Mode is the per-grant-entry write modifier. Read actions ignore
// Mode entirely.
type Mode string

const (
	// ModeExecute is the default: the handler runs normally.
	ModeExecute Mode = "execute"

	// ModeDryRun: the handler runs validation, returns a synthetic
	// dry_run-envelope response, and does NOT mutate state. Audit
	// records the attempted action with `executed: false`.
	ModeDryRun Mode = "dry_run"
)

// GrantEntry is one entry in an API-key's permission grant.
// Forward-compatible: unknown JSON fields are preserved in Extras so a
// future server reading a key minted by this server doesn't lose data.
// Today's parser ignores Extras for matching; V2 may consume `scope`
// / `rate_limit` etc. without a schema migration.
type GrantEntry struct {
	Action string `json:"action"`
	Mode   Mode   `json:"mode,omitempty"`

	// Extras carries any unknown JSON fields encountered during
	// unmarshal. Preserved on the wire; ignored by the permission
	// matcher.
	Extras map[string]json.RawMessage `json:"-"`
}

// Grant is the full grant on a key — an ordered list of entries.
// First-match-wins; ordering is significant for mode resolution.
type Grant []GrantEntry

// ErrInvalidGrant is the sentinel returned by UnmarshalJSON and
// ValidateGrant on grants that fail basic shape checks.
var ErrInvalidGrant = errors.New("auth: invalid grant")

// UnmarshalJSON preserves unknown fields in Extras and validates the
// basic shape (action is a non-empty string; mode if set is a known
// value).
func (g *GrantEntry) UnmarshalJSON(data []byte) error {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return fmt.Errorf("grant entry: %w", err)
	}
	var actionStr string
	if v, ok := raw["action"]; ok {
		if err := json.Unmarshal(v, &actionStr); err != nil {
			return fmt.Errorf("grant entry: action: %w", err)
		}
	}
	if actionStr == "" {
		return fmt.Errorf("%w: action is required and must be non-empty", ErrInvalidGrant)
	}
	g.Action = actionStr
	delete(raw, "action")
	if v, ok := raw["mode"]; ok {
		var modeStr string
		if err := json.Unmarshal(v, &modeStr); err != nil {
			return fmt.Errorf("grant entry: mode: %w", err)
		}
		switch Mode(modeStr) {
		case ModeExecute, ModeDryRun:
			g.Mode = Mode(modeStr)
		default:
			return fmt.Errorf("%w: mode must be %q or %q (got %q)", ErrInvalidGrant, ModeExecute, ModeDryRun, modeStr)
		}
		delete(raw, "mode")
	}
	if len(raw) > 0 {
		g.Extras = raw
	}
	return nil
}

// MarshalJSON omits Mode if zero, omits Extras if empty, and
// preserves any extras encountered at unmarshal. Keys are emitted in
// a deterministic order — `action` then `mode` then any extras in
// lexical order — so the persisted JSON round-trips byte-stably.
// (The persisted grant is later carried in audit `permissions`
// payloads; downstream consumers that hash-key the JSON, e.g. the
// V2-deferred `tools/list` cache-by-grant-hash, rely on this.)
func (g GrantEntry) MarshalJSON() ([]byte, error) {
	// Build the output by hand so the key order is deterministic
	// regardless of Go's randomized map iteration.
	var buf bytes.Buffer
	buf.WriteByte('{')
	// "action": <action>
	actionJSON, err := json.Marshal(g.Action)
	if err != nil {
		return nil, err
	}
	buf.WriteString(`"action":`)
	buf.Write(actionJSON)
	// "mode": <mode> (omit-empty)
	if g.Mode != "" {
		modeJSON, err := json.Marshal(g.Mode)
		if err != nil {
			return nil, err
		}
		buf.WriteString(`,"mode":`)
		buf.Write(modeJSON)
	}
	// Extras in sorted-key order.
	if len(g.Extras) > 0 {
		keys := make([]string, 0, len(g.Extras))
		for k := range g.Extras {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			kJSON, err := json.Marshal(k)
			if err != nil {
				return nil, err
			}
			buf.WriteByte(',')
			buf.Write(kJSON)
			buf.WriteByte(':')
			buf.Write(g.Extras[k])
		}
	}
	buf.WriteByte('}')
	return buf.Bytes(), nil
}
