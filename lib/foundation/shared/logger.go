// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package shared

import (
	"log/slog"
	"sync"
)

type Logger interface {
	Debug(msg string, fields ...any)
	Info(msg string, fields ...any)
	Warn(msg string, fields ...any)
	Error(msg string, fields ...any)
	With(fields ...any) Logger
}

type slogLogger struct{ l *slog.Logger }

func NewSlogLogger(l *slog.Logger) Logger { return &slogLogger{l} }

func (s *slogLogger) Debug(msg string, fields ...any) { s.l.Debug(msg, fields...) }
func (s *slogLogger) Info(msg string, fields ...any)  { s.l.Info(msg, fields...) }
func (s *slogLogger) Warn(msg string, fields ...any)  { s.l.Warn(msg, fields...) }
func (s *slogLogger) Error(msg string, fields ...any) { s.l.Error(msg, fields...) }
func (s *slogLogger) With(fields ...any) Logger {
	return &slogLogger{l: s.l.With(fields...)}
}

type SilentLogger struct{}

func (SilentLogger) Debug(string, ...any)      {}
func (SilentLogger) Info(string, ...any)       {}
func (SilentLogger) Warn(string, ...any)       {}
func (SilentLogger) Error(string, ...any)      {}
func (SilentLogger) With(fields ...any) Logger { return SilentLogger{} }

type Record struct {
	Level  string
	Msg    string
	Fields map[string]any
}

type capturingSink struct {
	mu      sync.Mutex
	records []Record
}

type CapturingLogger struct {
	sink *capturingSink
	base map[string]any
}

func NewCapturingLogger() *CapturingLogger { return &CapturingLogger{sink: &capturingSink{}} }

func (c *CapturingLogger) mergeFields(fields []any) map[string]any {
	merged := map[string]any{}
	for k, v := range c.base {
		merged[k] = v
	}
	for i := 0; i+1 < len(fields); i += 2 {
		key, ok := fields[i].(string)
		if !ok {
			continue
		}
		merged[key] = fields[i+1]
	}
	return merged
}

func (c *CapturingLogger) capture(level, msg string, fields []any) {
	rec := Record{Level: level, Msg: msg, Fields: c.mergeFields(fields)}
	c.sink.mu.Lock()
	defer c.sink.mu.Unlock()
	c.sink.records = append(c.sink.records, rec)
}

func (c *CapturingLogger) Debug(msg string, fields ...any) { c.capture("debug", msg, fields) }
func (c *CapturingLogger) Info(msg string, fields ...any)  { c.capture("info", msg, fields) }
func (c *CapturingLogger) Warn(msg string, fields ...any)  { c.capture("warn", msg, fields) }
func (c *CapturingLogger) Error(msg string, fields ...any) { c.capture("error", msg, fields) }

func (c *CapturingLogger) With(fields ...any) Logger {
	return &CapturingLogger{sink: c.sink, base: c.mergeFields(fields)}
}

func (c *CapturingLogger) Records() []Record {
	c.sink.mu.Lock()
	defer c.sink.mu.Unlock()
	out := make([]Record, len(c.sink.records))
	copy(out, c.sink.records)
	return out
}

func (c *CapturingLogger) Clear() {
	c.sink.mu.Lock()
	defer c.sink.mu.Unlock()
	c.sink.records = nil
}
