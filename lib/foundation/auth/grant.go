// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package auth

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
)

// Mode is the per-request write modifier, resolved from the
// `?dry_run=true` request flag (NOT from the grant — a grant entry is
// just an action string). Read actions honor the flag as a no-op.
type Mode string

const (
	// ModeExecute is the default: the handler runs normally.
	ModeExecute Mode = "execute"

	// ModeDryRun: the handler runs validation, returns a synthetic
	// dry_run-envelope response, and does NOT mutate state. Audit
	// records the attempted action with `executed: false` (for writes).
	ModeDryRun Mode = "dry_run"
)

// GrantEntry is one entry in an API-key's permission grant — an action
// string (the wildcard grammar `*`, `<noun>:*`, `*:<verb>`). Permission
// is set membership: a request is allowed iff any entry's action
// matches. There is no per-entry mode modifier; dry-run is a per-request
// flag (see Mode).
//
// Forward-compatible: unknown JSON fields are preserved in Extras so a
// future server reading a key minted by this server doesn't lose data.
// Today's parser ignores Extras for matching; V2 may consume `scope`
// / `rate_limit` etc. without a schema migration. A legacy `mode` key on
// a persisted grant now falls into Extras like any other unknown field
// (pre-v1: dropped from the matcher, no compat shim).
type GrantEntry struct {
	Action string `json:"action"`

	// Extras carries any unknown JSON fields encountered during
	// unmarshal. Preserved on the wire; ignored by the permission
	// matcher.
	Extras map[string]json.RawMessage `json:"-"`
}

// Grant is the full grant on a key — a list of entries. Evaluation is
// set membership (any matching entry allows); order is not significant.
type Grant []GrantEntry

// ErrInvalidGrant is the sentinel returned by UnmarshalJSON and
// ValidateGrant on grants that fail basic shape checks.
var ErrInvalidGrant = errors.New("auth: invalid grant")

// UnmarshalJSON preserves unknown fields in Extras and validates the
// basic shape (action is a non-empty string). A `mode` key on a legacy
// persisted grant is no longer a recognized field; it falls into Extras
// like any other unknown key and is ignored by the matcher.
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
	if len(raw) > 0 {
		g.Extras = raw
	}
	return nil
}

// MarshalJSON omits Extras if empty and preserves any extras
// encountered at unmarshal. Keys are emitted in a deterministic order —
// `action` then any extras in lexical order — so the persisted JSON
// round-trips byte-stably. (The persisted grant is later carried in
// audit `permissions` payloads; downstream consumers that hash-key the
// JSON, e.g. the V2-deferred `tools/list` cache-by-grant-hash, rely on
// this.)
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
