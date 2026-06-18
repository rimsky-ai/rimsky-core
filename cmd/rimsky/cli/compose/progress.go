// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

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
	InstanceTerminal(project, name, outcome string, frames int)
	FrameTick(project, name string, frameNo int)
	Finalize()
}

func newProgressPrinter(w io.Writer, quiet, verbose, jsonMode bool) ProgressPrinter {
	if jsonMode {
		return newJSONPrinter(w)
	}
	if quiet {
		return newQuietPrinter(w)
	}
	if verbose {
		return newVerbosePrinter(w)
	}
	return newDefaultPrinter(w)
}

type linePrinter struct {
	mu  sync.Mutex
	buf *bufio.Writer
}

func newLinePrinter(w io.Writer) *linePrinter {
	return &linePrinter{buf: bufio.NewWriter(w)}
}

func (lp *linePrinter) emit(line string) error {
	lp.mu.Lock()
	defer lp.mu.Unlock()
	if _, err := lp.buf.WriteString(line); err != nil {
		return err
	}
	if !endsWithNewline(line) {
		if _, err := lp.buf.WriteString("\n"); err != nil {
			return err
		}
	}
	return lp.buf.Flush()
}

func (lp *linePrinter) InstanceStarting(project, name string) {
	_ = lp.emit(fmt.Sprintf("instance %s/%s: tracking", project, name))
}

func (lp *linePrinter) NodeRunTerminal(project, name, nodeID, outcome, reason string) {
	if reason == "" {
		_ = lp.emit(fmt.Sprintf("instance %s/%s node %s: %s", project, name, nodeID, outcome))
		return
	}
	_ = lp.emit(fmt.Sprintf("instance %s/%s node %s: %s (%s)", project, name, nodeID, outcome, reason))
}

func (lp *linePrinter) InstanceTerminal(project, name, outcome string, frames int) {
	_ = lp.emit(fmt.Sprintf("instance %s/%s: %s (frames=%d)", project, name, outcome, frames))
}

func (lp *linePrinter) FrameTick(project, name string, frameNo int) {}

func (lp *linePrinter) Finalize() {
	lp.mu.Lock()
	defer lp.mu.Unlock()
	_ = lp.buf.Flush()
}

func endsWithNewline(s string) bool {
	return len(s) > 0 && s[len(s)-1] == '\n'
}

type defaultPrinter struct{ *linePrinter }

func newDefaultPrinter(w io.Writer) *defaultPrinter {
	return &defaultPrinter{linePrinter: newLinePrinter(w)}
}

type quietPrinter struct{ *linePrinter }

func newQuietPrinter(w io.Writer) *quietPrinter {
	return &quietPrinter{linePrinter: newLinePrinter(w)}
}

func (p *quietPrinter) InstanceStarting(project, name string)                         {}
func (p *quietPrinter) NodeRunTerminal(project, name, nodeID, outcome, reason string) {}
func (p *quietPrinter) InstanceTerminal(project, name, outcome string, frames int)    {}
func (p *quietPrinter) FrameTick(project, name string, frameNo int)                   {}

type verbosePrinter struct{ *linePrinter }

func newVerbosePrinter(w io.Writer) *verbosePrinter {
	return &verbosePrinter{linePrinter: newLinePrinter(w)}
}

func (p *verbosePrinter) FrameTick(project, name string, frameNo int) {
	_ = p.emit(fmt.Sprintf("instance %s/%s frame %d", project, name, frameNo))
}

type jsonPrinter struct {
	mu  sync.Mutex
	buf *bufio.Writer
}

func newJSONPrinter(w io.Writer) *jsonPrinter {
	return &jsonPrinter{buf: bufio.NewWriter(w)}
}

func (p *jsonPrinter) writeRecord(rec map[string]any) {
	p.mu.Lock()
	defer p.mu.Unlock()
	body, err := json.Marshal(rec)
	if err != nil {
		_, _ = p.buf.WriteString(fmt.Sprintf(`{"event":"_marshal_error","detail":%q}`+"\n", err.Error()))
		_ = p.buf.Flush()
		return
	}
	_, _ = p.buf.Write(body)
	_, _ = p.buf.WriteString("\n")
	_ = p.buf.Flush()
}

func (p *jsonPrinter) InstanceStarting(project, name string) {
	p.writeRecord(map[string]any{
		"event":    "instance_starting",
		"project":  project,
		"instance": name,
	})
}

func (p *jsonPrinter) NodeRunTerminal(project, name, nodeID, outcome, reason string) {
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
	p.writeRecord(rec)
}

func (p *jsonPrinter) InstanceTerminal(project, name, outcome string, frames int) {
	p.writeRecord(map[string]any{
		"event":    "instance_terminal",
		"project":  project,
		"instance": name,
		"outcome":  outcome,
		"frames":   frames,
	})
}

func (p *jsonPrinter) FrameTick(project, name string, frameNo int) {
	p.writeRecord(map[string]any{
		"event":    "frame_tick",
		"project":  project,
		"instance": name,
		"frame":    frameNo,
	})
}

func (p *jsonPrinter) Finalize() {
	p.mu.Lock()
	defer p.mu.Unlock()
	_ = p.buf.Flush()
}
