// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package auth

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
)

type Mode string

const (
	ModeExecute Mode = "execute"

	ModeDryRun Mode = "dry_run"
)

type GrantEntry struct {
	Action string `json:"action"`

	Mode Mode `json:"mode,omitempty"`

	Scope map[string]string `json:"scope,omitempty"`

	Extras map[string]json.RawMessage `json:"-"`
}

type Grant []GrantEntry

var ErrInvalidGrant = errors.New("auth: invalid grant")

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
