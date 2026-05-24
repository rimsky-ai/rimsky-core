// Copyright © 2026 Fall Guy Consulting.
// Licensed under the Apache License, Version 2.0. See LICENSE.apache at the
// repo root, or http://www.apache.org/licenses/LICENSE-2.0.

// Subscription-edge inverse map. Computed at template registration by
// the validator (template_validator.go::validateSubscribes plus the
// substitution-ref auto-subscribe inference). Cached on the registry
// per template_hash for cascade-walk lookup at runtime.
//
// Under the 2026-05-23 signal-taxonomy reshape, an edge carries:
//
//   - TypePattern: a canonical signal.TypePath (exact or trailing-`*`
//     prefix per concept:signal).
//   - WhenExpr: an optional compiled CEL predicate the cascade walker
//     evaluates against the emitted signal's payload at walk time.
//
// The per-sender map is a small radix structure that supports trailing-
// `*` prefix matches against an emitted signal's type-path. Cross-
// cutting (`instance: true`) edges live under the empty sender-key.
//
//	@concept: node-subscription
//	@concept: signal
package node

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/fallguy/rimsky/foundation/signal"
	"github.com/fallguy/rimsky/foundation/spec"
)

// SubscriptionEdge is one entry in the inverse map: a (receiver,
// signal type-pattern, when-predicate) coupling that the cascade walk
// matches against an emitted signal.
type SubscriptionEdge struct {
	ReceiverNodeType  string
	TypePattern       signal.TypePath           // exact or trailing-`*` prefix
	WhenExpr          *signal.CompiledPredicate // nil if no when:
	SubscriptionScope string                    // "direct" | "instance"
	Frame             string                    // "in" | "next"
}

// SubscriptionEdgeMap is the inverse-edge structure: per-sender prefix
// tries that resolve emitted signals to matching subscription edges.
type SubscriptionEdgeMap struct {
	bySender map[string]*prefixNode
}

// prefixNode is one segment of the per-sender prefix trie. `exact`
// edges fire when the walker terminates at this node; `wildcard`
// edges (trailing-`*` patterns ending at this depth) fire for any
// path that reaches this depth or deeper.
type prefixNode struct {
	exact    []SubscriptionEdge
	wildcard []SubscriptionEdge
	children map[string]*prefixNode
}

// NewSubscriptionEdgeMap returns an empty edge map. The map's zero
// value is also a usable empty map but allocates lazily.
func NewSubscriptionEdgeMap() *SubscriptionEdgeMap {
	return &SubscriptionEdgeMap{bySender: map[string]*prefixNode{}}
}

// Insert records `edge` under the given sender-node-type key. The
// edge's TypePattern selects the trie path. Trailing-`*` patterns
// attach to the `wildcard` bucket at the prefix's final segment;
// exact patterns attach to the `exact` bucket at their final segment.
//
// Insert is idempotent for content-equal edges (CompiledPredicate
// identity is the only distinguishing field that escapes deep
// equality; in practice all instances of the same (receiver, type,
// when) tuple compile to the same predicate at registration so a
// re-insert is a benign duplicate).
func (m *SubscriptionEdgeMap) Insert(senderNodeType string, edge SubscriptionEdge) {
	if m.bySender == nil {
		m.bySender = map[string]*prefixNode{}
	}
	root, ok := m.bySender[senderNodeType]
	if !ok {
		root = &prefixNode{}
		m.bySender[senderNodeType] = root
	}
	pattern := string(edge.TypePattern)
	isWildcard := strings.HasSuffix(pattern, "*")
	if isWildcard {
		pattern = strings.TrimSuffix(pattern, "*")
		pattern = strings.TrimSuffix(pattern, "/")
	}
	segs := splitSegments(pattern)
	node := root
	for _, seg := range segs {
		if node.children == nil {
			node.children = map[string]*prefixNode{}
		}
		next, ok := node.children[seg]
		if !ok {
			next = &prefixNode{}
			node.children[seg] = next
		}
		node = next
	}
	if isWildcard {
		if !containsEdge(node.wildcard, edge) {
			node.wildcard = append(node.wildcard, edge)
		}
		return
	}
	if !containsEdge(node.exact, edge) {
		node.exact = append(node.exact, edge)
	}
}

// Match returns every edge whose (senderNodeType, type-pattern) tuple
// accepts the emitted signal. The result combines:
//
//   - per-sender edges keyed under `senderNodeType`: exact-pattern
//     edges that match the full type-path plus trailing-`*` edges
//     whose stripped prefix is a leading segment of the type-path;
//   - cross-cutting edges keyed under "" (empty sender key): same
//     matching rule.
//
// Returns nil when no edges match. Order is implementation-defined.
func (m *SubscriptionEdgeMap) Match(senderNodeType string, signalType signal.TypePath) []SubscriptionEdge {
	if m == nil || len(m.bySender) == 0 {
		return nil
	}
	var out []SubscriptionEdge
	out = appendMatches(out, m.bySender[senderNodeType], signalType)
	if senderNodeType != "" {
		out = appendMatches(out, m.bySender[""], signalType)
	}
	return out
}

// Senders returns the set of sender-node-type keys with at least one
// edge. Used by post-commit fanout helpers that iterate receivers per
// sender rather than matching one signal at a time.
func (m *SubscriptionEdgeMap) Senders() []string {
	if m == nil {
		return nil
	}
	out := make([]string, 0, len(m.bySender))
	for k := range m.bySender {
		out = append(out, k)
	}
	return out
}

// ReceiverNodeTypesForSender returns the distinct ReceiverNodeType set
// across every edge stored under senderNodeType (and, when
// senderNodeType != "", also under the empty cross-cutting key). Used
// by post-commit fanout helpers that route a recalculate event per
// subscribed receiver without evaluating the signal predicate.
func (m *SubscriptionEdgeMap) ReceiverNodeTypesForSender(senderNodeType string) []string {
	if m == nil || len(m.bySender) == 0 {
		return nil
	}
	seen := map[string]struct{}{}
	collect := func(root *prefixNode) {
		if root == nil {
			return
		}
		walkAllEdges(root, func(e SubscriptionEdge) {
			seen[e.ReceiverNodeType] = struct{}{}
		})
	}
	collect(m.bySender[senderNodeType])
	if senderNodeType != "" {
		collect(m.bySender[""])
	}
	if len(seen) == 0 {
		return nil
	}
	out := make([]string, 0, len(seen))
	for r := range seen {
		out = append(out, r)
	}
	return out
}

// ReceiverEdgesForSender returns every edge keyed under
// `senderNodeType` plus every cross-cutting edge (under the empty
// sender-key) — without applying type-pattern matching. Used by the
// cascade walker's pessimistic seed at BFS depth ≥ 1, where the
// downstream signal isn't in hand yet but the receiver still has to
// be gated for safety.
func (m *SubscriptionEdgeMap) ReceiverEdgesForSender(senderNodeType string) []SubscriptionEdge {
	if m == nil || len(m.bySender) == 0 {
		return nil
	}
	var out []SubscriptionEdge
	walkAllEdges(m.bySender[senderNodeType], func(e SubscriptionEdge) {
		out = append(out, e)
	})
	if senderNodeType != "" {
		walkAllEdges(m.bySender[""], func(e SubscriptionEdge) {
			out = append(out, e)
		})
	}
	return out
}

// AllEdges returns every edge across every sender bucket. Used by
// callers (e.g. message-delivery fan-out) that match the same edge
// set against many signals.
func (m *SubscriptionEdgeMap) AllEdges() []SubscriptionEdge {
	if m == nil {
		return nil
	}
	var out []SubscriptionEdge
	for _, root := range m.bySender {
		walkAllEdges(root, func(e SubscriptionEdge) {
			out = append(out, e)
		})
	}
	return out
}

// CrossCuttingEdges returns the edges registered under the empty
// sender key (the `instance: true` bucket).
func (m *SubscriptionEdgeMap) CrossCuttingEdges() []SubscriptionEdge {
	if m == nil {
		return nil
	}
	var out []SubscriptionEdge
	walkAllEdges(m.bySender[""], func(e SubscriptionEdge) {
		out = append(out, e)
	})
	return out
}

// walkAllEdges depth-first-walks the prefix trie, invoking cb for
// every edge in every bucket.
func walkAllEdges(n *prefixNode, cb func(SubscriptionEdge)) {
	if n == nil {
		return
	}
	for _, e := range n.exact {
		cb(e)
	}
	for _, e := range n.wildcard {
		cb(e)
	}
	for _, child := range n.children {
		walkAllEdges(child, cb)
	}
}

// appendMatches walks the prefix trie along the segments of `typ`,
// collecting (a) every wildcard bucket encountered along the path
// and (b) the exact bucket at the terminal node.
//
// Positional `*` segments (e.g. the middle segment of
// `attribute/*/changed`) are supported: at each step the walker
// follows both the literal segment child AND the `*` child,
// branching into a small breadth-first set. This lets the canonical
// `attribute/*/changed` emit-shape pattern (registered by the
// auto-subscribe inference for bare `{{nodes.X.attribute}}` pulls)
// match concrete signals like `attribute/foo/changed` without
// requiring a separate per-key registration.
func appendMatches(out []SubscriptionEdge, root *prefixNode, typ signal.TypePath) []SubscriptionEdge {
	if root == nil {
		return out
	}
	// A prefix-bucket at the root matches any signal under any
	// top-level kind — that's `type: *` semantics. We still surface
	// it for completeness; in practice templates don't write `type:
	// *` but the trie supports it.
	out = append(out, root.wildcard...)
	segs := splitSegments(string(typ))
	// frontier holds the set of trie nodes the walker is currently
	// at. After consuming each segment we step every frontier node
	// along (a) its literal-segment child and (b) its `*` child
	// (positional wildcard). Bounded by trie depth × fan-out, so the
	// growth stays small for the canonical taxonomy.
	frontier := []*prefixNode{root}
	for _, seg := range segs {
		var next []*prefixNode
		for _, cur := range frontier {
			if cur.children == nil {
				continue
			}
			if child, ok := cur.children[seg]; ok {
				next = append(next, child)
				out = append(out, child.wildcard...)
			}
			if seg != "*" {
				if child, ok := cur.children["*"]; ok {
					next = append(next, child)
					out = append(out, child.wildcard...)
				}
			}
		}
		if len(next) == 0 {
			return out
		}
		frontier = next
	}
	for _, cur := range frontier {
		out = append(out, cur.exact...)
	}
	return out
}

// splitSegments splits a slash-delimited path. Empty input returns
// nil; a trailing slash is dropped.
func splitSegments(s string) []string {
	s = strings.TrimSuffix(s, "/")
	if s == "" {
		return nil
	}
	return strings.Split(s, "/")
}

// containsEdge reports whether the slice already holds a
// content-equal edge. CompiledPredicate is identity-compared (a fresh
// compile of the same source produces a distinct *CompiledPredicate),
// so two registrations of the same (receiver, type, when) source land
// as two distinct entries — benign in practice, the cascade walker
// just evaluates the same predicate twice.
func containsEdge(edges []SubscriptionEdge, e SubscriptionEdge) bool {
	for _, existing := range edges {
		if existing.ReceiverNodeType == e.ReceiverNodeType &&
			existing.TypePattern == e.TypePattern &&
			existing.WhenExpr == e.WhenExpr &&
			existing.SubscriptionScope == e.SubscriptionScope &&
			existing.Frame == e.Frame {
			return true
		}
	}
	return false
}

// BuildSubscriptionEdges walks every node's Subscribes block plus the
// substitution refs parsed from its attribute schema, and produces the
// inverse map. Called by the template validator at registration.
//
// CEL when: expressions are compiled here; compile failures propagate
// as registration-time errors so a malformed predicate cannot reach
// the cascade walker.
//
//	@concept: node-subscription
//	@concept: signal
func BuildSubscriptionEdges(
	tmpl spec.TemplateSpec,
	substitutionRefs map[string][]substitutionRef,
) (*SubscriptionEdgeMap, error) {
	out := NewSubscriptionEdgeMap()
	for _, n := range tmpl.Nodes {
		receiverType := n.Type
		// Explicit subscriptions.
		for i, s := range n.Subscribes {
			edge, err := edgeFromSubscription(s, receiverType)
			if err != nil {
				return nil, fmt.Errorf("BuildSubscriptionEdges: node %q subscribes[%d]: %w",
					receiverType, i, err)
			}
			out.Insert(s.Node, edge)
		}
		// Implicit subscriptions from substitution refs.
		for _, ref := range substitutionRefs[receiverType] {
			edge := edgeFromSubstitutionRef(ref, receiverType)
			out.Insert(ref.SenderNodeType, edge)
		}
	}
	return out, nil
}

// substitutionRef is one parsed `{{nodes.X.attribute.Y}}` or
// `{{nodes.X.event.Z.<path>}}` directive in a node's attribute schema.
type substitutionRef struct {
	SenderNodeType string
	TopicKind      string // "attribute" | "event"
	Name           string // attribute key or event name
}

// substitutionDirectiveRe is the same shape as
// graph/attribute/substitution.go::directivePattern. Duplicated here so
// the validator does not import the attribute package (cyclic).
var substitutionDirectiveRe = regexp.MustCompile(`\{\{([^}]*)\}\}`)

// ExtractSubstitutionRefsFromTemplate scans every node's Attributes
// schema recursively, walks every `source:` string in the JSON-Schema
// tree, parses the {{...}} directives in each source string, and
// returns a map keyed by receiver node-type. Skips `claim.*` and
// `params.*` (those don't auto-subscribe) and skips refs that name the
// receiver itself.
//
// Exported so the validator can call it; the algorithm is also
// available per-node via parseSubstitutionRefsFromAttributes.
func ExtractSubstitutionRefsFromTemplate(tmpl spec.TemplateSpec) map[string][]substitutionRef {
	out := map[string][]substitutionRef{}
	for _, n := range tmpl.Nodes {
		refs := parseSubstitutionRefsFromAttributes(n)
		if len(refs) > 0 {
			out[n.Type] = refs
		}
	}
	return out
}

// SubstitutionRefSpec is the exported per-directive substitution-ref
// shape: sender node-type + topic kind (`attribute` | `event`) + the
// name (attribute key or event name) the directive read. Returned by
// `SubstitutionRefsFromAttributes` for callers outside the graph
// package (e.g. the lineage writer in `runtime/lineage_writer.go`) that
// need the full directive shape, not just the upstream sender set.
//
//	@concept: node-subscription
type SubstitutionRefSpec struct {
	SenderNodeType string // upstream node-type the directive named
	TopicKind      string // "attribute" | "event"
	Name           string // attribute key or event name
}

// SubstitutionRefsFromAttributes returns the per-directive substitution
// refs parsed from the receiver's attribute schema. Self-references are
// excluded. Distinct from `UpstreamNodeTypesFromAttributes`, which
// returns just the deduped upstream type set — this returns one entry
// per (sender, kind, name) directive so callers can populate per-ref
// lineage rows.
//
//	@concept: node-subscription
func SubstitutionRefsFromAttributes(n TemplateNodeDef) []SubstitutionRefSpec {
	refs := parseSubstitutionRefsFromAttributes(n)
	out := make([]SubstitutionRefSpec, 0, len(refs))
	for _, r := range refs {
		out = append(out, SubstitutionRefSpec(r))
	}
	return out
}

// UpstreamNodeTypesFromAttributes returns the distinct sender
// node-types referenced by `{{nodes.<X>.attribute.<Y>}}` /
// `{{nodes.<X>.event.<Y>}}` directives in the receiver's attribute
// schema. Excludes self-references. Exported for instance-creation
// callers that need the receiver's upstream set without exposing the
// full substitution-ref type.
//
//	@concept: node-subscription
func UpstreamNodeTypesFromAttributes(n TemplateNodeDef) []string {
	seen := make(map[string]struct{})
	for _, ref := range parseSubstitutionRefsFromAttributes(n) {
		if ref.SenderNodeType == "" || ref.SenderNodeType == n.Type {
			continue
		}
		seen[ref.SenderNodeType] = struct{}{}
	}
	if len(seen) == 0 {
		return nil
	}
	out := make([]string, 0, len(seen))
	for s := range seen {
		out = append(out, s)
	}
	return out
}

// parseSubstitutionRefsFromAttributes scans every site on the node
// where a `{{nodes.X.attribute.Y}}` directive can appear — the
// attribute-schema `source:` strings (auto-subscribe per spec §4.3),
// store selectors, and lock names. Each `{{nodes.X.attribute.Y}}`
// reference produces an attribute-topic auto-subscription so the
// dispatch-time substitution context (drained wait-set rows) sees the
// sender's attributes. Per the 2026-05-20 per-run attribute-keying spec,
// the substitution context is exactly "what fired this frame for this
// receiver" — so any read of an upstream attribute MUST be reflected
// as an attribute-topic subscription. Per spec
// .ok-planner/specs/2026-05-20-attribute-pull-resolution-design.md.
func parseSubstitutionRefsFromAttributes(n TemplateNodeDef) []substitutionRef {
	var out []substitutionRef
	seen := map[substitutionRef]struct{}{}
	// scanSrc accepts any directive shape parseSubstitutionDirective
	// admits (attribute or event). The attribute-schema `source:` scan
	// has used this surface since auto-subscribe shipped, so event
	// references in schemas continue to produce edges as before.
	scanSrc := func(src string) {
		for _, m := range substitutionDirectiveRe.FindAllStringSubmatch(src, -1) {
			body := strings.TrimSpace(m[1])
			if body == "" {
				continue
			}
			// Strip an optional fallback `| <literal>` suffix so the
			// upstream-ref parser sees only the directive proper.
			if idx := strings.Index(body, "|"); idx >= 0 {
				body = strings.TrimSpace(body[:idx])
			}
			ref, ok := parseSubstitutionDirective(body)
			if !ok {
				continue
			}
			if ref.SenderNodeType == "" || ref.SenderNodeType == n.Type {
				continue
			}
			if _, dup := seen[ref]; dup {
				continue
			}
			seen[ref] = struct{}{}
			out = append(out, ref)
		}
	}
	// scanSrcAttributeOnly is the same as scanSrc, but it discards any
	// directive whose TopicKind is not "attribute". The 2026-05-20
	// attribute-pull-resolution spec confines the new store-selector
	// and lock-name auto-subscribe scan to attribute reads; event reads
	// at those sites keep whatever subscription shape pre-existed the
	// 2026-05-20 plan (i.e. NONE introduced by this scan).
	scanSrcAttributeOnly := func(src string) {
		for _, m := range substitutionDirectiveRe.FindAllStringSubmatch(src, -1) {
			body := strings.TrimSpace(m[1])
			if body == "" {
				continue
			}
			if idx := strings.Index(body, "|"); idx >= 0 {
				body = strings.TrimSpace(body[:idx])
			}
			ref, ok := parseSubstitutionDirective(body)
			if !ok {
				continue
			}
			if ref.TopicKind != "attribute" {
				continue
			}
			if ref.SenderNodeType == "" || ref.SenderNodeType == n.Type {
				continue
			}
			if _, dup := seen[ref]; dup {
				continue
			}
			seen[ref] = struct{}{}
			out = append(out, ref)
		}
	}
	if n.Attributes != nil && len(n.Attributes.Schema) > 0 {
		walkSchemaForSources(n.Attributes.Schema, scanSrc)
	}
	// Stores selectors and lock names are also acquisition-time
	// substitution sites; reads there must auto-subscribe so the
	// dispatch-time substitution context contains the referenced
	// upstream attribute rows. Event reads at these sites do NOT
	// auto-subscribe — the 2026-05-20 minimalist substitution model
	// does not extend to events.
	for _, s := range n.Stores {
		scanSrcAttributeOnly(s.Selector)
	}
	for _, l := range n.Locks {
		scanSrcAttributeOnly(l.Name)
	}
	return out
}

// parseSubstitutionDirective parses one directive body (the text between
// `{{...}}`) and returns a substitutionRef when the form is
// `nodes.<X>.attribute(.<Y>?…)` or `nodes.<X>.event.<Y>(.…)?`. Returns
// ok=false for any other shape (claim/params/legacy/etc.).
//
// Bare-form pulls (`nodes.<X>.attribute` and `nodes.<X>.event.<name>`
// with no trailing field path) per spec §Item 3 "Empty trailing path"
// produce a substitutionRef with the appropriate Name:
//
//   - `nodes.<X>.attribute` → Name="" (whole-attribute pull;
//     auto-subscribes to the wildcard `attribute/*/changed` shape).
//   - `nodes.<X>.event.<name>` → Name=<name> (whole-event-payload pull;
//     same Name discipline as the field-path form).
//
// Matches the validator's checkAttributeSource grammar in
// `graph/node/template_validator.go::checkAttributeSource` so every
// directive accepted at registration also produces an inverse-edge entry.
func parseSubstitutionDirective(body string) (substitutionRef, bool) {
	// Strip the optional fallback `| <literal>` and the optional `?`
	// lenient marker so the source-kind dispatch below sees a clean
	// directive body. Per the 2026-05-21 userdata-collapse grammar
	// relaxation; the validator already rejected `?` + `|` combined.
	body = strings.TrimSpace(body)
	if idx := strings.Index(body, "|"); idx >= 0 {
		body = strings.TrimSpace(body[:idx])
	}
	body = strings.TrimSpace(strings.TrimSuffix(body, "?"))
	parts := strings.Split(body, ".")
	if len(parts) < 3 || parts[0] != "nodes" {
		return substitutionRef{}, false
	}
	sender := parts[1]
	if sender == "" {
		return substitutionRef{}, false
	}
	switch parts[2] {
	case "attribute":
		// Bare form: nodes.<X>.attribute → Name="" (whole-attribute pull).
		// Field-path form: nodes.<X>.attribute.<field>... → Name=<field>.
		name := ""
		if len(parts) >= 4 {
			name = parts[3]
		}
		return substitutionRef{
			SenderNodeType: sender, TopicKind: "attribute", Name: name,
		}, true
	case "event":
		// Event name is required (nodes.<X>.event.<name>[.<path>…]).
		if len(parts) < 4 || parts[3] == "" {
			return substitutionRef{}, false
		}
		return substitutionRef{
			SenderNodeType: sender, TopicKind: "event", Name: parts[3],
		}, true
	default:
		return substitutionRef{}, false
	}
}

// walkSchemaForSources recurses through a JSON-Schema map looking for
// any `"source"` string value at any depth. cb is invoked for each
// string source found.
func walkSchemaForSources(node any, cb func(string)) {
	switch v := node.(type) {
	case map[string]any:
		for k, child := range v {
			if k == "source" {
				if s, ok := child.(string); ok {
					cb(s)
				}
				continue
			}
			walkSchemaForSources(child, cb)
		}
	case []any:
		for _, child := range v {
			walkSchemaForSources(child, cb)
		}
	}
}

// edgeFromSubscription converts a parsed spec.SubscriptionEntry into a
// SubscriptionEdge. Returns an error if the entry's CEL when:
// expression fails to compile (the validator catches this earlier;
// surfaced here defensively for callers that bypass the validator).
func edgeFromSubscription(s spec.SubscriptionEntry, receiverType string) (SubscriptionEdge, error) {
	scope := spec.SubscriptionScopeDirect
	if s.Instance {
		scope = spec.SubscriptionScopeInstance
	}
	frame := s.Frame
	if frame == "" {
		if s.Instance {
			frame = "next"
		} else {
			frame = "in"
		}
	}
	pattern := signal.TypePath(s.Type)
	when, err := signal.CompileWhen(pattern, s.When)
	if err != nil {
		return SubscriptionEdge{}, fmt.Errorf("compile when: %w", err)
	}
	return SubscriptionEdge{
		ReceiverNodeType:  receiverType,
		TypePattern:       pattern,
		WhenExpr:          when,
		SubscriptionScope: scope,
		Frame:             frame,
	}, nil
}

// edgeFromSubstitutionRef converts an implicit substitution-ref into a
// SubscriptionEdge. Attribute refs become attribute/<name>/changed (or
// attribute/*/changed for bare-attribute pulls); event refs become
// event/<name>. Implicit edges always run with frame: in.
func edgeFromSubstitutionRef(ref substitutionRef, receiverType string) SubscriptionEdge {
	var pattern signal.TypePath
	switch ref.TopicKind {
	case "attribute":
		if ref.Name == "" {
			// Whole-attribute pull (`{{nodes.X.attribute}}`) — scope
			// to attribute/*/changed so the implicit subscription
			// only fires on attribute-delta signals, not on the broad
			// `attribute/*` umbrella.
			pattern = signal.TypePath("attribute/*/changed")
		} else {
			pattern = signal.TypePath("attribute/" + ref.Name + "/changed")
		}
	case "event":
		pattern = signal.TypePath("event/" + ref.Name)
	default:
		// Defensive: parseSubstitutionDirective only emits attribute/event.
		pattern = signal.TypePath("")
	}
	return SubscriptionEdge{
		ReceiverNodeType:  receiverType,
		TypePattern:       pattern,
		WhenExpr:          nil,
		SubscriptionScope: spec.SubscriptionScopeDirect,
		Frame:             "in",
	}
}
