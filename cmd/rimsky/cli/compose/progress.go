// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

// @decision: progress-default
// @decision: progress-flags
// @story: live-progress
package compose

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"sync"
)

type ProgressPrinter interface {
	InstanceStarting(project, name string)
	NodeRunTerminal(project, name, nodeID, outcome, reason string)
	InstanceTerminal(project, name, outcome string, nodeCount int)
	FrameTick(project, name, frameID string, frameNo int)
	Summary(verb, reason string, instanceCount int)
	Finalize()
}

type progressVolume int

const (
	volumeQuiet progressVolume = iota
	volumeDefault
	volumeVerbose
)

func progressVolumeFor(quiet, verbose bool) progressVolume {
	switch {
	case quiet:
		return volumeQuiet
	case verbose:
		return volumeVerbose
	default:
		return volumeDefault
	}
}

type progressFormat interface {
	instanceStarting(project, name string)
	nodeRunTerminal(project, name, nodeID, outcome, reason string)
	instanceTerminal(project, name, outcome string, nodeCount int)
	frameTick(project, name, frameID string, frameNo int)
	summary(verb, reason string, instanceCount int)
	finalize()
}

func newProgressPrinter(w io.Writer, quiet, verbose, jsonMode bool) ProgressPrinter {
	format := progressFormat(newLineFormat(w))
	if jsonMode {
		format = newJSONFormat(w)
	}
	return volumeGate{format: format, volume: progressVolumeFor(quiet, verbose)}
}

type volumeGate struct {
	format progressFormat
	volume progressVolume
}

func (g volumeGate) InstanceStarting(project, name string) {
	if g.volume < volumeDefault {
		return
	}
	g.format.instanceStarting(project, name)
}

func (g volumeGate) NodeRunTerminal(project, name, nodeID, outcome, reason string) {
	if g.volume < volumeDefault {
		return
	}
	g.format.nodeRunTerminal(project, name, nodeID, outcome, reason)
}

func (g volumeGate) InstanceTerminal(project, name, outcome string, nodeCount int) {
	if g.volume < volumeDefault {
		return
	}
	g.format.instanceTerminal(project, name, outcome, nodeCount)
}

func (g volumeGate) FrameTick(project, name, frameID string, frameNo int) {
	if g.volume < volumeVerbose {
		return
	}
	g.format.frameTick(project, name, frameID, frameNo)
}

func (g volumeGate) Summary(verb, reason string, instanceCount int) {
	g.format.summary(verb, reason, instanceCount)
}

func (g volumeGate) Finalize() { g.format.finalize() }

type lineFormat struct {
	mu  sync.Mutex
	buf *bufio.Writer
}

func newLineFormat(w io.Writer) *lineFormat {
	return &lineFormat{buf: bufio.NewWriter(w)}
}

func (f *lineFormat) emit(line string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, err := f.buf.WriteString(line); err != nil {
		return err
	}
	if !endsWithNewline(line) {
		if _, err := f.buf.WriteString("\n"); err != nil {
			return err
		}
	}
	return f.buf.Flush()
}

func (f *lineFormat) instanceStarting(project, name string) {
	_ = f.emit(fmt.Sprintf("instance %s/%s: tracking", project, name))
}

func (f *lineFormat) nodeRunTerminal(project, name, nodeID, outcome, reason string) {
	if reason == "" {
		_ = f.emit(fmt.Sprintf("instance %s/%s node %s: %s", project, name, nodeID, outcome))
		return
	}
	_ = f.emit(fmt.Sprintf("instance %s/%s node %s: %s (%s)", project, name, nodeID, outcome, reason))
}

func (f *lineFormat) instanceTerminal(project, name, outcome string, nodeCount int) {
	_ = f.emit(fmt.Sprintf("instance %s/%s: %s (nodes=%d)", project, name, outcome, nodeCount))
}

func (f *lineFormat) frameTick(project, name, frameID string, frameNo int) {
	_ = f.emit(fmt.Sprintf("instance %s/%s frame %d (%s)", project, name, frameNo, frameID))
}

func (f *lineFormat) summary(verb, reason string, instanceCount int) {
	if instanceCount > 0 {
		_ = f.emit(fmt.Sprintf("%s: %s (%d instance%s)", verb, reason, instanceCount, pluralS(instanceCount)))
		return
	}
	_ = f.emit(fmt.Sprintf("%s: %s", verb, reason))
}

func (f *lineFormat) finalize() {
	f.mu.Lock()
	defer f.mu.Unlock()
	_ = f.buf.Flush()
}

func endsWithNewline(s string) bool {
	return len(s) > 0 && s[len(s)-1] == '\n'
}

type jsonFormat struct {
	mu  sync.Mutex
	buf *bufio.Writer
}

func newJSONFormat(w io.Writer) *jsonFormat {
	return &jsonFormat{buf: bufio.NewWriter(w)}
}

func (f *jsonFormat) writeRecord(rec map[string]any) {
	f.mu.Lock()
	defer f.mu.Unlock()
	body, err := json.Marshal(rec)
	if err != nil {
		_, _ = f.buf.WriteString(fmt.Sprintf(`{"event":"_marshal_error","detail":%q}`+"\n", err.Error()))
		_ = f.buf.Flush()
		return
	}
	_, _ = f.buf.Write(body)
	_, _ = f.buf.WriteString("\n")
	_ = f.buf.Flush()
}

func (f *jsonFormat) instanceStarting(project, name string) {
	f.writeRecord(map[string]any{
		"event":    "instance_starting",
		"project":  project,
		"instance": name,
	})
}

func (f *jsonFormat) nodeRunTerminal(project, name, nodeID, outcome, reason string) {
	rec := map[string]any{
		"event":    "node_terminal",
		"project":  project,
		"instance": name,
		"node":     nodeID,
		"outcome":  outcome,
	}
	if reason != "" {
		rec["reason"] = reason
	}
	f.writeRecord(rec)
}

func (f *jsonFormat) instanceTerminal(project, name, outcome string, nodeCount int) {
	f.writeRecord(map[string]any{
		"event":    "instance_terminal",
		"project":  project,
		"instance": name,
		"outcome":  outcome,
		"nodes":    nodeCount,
	})
}

func (f *jsonFormat) frameTick(project, name, frameID string, frameNo int) {
	f.writeRecord(map[string]any{
		"event":    "frame_tick",
		"project":  project,
		"instance": name,
		"frame":    frameNo,
		"frame_id": frameID,
	})
}

func (f *jsonFormat) summary(verb, reason string, instanceCount int) {
	rec := map[string]any{
		"event":  "summary",
		"verb":   verb,
		"reason": reason,
	}
	if instanceCount > 0 {
		rec["instance_count"] = instanceCount
	}
	f.writeRecord(rec)
}

func (f *jsonFormat) finalize() {
	f.mu.Lock()
	defer f.mu.Unlock()
	_ = f.buf.Flush()
}
