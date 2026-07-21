// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package claudeagent

import (
	"reflect"
	"strings"
	"testing"
)

func TestStreamParserEmitsToolUseStart(t *testing.T) {
	p := NewCliStreamParser()
	evs := p.Push(`{"type":"assistant","message":{"content":[{"type":"tool_use","id":"toolu_1","name":"Bash","input":{}}]}}` + "\n")
	want := []CliProgressEvent{{Kind: "tool_use_start", ID: "toolu_1", Name: "Bash"}}
	if !reflect.DeepEqual(evs, want) {
		t.Fatalf("events = %+v, want %+v", evs, want)
	}
}

func TestStreamParserEmitsToolUseEnd(t *testing.T) {
	p := NewCliStreamParser()
	evs := p.Push(`{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"toolu_1","content":"ok"}]}}` + "\n")
	want := []CliProgressEvent{{Kind: "tool_use_end", ID: "toolu_1"}}
	if !reflect.DeepEqual(evs, want) {
		t.Fatalf("events = %+v, want %+v", evs, want)
	}
}

func TestStreamParserBuffersPartialLines(t *testing.T) {
	p := NewCliStreamParser()
	line := `{"type":"assistant","message":{"content":[{"type":"tool_use","id":"toolu_2","name":"Read"}]}}`
	if evs := p.Push(line[:30]); len(evs) != 0 {
		t.Fatalf("expected no events for partial line, got %+v", evs)
	}
	evs := p.Push(line[30:] + "\n")
	want := []CliProgressEvent{{Kind: "tool_use_start", ID: "toolu_2", Name: "Read"}}
	if !reflect.DeepEqual(evs, want) {
		t.Fatalf("events = %+v, want %+v", evs, want)
	}
}

func TestStreamParserIgnoresNonJSONAndOtherShapes(t *testing.T) {
	p := NewCliStreamParser()
	evs := p.Push("not json\n" +
		`{"type":"system","subtype":"init"}` + "\n" +
		`{"type":"result","subtype":"success"}` + "\n")
	if len(evs) != 0 {
		t.Fatalf("expected no events, got %+v", evs)
	}
}

func TestStreamParserCapsUnboundedPendingLine(t *testing.T) {
	p := NewCliStreamParser()
	chunk := strings.Repeat("x", 1024*1024)
	for i := 0; i < 16; i++ {
		if evs := p.Push(chunk); len(evs) != 0 {
			t.Fatalf("expected no events for a newline-less oversized chunk, got %+v", evs)
		}
	}
	if got := p.buf.Len(); got > maxPendingStdoutLineBytes {
		t.Fatalf("pending buffer = %d bytes, want capped at %d (an adversarial newline-less stream must not grow memory unbounded)", got, maxPendingStdoutLineBytes)
	}
}

func TestStreamParserEmitsMultipleParallelToolUses(t *testing.T) {
	p := NewCliStreamParser()
	evs := p.Push(`{"type":"assistant","message":{"content":[` +
		`{"type":"text","text":"thinking"},` +
		`{"type":"tool_use","id":"toolu_A","name":"Bash"},` +
		`{"type":"tool_use","id":"toolu_B","name":"Read"}]}}` + "\n")
	want := []CliProgressEvent{
		{Kind: "tool_use_start", ID: "toolu_A", Name: "Bash"},
		{Kind: "tool_use_start", ID: "toolu_B", Name: "Read"},
	}
	if !reflect.DeepEqual(evs, want) {
		t.Fatalf("events = %+v, want %+v", evs, want)
	}
}
