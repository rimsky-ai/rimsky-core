// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: Apache-2.0

// @plumbline:allow-docstrings

// Package stubmode is the single definition of the stub-mode probe
// signature: the request attributes a conformance probe carries and the
// response shape a stub-conformant executor answers with. Every issuing
// and asserting site — the conformance runner and scenarios, and the
// stub-capable in-tree executors — imports this package; the signature
// is defined nowhere else.
//
// The signature is a convention over the ordinary request/response
// attribute bags, not a wire-protocol field. A non-Go executor is
// stub-conformant when it reproduces exactly this contract:
//
//   - A request whose attributes carry "stub_probe": true is a stub
//     probe. In stub mode the executor answers it with a terminal
//     Success whose attributes_delta carries "stub": true (any other
//     keys may accompany it). An executor not in stub mode handles the
//     request as ordinary work.
//   - A request whose attributes carry "probe_park": true asks a
//     stub-mode executor to answer with a Park outcome carrying
//     non-empty scratch, exercising the park/resume path.
//   - A request whose attributes carry "probe_cancel": true asks a
//     stub-mode executor to hold the dispatch open until cancelled,
//     exercising the cancel path.
//   - A request whose attributes carry "probe_async": true asks a
//     stub-mode executor to answer with an AwaitAsyncCallback outcome,
//     exercising the async-handoff path.
//   - A request whose attributes carry "park_resume_at" (an RFC 3339
//     timestamp string) asks a stub-mode executor to echo that instant
//     back as the Park outcome's resume_at.
//   - A request whose attributes carry "stub_tags" (an array of
//     strings) asks a stub-mode executor to echo those tags back on the
//     settling Success outcome.
package stubmode

const (
	ProbeAttribute       = "stub_probe"
	ParkProbeAttribute   = "probe_park"
	CancelProbeAttribute = "probe_cancel"
	ResponseAttribute    = "stub"
	AsyncProbeAttribute  = "probe_async"

	ParkResumeAtAttribute = "park_resume_at"
	TagsAttribute         = "stub_tags"
)

// IsAsyncProbe reports whether the request attributes carry the
// async-handoff probe.
func IsAsyncProbe(attrs map[string]any) bool {
	v, _ := attrs[AsyncProbeAttribute].(bool)
	return v
}

// IsProbe reports whether the request attributes carry the stub probe.
func IsProbe(attrs map[string]any) bool {
	v, _ := attrs[ProbeAttribute].(bool)
	return v
}

// IsParkProbe reports whether the request attributes carry the park probe.
func IsParkProbe(attrs map[string]any) bool {
	v, _ := attrs[ParkProbeAttribute].(bool)
	return v
}

// IsCancelProbe reports whether the request attributes carry the cancel probe.
func IsCancelProbe(attrs map[string]any) bool {
	v, _ := attrs[CancelProbeAttribute].(bool)
	return v
}

// ResponseDelta is the attributes_delta a stub-mode executor answers a
// stub probe with.
func ResponseDelta() map[string]any {
	return map[string]any{ResponseAttribute: true}
}

// ConfirmsStub reports whether a settled attributes_delta confirms stub
// mode.
func ConfirmsStub(delta map[string]any) bool {
	v, _ := delta[ResponseAttribute].(bool)
	return v
}
