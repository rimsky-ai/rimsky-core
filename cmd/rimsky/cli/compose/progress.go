// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// progress.go — per-instance / per-node progress printer for
// `rimsky compose run`. Three human-prose flavors (default / quiet /
// verbose) plus a JSON-Lines wrapper that emits one structured object
// per event. The verb constructs exactly one printer per run via
// newProgressPrinter(flags.quiet, flags.verbose, flags.json) and hands
// it to the terminal-wait loop.
//
// Every emit is line-flushed: a bufio.Writer wraps the underlying
// io.Writer and Flush() is called after each line so a transcript-
// capturing demo (STORY-live-progress) sees lifecycle lines as they
// happen, not in a single end-of-run batch.
//
// @decision: progress-default, progress-flags
// @story: live-progress
package compose

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"sync"
)

// ProgressPrinter is the surface the terminal-wait loop calls into.
// Implementations route events to their own flavor of output; the
// loop calls the same methods regardless of which printer is active.
//
// @agent-contract guarantees: every call writes whole lines (newline-
// terminated) to the printer's underlying writer and flushes before
// returning, so a concurrent reader of the writer never sees a torn
// half-line and never has to wait for a buffer fill. Does NOT
// serialize calls from multiple goroutines on its own — callers that
// fan out per-instance polls must protect access externally; the
// terminal-wait loop in this package serializes by design (single
// goroutine polling sequentially). Finalize flushes the printer's
// internal buffer; it accepts no destination because every flavor
// owns its writer at construction.
type ProgressPrinter interface {
	InstanceStarting(project, name string)
	NodeRunTerminal(project, name, nodeID, outcome, reason string)
	InstanceTerminal(project, name, outcome string, frames int)
	FrameTick(project, name string, frameNo int)
	Finalize()
}

// newProgressPrinter resolves the flag combination to a concrete
// printer. Flag mutual-exclusion is enforced upstream in
// parseComposeRunFlags (--quiet and --verbose may not both be set);
// json is independent and wraps whichever prose printer the operator
// selected.
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

// linePrinter is the shared base for the three prose printers. It
// owns a bufio.Writer wrapping the operator-supplied writer, flushes
// after every line, and carries the default prose-emit methods. The
// default printer reuses linePrinter's prose methods unchanged; the
// verbose printer reuses them and adds FrameTick; the quiet printer
// overrides each method as a no-op.
type linePrinter struct {
	mu  sync.Mutex
	buf *bufio.Writer
}

func newLinePrinter(w io.Writer) *linePrinter {
	return &linePrinter{buf: bufio.NewWriter(w)}
}

// emit writes one line and flushes immediately so a streaming reader
// observes the line before the next event lands. Returns any flush
// error so the caller can surface it; the prose printers ignore the
// error (the progress channel is best-effort and a downstream pipe
// closing should not abort the run).
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

// InstanceStarting emits the standard prose form. Reused verbatim by
// defaultPrinter and verbosePrinter; quietPrinter overrides as a
// no-op. @blessed-invariant: progress-prose-single-source — the
// prose shape lives here once so a future tweak (timestamp prefix,
// localization) cannot drift between the default and verbose flavors.
// @deliberate: prose is "tracking", not "starting" — the instance was
// already created by ApplyPlan before the terminal-wait roster line
// fires; what this event actually marks is the wait loop beginning to
// track the instance, not the in-process executor invocation. The
// "starting" wording was time-misleading because it landed AFTER
// apply's own "create ok" line.
func (lp *linePrinter) InstanceStarting(project, name string) {
	_ = lp.emit(fmt.Sprintf("instance %s/%s: tracking", project, name))
}

// NodeRunTerminal emits the per-node terminal line; reason is appended
// in parentheses when non-empty.
func (lp *linePrinter) NodeRunTerminal(project, name, nodeID, outcome, reason string) {
	if reason == "" {
		_ = lp.emit(fmt.Sprintf("instance %s/%s node %s: %s", project, name, nodeID, outcome))
		return
	}
	_ = lp.emit(fmt.Sprintf("instance %s/%s node %s: %s (%s)", project, name, nodeID, outcome, reason))
}

// InstanceTerminal emits the per-instance terminal line with the
// settled frame count.
func (lp *linePrinter) InstanceTerminal(project, name, outcome string, frames int) {
	_ = lp.emit(fmt.Sprintf("instance %s/%s: %s (frames=%d)", project, name, outcome, frames))
}

// FrameTick is the verbose-only event surface; default and quiet
// printers ride the linePrinter no-op below, while verbosePrinter
// overrides with its own emit.
func (lp *linePrinter) FrameTick(project, name string, frameNo int) {}

// Finalize flushes the bufio.Writer so any partial write lands before
// the verb exits. The error is intentionally swallowed — the printer
// is best-effort and the caller has already reported the run's
// outcome by the time Finalize fires.
func (lp *linePrinter) Finalize() {
	lp.mu.Lock()
	defer lp.mu.Unlock()
	_ = lp.buf.Flush()
}

func endsWithNewline(s string) bool {
	return len(s) > 0 && s[len(s)-1] == '\n'
}

// defaultPrinter emits per-instance starts and per-node + per-instance
// terminals. No frame ticks. All event methods ride the linePrinter
// base; the wrapper exists for type-discrimination in the flag
// dispatch.
type defaultPrinter struct{ *linePrinter }

func newDefaultPrinter(w io.Writer) *defaultPrinter {
	return &defaultPrinter{linePrinter: newLinePrinter(w)}
}

// quietPrinter suppresses per-event output. Only Finalize writes —
// the aggregate summary the verb constructs once every instance has
// reached terminal.
type quietPrinter struct{ *linePrinter }

func newQuietPrinter(w io.Writer) *quietPrinter {
	return &quietPrinter{linePrinter: newLinePrinter(w)}
}

func (p *quietPrinter) InstanceStarting(project, name string)                         {}
func (p *quietPrinter) NodeRunTerminal(project, name, nodeID, outcome, reason string) {}
func (p *quietPrinter) InstanceTerminal(project, name, outcome string, frames int)    {}
func (p *quietPrinter) FrameTick(project, name string, frameNo int)                   {}

// verbosePrinter extends the default with per-frame tick lines. Used
// when --verbose is set; the spec's STORY-live-progress mentions
// frame ticks as the dial that surfaces inter-frame progress on a
// slow run. Inherits InstanceStarting / NodeRunTerminal /
// InstanceTerminal / Finalize from linePrinter and overrides only
// FrameTick.
type verbosePrinter struct{ *linePrinter }

func newVerbosePrinter(w io.Writer) *verbosePrinter {
	return &verbosePrinter{linePrinter: newLinePrinter(w)}
}

func (p *verbosePrinter) FrameTick(project, name string, frameNo int) {
	_ = p.emit(fmt.Sprintf("instance %s/%s frame %d", project, name, frameNo))
}

// jsonPrinter emits one JSON object per event, line-flushed. The
// "event" field discriminates between record kinds so a consumer
// (CI gate, log-shipping pipeline) can filter without parsing the
// surrounding prose. Independent of the prose printers — it does
// not wrap one of them, because mixing JSON Lines with prose on
// the same stream would defeat the line-discipline guarantee.
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
		// @constraint: a marshal error on a fixed-shape map[string]any
		// is the program's bug, not the operator's — emit a fallback
		// line so the absence of an event in a JSON-consuming pipeline
		// is still visible.
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
