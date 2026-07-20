// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package compose

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func TestDefaultPrinter_LineFlushed(t *testing.T) {
	var buf bytes.Buffer
	p := newDefaultPrinter(&buf)

	p.InstanceStarting("proj", "inst")
	got := buf.String()
	if !strings.Contains(got, "instance proj/inst: tracking") {
		t.Fatalf("InstanceStarting line not flushed; buffer = %q", got)
	}
	if !strings.HasSuffix(got, "\n") {
		t.Errorf("default printer must terminate lines with newline; got %q", got)
	}

	buf.Reset()
	p.NodeRunTerminal("proj", "inst", "node1", "success", "")
	got = buf.String()
	if !strings.Contains(got, "instance proj/inst node node1: success") {
		t.Fatalf("NodeRunTerminal line not flushed; buffer = %q", got)
	}
	if !strings.HasSuffix(got, "\n") {
		t.Errorf("default printer must terminate lines with newline; got %q", got)
	}
}

func TestQuietPrinter_SuppressesEvents(t *testing.T) {
	var buf bytes.Buffer
	p := newQuietPrinter(&buf)

	p.InstanceStarting("proj", "inst")
	p.NodeRunTerminal("proj", "inst", "node1", "success", "")
	p.InstanceTerminal("proj", "inst", "success", 3)
	p.FrameTick("proj", "inst", 2)
	p.Finalize()

	if got := buf.String(); got != "" {
		t.Errorf("quiet printer must suppress all lifecycle events; got %q", got)
	}
}

func TestVerbosePrinter_EmitsFrameTicks(t *testing.T) {
	var verboseBuf, defaultBuf bytes.Buffer

	v := newVerbosePrinter(&verboseBuf)
	v.FrameTick("proj", "inst", 7)
	got := verboseBuf.String()
	if !strings.Contains(got, "instance proj/inst frame 7") {
		t.Errorf("verbose printer FrameTick line missing; got %q", got)
	}
	if !strings.HasSuffix(got, "\n") {
		t.Errorf("verbose printer must terminate lines with newline; got %q", got)
	}

	d := newDefaultPrinter(&defaultBuf)
	d.FrameTick("proj", "inst", 7)
	if got := defaultBuf.String(); got != "" {
		t.Errorf("default printer FrameTick must be a no-op; got %q", got)
	}
}

func TestJSONPrinter_EmitsJSONLines(t *testing.T) {
	var buf bytes.Buffer
	p := newJSONPrinter(&buf)

	p.InstanceStarting("proj", "inst")
	p.NodeRunTerminal("proj", "inst", "node1", "success", "")
	p.InstanceTerminal("proj", "inst", "success", 4)
	p.FrameTick("proj", "inst", 2)

	lines := strings.Split(strings.TrimRight(buf.String(), "\n"), "\n")
	if len(lines) != 4 {
		t.Fatalf("want 4 JSON lines, got %d; buffer = %q", len(lines), buf.String())
	}
	for i, line := range lines {
		var rec map[string]any
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			t.Fatalf("line %d not parseable JSON: %v (line = %q)", i, err, line)
		}
		if _, ok := rec["event"]; !ok {
			t.Errorf("line %d missing event key; rec = %+v", i, rec)
		}
		if rec["project"] != "proj" {
			t.Errorf("line %d project field = %v, want proj", i, rec["project"])
		}
	}

	var first map[string]any
	_ = json.Unmarshal([]byte(lines[0]), &first)
	if first["event"] != "instance_starting" {
		t.Errorf("first line event = %v, want instance_starting", first["event"])
	}
	var second map[string]any
	_ = json.Unmarshal([]byte(lines[1]), &second)
	if second["event"] != "node_terminal" || second["node"] != "node1" || second["outcome"] != "success" {
		t.Errorf("second line shape wrong: %+v", second)
	}
}

func TestJSONPrinter_SummaryEmitsParseableJSONLine(t *testing.T) {
	var buf bytes.Buffer
	p := newJSONPrinter(&buf)

	p.InstanceStarting("proj", "inst")
	p.Summary("compose run", "all-success", 3)

	lines := strings.Split(strings.TrimRight(buf.String(), "\n"), "\n")
	if len(lines) != 2 {
		t.Fatalf("want 2 JSON lines, got %d; buffer = %q", len(lines), buf.String())
	}
	var rec map[string]any
	if err := json.Unmarshal([]byte(lines[len(lines)-1]), &rec); err != nil {
		t.Fatalf("trailing summary line is not parseable JSON: %v (line = %q)", err, lines[len(lines)-1])
	}
	if rec["event"] != "summary" || rec["verb"] != "compose run" || rec["reason"] != "all-success" {
		t.Errorf("summary record shape wrong: %+v", rec)
	}
	if n, ok := rec["instance_count"].(float64); !ok || n != 3 {
		t.Errorf("summary record instance_count = %v, want 3", rec["instance_count"])
	}
}

func TestJSONPrinter_NodeRunTerminalReasonOptional(t *testing.T) {
	var buf bytes.Buffer
	p := newJSONPrinter(&buf)

	p.NodeRunTerminal("proj", "inst", "node1", "failure", "exec_error")
	var rec map[string]any
	if err := json.Unmarshal(bytes.TrimRight(buf.Bytes(), "\n"), &rec); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if rec["reason"] != "exec_error" {
		t.Errorf("reason field missing on JSON event with non-empty reason; rec = %+v", rec)
	}
}

func TestLinePrinter_ProseSingleSource(t *testing.T) {
	emit := func(p ProgressPrinter) string {
		p.InstanceStarting("proj", "inst")
		p.NodeRunTerminal("proj", "inst", "node1", "success", "")
		p.NodeRunTerminal("proj", "inst", "node2", "failure", "exec_error")
		p.InstanceTerminal("proj", "inst", "success", 4)
		return ""
	}

	var defaultBuf, verboseBuf bytes.Buffer
	d := newDefaultPrinter(&defaultBuf)
	v := newVerbosePrinter(&verboseBuf)
	emit(d)
	emit(v)

	if defaultBuf.String() != verboseBuf.String() {
		t.Fatalf("progress-prose-single-source: default and verbose prose diverged\n default = %q\n verbose = %q",
			defaultBuf.String(), verboseBuf.String())
	}
}

func TestNewProgressPrinter_FlagDispatch(t *testing.T) {
	var buf bytes.Buffer

	if _, ok := newProgressPrinter(&buf, false, false, false).(*defaultPrinter); !ok {
		t.Errorf("default flags should produce *defaultPrinter")
	}
	if _, ok := newProgressPrinter(&buf, true, false, false).(*quietPrinter); !ok {
		t.Errorf("--quiet should produce *quietPrinter")
	}
	if _, ok := newProgressPrinter(&buf, false, true, false).(*verbosePrinter); !ok {
		t.Errorf("--verbose should produce *verbosePrinter")
	}
	if _, ok := newProgressPrinter(&buf, false, false, true).(*jsonPrinter); !ok {
		t.Errorf("--json should produce *jsonPrinter")
	}
	// @decision: progress-flags
	if _, ok := newProgressPrinter(&buf, true, false, true).(*jsonPrinter); !ok {
		t.Errorf("--quiet --json should still produce *jsonPrinter")
	}
}
