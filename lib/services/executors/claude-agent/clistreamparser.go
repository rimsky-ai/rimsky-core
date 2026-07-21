// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package claudeagent

import (
	"encoding/json"
	"strings"
)

type CliProgressEvent struct {
	Kind string
	ID   string
	Name string
}

const maxPendingStdoutLineBytes = 8 * 1024 * 1024

type CliStreamParser struct {
	buf strings.Builder
}

func NewCliStreamParser() *CliStreamParser {
	return &CliStreamParser{}
}

func (p *CliStreamParser) Push(chunk string) []CliProgressEvent {
	p.buf.WriteString(chunk)
	pending := p.buf.String()
	var events []CliProgressEvent
	for {
		nl := strings.IndexByte(pending, '\n')
		if nl < 0 {
			break
		}
		line := strings.TrimSpace(pending[:nl])
		pending = pending[nl+1:]
		if line != "" {
			events = extractEvents(line, events)
		}
	}
	if len(pending) > maxPendingStdoutLineBytes {
		pending = pending[len(pending)-maxPendingStdoutLineBytes:]
	}
	p.buf.Reset()
	p.buf.WriteString(pending)
	return events
}

func extractEvents(line string, out []CliProgressEvent) []CliProgressEvent {
	var rec map[string]any
	if err := json.Unmarshal([]byte(line), &rec); err != nil {
		return out
	}
	msg, _ := rec["message"].(map[string]any)
	if msg == nil {
		return out
	}
	content, _ := msg["content"].([]any)
	switch rec["type"] {
	case "assistant":
		for _, blk := range content {
			b, _ := blk.(map[string]any)
			if b == nil || b["type"] != "tool_use" {
				continue
			}
			id, ok := b["id"].(string)
			if !ok {
				continue
			}
			name, _ := b["name"].(string)
			out = append(out, CliProgressEvent{Kind: "tool_use_start", ID: id, Name: name})
		}
	case "user":
		for _, blk := range content {
			b, _ := blk.(map[string]any)
			if b == nil || b["type"] != "tool_result" {
				continue
			}
			id, ok := b["tool_use_id"].(string)
			if !ok {
				continue
			}
			out = append(out, CliProgressEvent{Kind: "tool_use_end", ID: id})
		}
	}
	return out
}
