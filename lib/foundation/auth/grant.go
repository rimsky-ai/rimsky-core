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

// Mode is the write modifier resolved per request. It has two sources
// that compose into a floor-and-request model: a grant entry MAY pin an
// identity-bound `mode` floor (e.g. an attempt-only key whose write
// entries carry `dry_run`), and the caller MAY additionally request
// dry-run per request via the `?dry_run=true` flag. The effective mode
// is the stricter of the two — a grant pinned to `dry_run` can never be
// escalated to `execute` by omitting the flag. Read actions honor the
// flag as a no-op.
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
// string (the wildcard grammar `*`, `<noun>:*`, `*:<verb>`) plus two
// optional matcher-consumed modifiers:
//
//   - Mode pins an identity-bound write floor. Empty defaults to
//     ModeExecute at evaluation time (the field stays empty on the
//     struct; the evaluator defaults it). A `dry_run` floor makes the
//     entry attempt-only: a matching write previews but never commits,
//     no matter what the caller's `?dry_run` flag says.
//   - Scope is a resource selector. Empty/nil means unscoped (matches
//     any target). A non-empty selector is satisfied only when the
//     request target carries every selector key with an equal value
//     (subset-satisfaction, see ScopeMatches).
//
// Permission is set membership: a request is allowed iff any entry both
// action-matches AND scope-matches; the matched entry's Mode is returned.
//
// Forward-compatible: genuinely-unknown JSON fields are preserved in
// Extras so a future server reading a key minted by this server doesn't
// lose data. `mode` and `scope` are now recognized first-class fields
// (lifted out of Extras); other unknown keys (e.g. `rate_limit`) still
// land in Extras and a later server version may consume them without a
// schema migration.
type GrantEntry struct {
	Action string `json:"action"`

	// Mode is the identity-bound write floor for this entry. Omitted on
	// the wire when empty; the evaluator defaults empty to ModeExecute.
	Mode Mode `json:"mode,omitempty"`

	// Scope is the resource selector this entry is restricted to.
	// Omitted on the wire when empty/nil; an empty selector is unscoped.
	Scope map[string]string `json:"scope,omitempty"`

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

// UnmarshalJSON decodes the recognized fields (`action`, `mode`,
// `scope`) and preserves any remaining unknown keys in Extras. It
// validates the basic shape: `action` is a non-empty string and `mode`,
// when present, is one of "" | "execute" | "dry_run". An empty/absent
// `mode` is left empty on the struct (the evaluator defaults it to
// ModeExecute), so byte-stable round-trips don't gain a `mode` key.
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
		case "", ModeExecute, ModeDryRun:
			g.Mode = Mode(modeStr)
		default:
			return fmt.Errorf("%w: mode %q must be \"\", %q, or %q", ErrInvalidGrant, modeStr, ModeExecute, ModeDryRun)
		}
		delete(raw, "mode")
	}

	if v, ok := raw["scope"]; ok {
		var scope map[string]string
		if err := json.Unmarshal(v, &scope); err != nil {
			return fmt.Errorf("grant entry: scope: %w", err)
		}
		if len(scope) > 0 {
			g.Scope = scope
		}
		delete(raw, "scope")
	}

	if len(raw) > 0 {
		g.Extras = raw
	}
	return nil
}

// MarshalJSON emits keys in a deterministic order — `action`, then
// `mode` (only when non-empty), then `scope` (only when non-empty, with
// its keys sorted), then any Extras in lexical order — so the persisted
// JSON round-trips byte-stably. (The persisted grant is later carried in
// audit `permissions` payloads; downstream consumers that hash-key the
// JSON, e.g. the V2-deferred `tools/list` cache-by-grant-hash, rely on
// this.) Empty Mode/Scope are omitted, so an entry that names neither
// modifier marshals identically to the pre-modifier `{"action":...}`
// shape.
func (g GrantEntry) MarshalJSON() ([]byte, error) {
	var buf bytes.Buffer
	buf.WriteByte('{')
	actionJSON, err := json.Marshal(g.Action)
	if err != nil {
		return nil, err
	}
	buf.WriteString(`"action":`)
	buf.Write(actionJSON)
	if g.Mode != "" {
		modeJSON, err := json.Marshal(string(g.Mode))
		if err != nil {
			return nil, err
		}
		buf.WriteString(`,"mode":`)
		buf.Write(modeJSON)
	}
	// @deliberate: scope is written by-hand with sorted keys so the
	// selector serializes byte-stably regardless of map iteration order.
	if len(g.Scope) > 0 {
		buf.WriteString(`,"scope":`)
		if err := writeSortedStringMap(&buf, g.Scope); err != nil {
			return nil, err
		}
	}
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

// writeSortedStringMap serializes a string→string map as a JSON object
// with keys in lexical order, so the output is byte-stable across Go's
// randomized map iteration (load-bearing for the byte-stable grant
// round-trip the audit hash-key relies on).
func writeSortedStringMap(buf *bytes.Buffer, m map[string]string) error {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	buf.WriteByte('{')
	for i, k := range keys {
		if i > 0 {
			buf.WriteByte(',')
		}
		kJSON, err := json.Marshal(k)
		if err != nil {
			return err
		}
		vJSON, err := json.Marshal(m[k])
		if err != nil {
			return err
		}
		buf.Write(kJSON)
		buf.WriteByte(':')
		buf.Write(vJSON)
	}
	buf.WriteByte('}')
	return nil
}
