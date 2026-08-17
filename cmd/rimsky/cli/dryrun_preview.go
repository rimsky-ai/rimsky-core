// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
)

// @decision: auth-dry-run-request-flag
// @decision: auth-dry-run-mode-floor-on-key
type DryRunPreview struct {
	Intent  string
	Details map[string]any
	Body    map[string]any
}

func (p *DryRunPreview) Error() string {
	return strings.ReplaceAll(p.Intent, "_", " ")
}

// @decision: auth-dry-run-request-flag
func dryRunPreviewFromBody(body []byte) *DryRunPreview {
	var envelope map[string]any
	if err := json.Unmarshal(body, &envelope); err != nil {
		return nil
	}
	marked, _ := envelope["dry_run"].(bool)
	if !marked {
		return nil
	}
	p := &DryRunPreview{Intent: "would_have_written", Body: envelope}
	keys := make([]string, 0, len(envelope))
	for k := range envelope {
		if k != "dry_run" {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	for _, k := range keys {
		if !strings.HasPrefix(k, "would_have_") {
			continue
		}
		p.Intent = k
		p.Details, _ = envelope[k].(map[string]any)
		break
	}
	return p
}

// @decision: auth-dry-run-request-flag
func ReportDryRunPreview(err error) (int, bool) {
	var p *DryRunPreview
	if !errors.As(err, &p) {
		return 0, false
	}
	if activeFormatFlag == FormatJSON {
		_ = EmitJSON(os.Stdout, p.Body)
		return 0, true
	}
	fmt.Fprintln(os.Stdout, p.Error())
	keys := make([]string, 0, len(p.Details))
	for k := range p.Details {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	pairs := make([][2]string, 0, len(keys))
	for _, k := range keys {
		pairs = append(pairs, [2]string{k, fmt.Sprintf("%v", p.Details[k])})
	}
	if len(pairs) > 0 {
		EmitKV(os.Stdout, pairs)
	}
	return 0, true
}
