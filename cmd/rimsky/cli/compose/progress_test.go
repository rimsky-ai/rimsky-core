// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package compose

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func emitFullLifecycle(p ProgressPrinter) {
	p.InstanceStarting("proj", "inst")
	p.NodeRunTerminal("proj", "inst", "node1", "success", "")
	p.FrameTick("proj", "inst", "frm-2", 2)
	p.InstanceTerminal("proj", "inst", "success", 4)
	p.Summary("compose run", "all-success", 1)
	p.Finalize()
}

func jsonRecords(t *testing.T, raw string) []map[string]any {
	t.Helper()
	trimmed := strings.TrimRight(raw, "\n")
	if trimmed == "" {
		return nil
	}
	var out []map[string]any
	for i, line := range strings.Split(trimmed, "\n") {
		var rec map[string]any
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			t.Fatalf("line %d is not parseable JSON: %v (line = %q)", i, err, line)
		}
		out = append(out, rec)
	}
	return out
}

func recordEvents(recs []map[string]any) []string {
	events := make([]string, 0, len(recs))
	for _, rec := range recs {
		event, _ := rec["event"].(string)
		events = append(events, event)
	}
	return events
}

func TestDefaultVolumeReportsInstanceAndNodeLifecycleAsProse(t *testing.T) {
	var buf bytes.Buffer
	p := newProgressPrinter(&buf, false, false, false)

	p.InstanceStarting("proj", "inst")
	p.NodeRunTerminal("proj", "inst", "node1", "success", "")
	p.NodeRunTerminal("proj", "inst", "node2", "failure", "exec_error")
	p.InstanceTerminal("proj", "inst", "success", 4)

	got := buf.String()
	for _, want := range []string{
		"instance proj/inst: tracking",
		"instance proj/inst node node1: success",
		"instance proj/inst node node2: failure (exec_error)",
		"instance proj/inst: success (nodes=4)",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("default volume dropped %q; output = %q", want, got)
		}
	}
	if !strings.HasSuffix(got, "\n") {
		t.Errorf("every emitted line ends with a newline; output = %q", got)
	}
}

func TestQuietVolumeReportsOnlyTheFinalSummaryAsProse(t *testing.T) {
	var buf bytes.Buffer
	emitFullLifecycle(newProgressPrinter(&buf, true, false, false))

	got := strings.TrimRight(buf.String(), "\n")
	want := "compose run: all-success (1 instance)"
	if got != want {
		t.Errorf("quiet volume output = %q, want exactly %q", got, want)
	}
}

// @decision: progress-flags
func TestQuietVolumeUnderJSONReportsOnlyTheSummaryRecord(t *testing.T) {
	var buf bytes.Buffer
	emitFullLifecycle(newProgressPrinter(&buf, true, false, true))

	recs := jsonRecords(t, buf.String())
	if len(recs) != 1 {
		t.Fatalf("quiet with json emits one record, got %d: %v", len(recs), recordEvents(recs))
	}
	rec := recs[0]
	if rec["event"] != "summary" {
		t.Errorf("the one record is the summary, got event %v", rec["event"])
	}
	if rec["verb"] != "compose run" || rec["reason"] != "all-success" {
		t.Errorf("summary record shape wrong: %+v", rec)
	}
	if n, ok := rec["instance_count"].(float64); !ok || n != 1 {
		t.Errorf("summary record instance_count = %v, want 1", rec["instance_count"])
	}
}

// @decision: progress-flags
func TestVerboseVolumeAddsFrameTicksToBothFormats(t *testing.T) {
	var prose, records bytes.Buffer
	newProgressPrinter(&prose, false, true, false).FrameTick("proj", "inst", "frm-7", 7)
	newProgressPrinter(&records, false, true, true).FrameTick("proj", "inst", "frm-7", 7)

	if got := prose.String(); !strings.Contains(got, "instance proj/inst frame 7 (frm-7)") {
		t.Errorf("verbose prose frame tick missing; output = %q", got)
	} else if !strings.HasSuffix(got, "\n") {
		t.Errorf("every emitted line ends with a newline; output = %q", got)
	}

	recs := jsonRecords(t, records.String())
	if len(recs) != 1 || recs[0]["event"] != "frame_tick" {
		t.Fatalf("verbose with json emits one frame_tick record, got %v", recordEvents(recs))
	}
	if n, ok := recs[0]["frame"].(float64); !ok || n != 7 {
		t.Errorf("frame_tick record frame = %v, want 7", recs[0]["frame"])
	}
	if recs[0]["frame_id"] != "frm-7" {
		t.Errorf("frame_tick record frame_id = %v, want frm-7: an operator looks the frame up by its id", recs[0]["frame_id"])
	}
}

// @decision: progress-default
func TestDefaultVolumeWithholdsFrameTicksFromBothFormats(t *testing.T) {
	var prose, records bytes.Buffer
	newProgressPrinter(&prose, false, false, false).FrameTick("proj", "inst", "frm-7", 7)
	newProgressPrinter(&records, false, false, true).FrameTick("proj", "inst", "frm-7", 7)

	if got := prose.String(); got != "" {
		t.Errorf("default prose emitted a frame tick; output = %q", got)
	}
	if got := records.String(); got != "" {
		t.Errorf("default json emitted a frame tick; output = %q", got)
	}
}

// @decision: progress-flags
func TestEachVolumeKeepsItsEventSetUnderBothFormats(t *testing.T) {
	for _, tc := range []struct {
		name    string
		quiet   bool
		verbose bool
		want    []string
	}{
		{name: "quiet", quiet: true, want: []string{"summary"}},
		{name: "default", want: []string{"instance_starting", "node_terminal", "instance_terminal", "summary"}},
		{name: "verbose", verbose: true, want: []string{"instance_starting", "node_terminal", "frame_tick", "instance_terminal", "summary"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var records bytes.Buffer
			emitFullLifecycle(newProgressPrinter(&records, tc.quiet, tc.verbose, true))
			got := recordEvents(jsonRecords(t, records.String()))
			if strings.Join(got, ",") != strings.Join(tc.want, ",") {
				t.Errorf("json records = %v, want %v", got, tc.want)
			}

			var prose bytes.Buffer
			emitFullLifecycle(newProgressPrinter(&prose, tc.quiet, tc.verbose, false))
			if lines := strings.Count(strings.TrimRight(prose.String(), "\n"), "\n") + 1; lines != len(tc.want) {
				t.Errorf("prose emitted %d lines, want %d to match the json record set %v; output = %q",
					lines, len(tc.want), tc.want, prose.String())
			}
		})
	}
}

func TestJSONFormatCarriesEveryEventAsOneRecord(t *testing.T) {
	var buf bytes.Buffer
	p := newProgressPrinter(&buf, false, true, true)

	p.InstanceStarting("proj", "inst")
	p.NodeRunTerminal("proj", "inst", "node1", "success", "")
	p.InstanceTerminal("proj", "inst", "success", 4)
	p.FrameTick("proj", "inst", "frm-2", 2)

	recs := jsonRecords(t, buf.String())
	if len(recs) != 4 {
		t.Fatalf("want 4 records, got %d: %v", len(recs), recordEvents(recs))
	}
	for i, rec := range recs {
		if rec["project"] != "proj" {
			t.Errorf("record %d project = %v, want proj", i, rec["project"])
		}
		if rec["instance"] != "inst" {
			t.Errorf("record %d instance = %v, want inst", i, rec["instance"])
		}
	}
	if recs[0]["event"] != "instance_starting" {
		t.Errorf("first record event = %v, want instance_starting", recs[0]["event"])
	}
	if recs[1]["event"] != "node_terminal" || recs[1]["node"] != "node1" || recs[1]["outcome"] != "success" {
		t.Errorf("second record shape wrong: %+v", recs[1])
	}
}

func TestJSONFormatOmitsAnEmptyNodeReasonAndCarriesAPresentOne(t *testing.T) {
	var withReason, withoutReason bytes.Buffer
	newProgressPrinter(&withReason, false, false, true).NodeRunTerminal("proj", "inst", "node1", "failure", "exec_error")
	newProgressPrinter(&withoutReason, false, false, true).NodeRunTerminal("proj", "inst", "node1", "success", "")

	if rec := jsonRecords(t, withReason.String())[0]; rec["reason"] != "exec_error" {
		t.Errorf("reason field missing on a record with a non-empty reason; rec = %+v", rec)
	}
	if rec := jsonRecords(t, withoutReason.String())[0]; rec["reason"] != nil {
		t.Errorf("reason field present on a record with an empty reason; rec = %+v", rec)
	}
}

func TestDefaultAndVerboseVolumesShareTheirProse(t *testing.T) {
	emit := func(p ProgressPrinter) {
		p.InstanceStarting("proj", "inst")
		p.NodeRunTerminal("proj", "inst", "node1", "success", "")
		p.NodeRunTerminal("proj", "inst", "node2", "failure", "exec_error")
		p.InstanceTerminal("proj", "inst", "success", 4)
	}

	var defaultBuf, verboseBuf bytes.Buffer
	emit(newProgressPrinter(&defaultBuf, false, false, false))
	emit(newProgressPrinter(&verboseBuf, false, true, false))

	if defaultBuf.String() != verboseBuf.String() {
		t.Fatalf("default and verbose prose diverged on the events both volumes report\n default = %q\n verbose = %q",
			defaultBuf.String(), verboseBuf.String())
	}
}
