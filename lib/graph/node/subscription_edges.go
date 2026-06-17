// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// Subscription-edge inverse map. Computed at template registration by
// the validator (template_validator.go::validateSubscribes) from the
// template's explicit `subscribes:` block. Cached on the registry per
// template_hash for cascade-walk lookup at runtime.
//
// An edge carries:
//
//   - TypePattern: a canonical signal.TypePath (exact or trailing-`*`
//     prefix per concept:signal).
//   - WhenExpr: an optional compiled CEL predicate the cascade walker
//     evaluates against the emitted signal's payload at walk time.
//   - WakeOnChange: whether a matching emission stale-marks the
//     receiver (when false, the wait-set row is still inserted but
//     the receiver is not dispatched from this edge).
//   - ForceUpstreamRefresh: whether invalidating the receiver drags
//     the sender into the same frame.
//
// The per-sender map is a small radix structure that supports trailing-
// `*` prefix matches against an emitted signal's type-path. Cross-
// cutting (`instance: true`) edges live under the empty sender-key.
//
//	@concept: node-subscription
//	@concept: signal
//	@concept: cascade
package node

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/signal"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/spec"
)

// SubscriptionEdge is one entry in the inverse map: a (receiver,
// signal type-pattern, when-predicate) coupling that the cascade walk
// matches against an emitted signal.
//
// The pre-message-schema-layer per-subscription `frame: in | next`
// modifier retired alongside the SubscriptionEntry's `Frame:` field —
// the cascade walker has one path now (in-tx, in-frame). Cross-frame
// coupling is expressed by message-emitter nodes.
type SubscriptionEdge struct {
	ReceiverNodeType string
	// @constraint: TypePattern is either an exact path or a trailing-`*`
	// prefix; WhenExpr is nil when no `when:` predicate is declared;
	// SubscriptionScope is "direct" | "instance".
	TypePattern       signal.TypePath
	WhenExpr          *signal.CompiledPredicate
	SubscriptionScope string

	// WakeOnChange is unwrapped from spec.SubscriptionEntry.WakeOnChange
	// at edge construction. When true, the cascade walker stale-marks
	// the receiver on a matching emission; when false the wait-set row
	// is still inserted but the receiver is not stale-marked.
	//
	//	@concept: cascade
	//	@concept: node-subscription
	WakeOnChange bool

	// ForceUpstreamRefresh is unwrapped from
	// spec.SubscriptionEntry.ForceUpstreamRefresh at edge construction.
	// When true, invalidating the receiver also invalidates the sender
	// so the sender re-runs in the same frame before the receiver
	// dispatches.
	//
	//	@concept: cascade
	//	@concept: node-subscription
	ForceUpstreamRefresh bool

	// SenderBoundToEmpty distinguishes runtime-injected structural-root
	// edges (true; fire only when the actual settling sender's type is
	// "") from cross-cutting `instance: true` subscription edges (false;
	// fire on every settled sender) — both live under the empty
	// sender-key. Author-declared subscriptions cannot set this flag;
	// the runtime owns it.
	//
	//	@concept: node-subscription
	//	@concept: cascade
	SenderBoundToEmpty bool
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

// senderBoundFilter selects which edges under the `""` sender-key are
// surfaced from a Match-style lookup against that bucket. Structural-
// root edges (SenderBoundToEmpty=true) and cross-cutting edges
// (SenderBoundToEmpty=false) coexist there but have different firing
// semantics: cross-cutting edges fire on every settled sender;
// structural-root edges fire only when the actual settling sender's
// type is `""` (the implicit empty-message virtual).
//
//	@decision: empty-sender-key-edge-disambiguation
type senderBoundFilter int

// @constraint: edgeFilterAll surfaces every edge in the bucket — used
// when the caller's senderNodeType is `""` so both kinds legitimately
// fire (cross-cutting matches every sender; structural-root matches
// the empty sender specifically). edgeFilterCrossCuttingOnly excludes
// SenderBoundToEmpty=true edges — used when the caller's
// senderNodeType is non-empty so only cross-cutting edges should fire
// from the `""` bucket.
const (
	edgeFilterAll senderBoundFilter = iota
	edgeFilterCrossCuttingOnly
)

// Match returns every edge whose (senderNodeType, type-pattern) tuple
// accepts the emitted signal. The result combines:
//
//   - per-sender edges keyed under `senderNodeType`: exact-pattern
//     edges that match the full type-path plus trailing-`*` edges
//     whose stripped prefix is a leading segment of the type-path;
//   - cross-cutting edges keyed under "" (empty sender key): same
//     matching rule, with structural-root edges
//     (SenderBoundToEmpty=true) suppressed when the caller's
//     senderNodeType is non-empty per
//     decision:empty-sender-key-edge-disambiguation.
//
// Returns nil when no edges match. Order is implementation-defined.
func (m *SubscriptionEdgeMap) Match(senderNodeType string, signalType signal.TypePath) []SubscriptionEdge {
	if m == nil || len(m.bySender) == 0 {
		return nil
	}
	var out []SubscriptionEdge
	if senderNodeType == "" {
		// @deliberate: actual sender is the empty-message virtual. The
		// `""` bucket holds both structural-root edges (which exist for
		// exactly this case) and cross-cutting edges (which fire for
		// every sender including this one). Both should fire here.
		out = appendMatches(out, m.bySender[""], signalType, edgeFilterAll)
		return out
	}
	out = appendMatches(out, m.bySender[senderNodeType], signalType, edgeFilterAll)
	// @deliberate: cross-cutting lookup under the `""` bucket must
	// suppress structural-root edges — those are bound to the empty
	// sender's settlement specifically and would mis-fire on a regular
	// node-type settlement.
	out = appendMatches(out, m.bySender[""], signalType, edgeFilterCrossCuttingOnly)
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
//
// When walking the `""`-sender bucket, structural-root edges
// (SenderBoundToEmpty=true) are suppressed iff senderNodeType is
// non-empty per decision:empty-sender-key-edge-disambiguation — a
// receiver bound to the empty sender's settlement does not become a
// downstream of a regular node-type settlement.
func (m *SubscriptionEdgeMap) ReceiverNodeTypesForSender(senderNodeType string) []string {
	if m == nil || len(m.bySender) == 0 {
		return nil
	}
	seen := map[string]struct{}{}
	collect := func(root *prefixNode, filter senderBoundFilter) {
		if root == nil {
			return
		}
		walkAllEdges(root, func(e SubscriptionEdge) {
			if filter == edgeFilterCrossCuttingOnly && e.SenderBoundToEmpty {
				return
			}
			seen[e.ReceiverNodeType] = struct{}{}
		})
	}
	if senderNodeType == "" {
		// @deliberate: actual sender is the empty-message virtual; both
		// kinds of edges in the `""` bucket name legitimate downstream
		// receivers here.
		collect(m.bySender[""], edgeFilterAll)
	} else {
		collect(m.bySender[senderNodeType], edgeFilterAll)
		collect(m.bySender[""], edgeFilterCrossCuttingOnly)
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

// SenderNodeTypesForReceiver returns the distinct named sender-node-
// type keys carrying at least one edge whose ReceiverNodeType equals
// receiverNodeType. The cross-cutting bucket (empty sender key,
// `instance: true` subscriptions) is deliberately EXCLUDED: it names no
// upstream node-type, and treating it as "subscribes to every node in
// the instance" would let two instance-wide subscribers gate each other
// into a standstill. Used by the supervisor's upstream-gating
// eligibility condition to derive the candidate's subscribed-sender
// set from the template.
//
// Remaining (deliberate) over-approximation: the sender set ignores
// each edge's TypePattern / `when:` predicate — a sender whose only
// matching edges fire on signal types the in-flight run can never emit
// still gates the receiver. Conservative in the gate's direction
// (never under-gates), but it widens the starvation/serialization
// surface; the candidate-selection cursor and the pending-cycle
// tie-breaker bound the damage.
func (m *SubscriptionEdgeMap) SenderNodeTypesForReceiver(receiverNodeType string) []string {
	if m == nil || len(m.bySender) == 0 {
		return nil
	}
	var out []string
	for sender, root := range m.bySender {
		if sender == "" {
			continue
		}
		found := false
		walkAllEdges(root, func(e SubscriptionEdge) {
			if e.ReceiverNodeType == receiverNodeType {
				found = true
			}
		})
		if found {
			out = append(out, sender)
		}
	}
	return out
}

// ReceiverEdgesForSender returns every edge keyed under
// `senderNodeType` plus every cross-cutting edge (under the empty
// sender-key) — without applying type-pattern matching. Used by the
// cascade walker's pessimistic seed at BFS depth ≥ 1, where the
// downstream signal isn't in hand yet but the receiver still has to
// be gated for safety.
//
// When walking the `""`-sender bucket, structural-root edges
// (SenderBoundToEmpty=true) are suppressed iff senderNodeType is
// non-empty per decision:empty-sender-key-edge-disambiguation.
func (m *SubscriptionEdgeMap) ReceiverEdgesForSender(senderNodeType string) []SubscriptionEdge {
	if m == nil || len(m.bySender) == 0 {
		return nil
	}
	var out []SubscriptionEdge
	if senderNodeType == "" {
		walkAllEdges(m.bySender[""], func(e SubscriptionEdge) {
			out = append(out, e)
		})
		return out
	}
	walkAllEdges(m.bySender[senderNodeType], func(e SubscriptionEdge) {
		out = append(out, e)
	})
	walkAllEdges(m.bySender[""], func(e SubscriptionEdge) {
		if e.SenderBoundToEmpty {
			return
		}
		out = append(out, e)
	})
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

// CrossCuttingEdges returns the cross-cutting (`instance: true`)
// edges registered under the empty sender key. Runtime-injected
// structural-root edges (SenderBoundToEmpty=true) also live in the
// `""` bucket but are NOT cross-cutting and are excluded here per
// decision:empty-sender-key-edge-disambiguation.
func (m *SubscriptionEdgeMap) CrossCuttingEdges() []SubscriptionEdge {
	if m == nil {
		return nil
	}
	var out []SubscriptionEdge
	walkAllEdges(m.bySender[""], func(e SubscriptionEdge) {
		if e.SenderBoundToEmpty {
			return
		}
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
// and (b) the exact bucket at the terminal node. The `filter`
// parameter governs which SenderBoundToEmpty values are admitted —
// see senderBoundFilter for the disambiguation rules used when
// walking the `""`-sender bucket per
// decision:empty-sender-key-edge-disambiguation. When walking a
// non-empty sender bucket, callers pass edgeFilterAll because
// SenderBoundToEmpty is structurally always false outside the `""`
// bucket.
//
// Positional `*` segments are tolerated at match time even though
// the operator-facing validator constrains explicit subscriptions to
// trailing-only `*`. At each step the walker follows both the literal
// segment child AND the `*` child, branching into a small breadth-
// first set. The defensive support keeps the matcher behavior stable
// for any future internal usage and for runtime-synthesized patterns
// that may opt into positional shapes.
func appendMatches(out []SubscriptionEdge, root *prefixNode, typ signal.TypePath, filter senderBoundFilter) []SubscriptionEdge {
	if root == nil {
		return out
	}
	// @deliberate: A prefix-bucket at the root matches any signal under
	// any top-level kind — that's `type: *` semantics. Surface it for
	// completeness; in practice templates don't write `type: *` but the
	// trie supports it.
	out = appendFiltered(out, root.wildcard, filter)
	segs := splitSegments(string(typ))
	// @deliberate: frontier holds the set of trie nodes the walker is
	// currently at. After consuming each segment, step every frontier
	// node along (a) its literal-segment child and (b) its `*` child
	// (positional wildcard). Bounded by trie depth × fan-out, so growth
	// stays small for the canonical taxonomy.
	frontier := []*prefixNode{root}
	for _, seg := range segs {
		var next []*prefixNode
		for _, cur := range frontier {
			if cur.children == nil {
				continue
			}
			if child, ok := cur.children[seg]; ok {
				next = append(next, child)
				out = appendFiltered(out, child.wildcard, filter)
			}
			if seg != "*" {
				if child, ok := cur.children["*"]; ok {
					next = append(next, child)
					out = appendFiltered(out, child.wildcard, filter)
				}
			}
		}
		if len(next) == 0 {
			return out
		}
		frontier = next
	}
	for _, cur := range frontier {
		out = appendFiltered(out, cur.exact, filter)
	}
	return out
}

// appendFiltered appends the entries of `src` that pass the filter
// onto `dst` and returns the extended slice. Centralizes the
// SenderBoundToEmpty filter check so every collection point in
// appendMatches is uniform.
func appendFiltered(dst, src []SubscriptionEdge, filter senderBoundFilter) []SubscriptionEdge {
	if filter == edgeFilterAll {
		return append(dst, src...)
	}
	for _, e := range src {
		if e.SenderBoundToEmpty {
			continue
		}
		dst = append(dst, e)
	}
	return dst
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
//
// Full edge equality includes the cascade-shape flags WakeOnChange and
// ForceUpstreamRefresh: two entries that match on (receiver, type, when,
// scope) but differ in either flag are NOT content-equal and must land
// as two distinct edges so the cascade walker honors both flag values.
// The matching-pair-with-conflicting-flags case is caught at the
// validator (validateSubscribes) and rejected at registration — see
// decision:cascade-flags-required-no-defaults. By the time the edge
// builder runs, only flag-coherent entries remain; exact-duplicate
// entries (same flags) collapse here, distinct-flag entries land as
// distinct edges.
func containsEdge(edges []SubscriptionEdge, e SubscriptionEdge) bool {
	for _, existing := range edges {
		if existing.ReceiverNodeType == e.ReceiverNodeType &&
			existing.TypePattern == e.TypePattern &&
			existing.WhenExpr == e.WhenExpr &&
			existing.SubscriptionScope == e.SubscriptionScope &&
			existing.WakeOnChange == e.WakeOnChange &&
			existing.ForceUpstreamRefresh == e.ForceUpstreamRefresh &&
			existing.SenderBoundToEmpty == e.SenderBoundToEmpty {
			return true
		}
	}
	return false
}

// BuildSubscriptionEdges walks every node's explicit Subscribes block
// and produces the inverse map. Called by the template validator at
// registration.
//
// CEL when: expressions are compiled here; compile failures propagate
// as registration-time errors so a malformed predicate cannot reach
// the cascade walker.
//
// `messageRefs` carries the per-receiver `{{messages.<type>.<field>}}`
// directives parsed from the same substitution sites: each one becomes
// an implicit `(node: <type>, type: terminal/success)` subscription
// against the message-virtual-node key. Parallel to substitutionRefs
// but routed against the message-type space rather than the node-type
// space — uniform with the explicit-subscription leg in
// `validateSubscribes` (which also admits message-type-shaped
// `node:` values). Per the spec §Auto-subscribe rule extension.
//
// After the explicit-block population, the builder augments the map
// with runtime-injected structural-root edges: one per top-level
// template node whose `subscribes:` block names no upstream node and
// whose attribute schema carries no upstream substitution refs. Each
// structural-root edge keys under sender="" with
// SenderBoundToEmpty=true, type-pattern matching `terminal/success`,
// and `WakeOnChange: true` / `ForceUpstreamRefresh: false`. The
// augmentation is template-determinable (the same root-detection rule
// is used by instance-create), so it lives on the runtime's derived
// in-memory map; the canonical template hash is over the spec bytes
// only and is unaffected. The cascade walker's `""`-sender-key lookup
// disambiguates structural-root edges from cross-cutting
// (`instance: true`) edges by the SenderBoundToEmpty flag.
//
//	@concept: node-subscription
//	@concept: signal
//	@concept: message-schema
//	@concept: cascade
//	@decision: structural-root-edge-injection-at-registration
//	@story: empty-message-wakes-roots
func BuildSubscriptionEdges(
	tmpl spec.TemplateSpec,
	substitutionRefs map[string][]substitutionRef,
	messageRefs map[string][]messageRef,
) (*SubscriptionEdgeMap, error) {
	out := NewSubscriptionEdgeMap()
	for _, n := range tmpl.Nodes {
		receiverType := n.Type
		for i, s := range n.Subscribes {
			edge, err := edgeFromSubscription(s, receiverType)
			if err != nil {
				return nil, fmt.Errorf("BuildSubscriptionEdges: node %q subscribes[%d]: %w",
					receiverType, i, err)
			}
			out.Insert(s.Node, edge)
		}
		// @deliberate: per decision:subscription-edges-only-from-explicit-block,
		// substitution refs do NOT contribute to the subscription-edge
		// map; the map is fed by the explicit `subscribes:` block only.
		// The substitutionRefs parameter is retained on the signature for
		// the validator's coverage check (registration rejects templates
		// with uncovered refs) but ignored here.
		_ = substitutionRefs
		// @deliberate: implicit subscriptions from
		// `{{messages.<type>.<field>}}` refs. Each ref auto-subscribes
		// the receiver to the message-virtual-node `<type>`'s
		// `terminal/success` — the signal a delivered message of that
		// type emits into the frame. Parallels the `subscribes:` leg's
		// `(node: <message-type>, type: terminal/success)` shape and
		// carries the same cascade defaults as substitution-ref auto-
		// edges.
		for _, ref := range messageRefs[receiverType] {
			edge := edgeFromMessageRef(receiverType)
			out.Insert(ref.MessageType, edge)
		}
	}
	// @deliberate: structural-root augmentation pass per
	// decision:structural-root-edge-injection-at-registration and
	// story:empty-message-wakes-roots. After canonicalization,
	// `tmpl.Nodes` is the flattened list of every node from every
	// graph; sub-graph internal nodes (everything in a non-main graph)
	// are excluded here — they only dispatch when their calling node
	// invokes the sub-graph, not on the implicit empty-message wake.
	// A node is a structural root iff it lives in the main graph AND
	// has no `subscribes:` entry naming an upstream `Node != ""`
	// (and != self) AND no upstream node refs from its attribute
	// substitution. Each root receives one edge keyed under sender=""
	// with SenderBoundToEmpty=true so the cascade walker fires it
	// only when the actual settling sender's type is "" (the implicit
	// empty-message virtual).
	subgraphInternal := subgraphInternalNodeTypes(tmpl)
	for _, def := range tmpl.Nodes {
		if subgraphInternal[def.Type] {
			continue
		}
		// @deliberate: per story:empty-message-wakes-roots, a structural
		// root is a node "whose author-declared `subscribes:` block is
		// empty or absent." ANY subscribes entry disqualifies the node
		// — a cross-cutting (`instance: true`) entry counts as an
		// author-declared subscription, so a monitor/cleanup node that
		// observes every settle is NOT a structural root. The
		// classification is intentional: cross-cutting-only nodes own
		// the "observe every event" surface and should not also wake
		// at instance-create-equivalent empty-message wakes. A
		// self-subscription (`node == def.Type`) does not establish an
		// upstream and is excluded from the disqualification.
		// @decision: structural-root-edge-injection-at-registration
		hasUpstream := false
		for _, s := range def.Subscribes {
			if s.Node == def.Type {
				continue
			}
			hasUpstream = true
			break
		}
		if !hasUpstream {
			for _, ref := range UpstreamNodeTypesFromAttributes(def) {
				if ref != def.Type {
					hasUpstream = true
					break
				}
			}
		}
		// @deliberate: a `{{messages.<type>.<field>}}` ref auto-
		// subscribes the receiver to the message-virtual-node's
		// terminal/success (see the messageRefs leg above), which IS
		// an upstream edge. A node that has no `subscribes:` block but
		// reads a typed-message field is therefore not a structural
		// root — the typed message wakes it, not the empty-message
		// virtual. Mirror the substitution-ref check above.
		// @decision: structural-root-edge-injection-at-registration
		// @story: empty-message-wakes-roots
		if !hasUpstream && len(messageRefs[def.Type]) > 0 {
			hasUpstream = true
		}
		if hasUpstream {
			continue
		}
		out.Insert("", SubscriptionEdge{
			ReceiverNodeType:     def.Type,
			TypePattern:          signal.TypePath("terminal/success"),
			WhenExpr:             nil,
			SubscriptionScope:    spec.SubscriptionScopeDirect,
			WakeOnChange:         true,
			ForceUpstreamRefresh: false,
			SenderBoundToEmpty:   true,
		})
	}
	return out, nil
}

// subgraphInternalNodeTypes returns the set of node types that are
// declared inside a non-main GraphSpec (i.e. sub-graph internal
// nodes). Templates that declare no `graphs:` (legacy flat-Nodes form)
// or only the `main` graph return an empty set. Used by the
// structural-root augmentation pass to exclude sub-graph internal
// nodes — those nodes dispatch only when their calling node invokes
// the sub-graph, never on the implicit empty-message wake.
//
// @concept: node-subscription
// @decision: structural-root-edge-injection-at-registration
func subgraphInternalNodeTypes(tmpl spec.TemplateSpec) map[string]bool {
	out := make(map[string]bool, 8)
	if len(tmpl.Graphs) == 0 {
		return out
	}
	for _, g := range tmpl.Graphs {
		if g.Name == spec.MainGraphName {
			continue
		}
		for _, n := range g.Nodes {
			out[n.Type] = true
		}
	}
	return out
}

// edgeFromMessageRef synthesizes the implicit subscription edge for a
// `{{messages.<type>.<field>}}` directive: the receiver auto-subscribes
// to the message-virtual-node's `terminal/success`. Symmetric with
// `edgeFromSubscription` for an explicit `{ node: <type>, type:
// terminal/success }` subscribes entry — no `when:`, direct scope.
//
// @concept: message-schema
// @concept: node-subscription
func edgeFromMessageRef(receiverType string) SubscriptionEdge {
	return SubscriptionEdge{
		ReceiverNodeType:     receiverType,
		TypePattern:          signal.TypePath("terminal/success"),
		WhenExpr:             nil,
		SubscriptionScope:    spec.SubscriptionScopeDirect,
		WakeOnChange:         true,
		ForceUpstreamRefresh: false,
	}
}

// substitutionRef is one parsed `{{nodes.X.attribute.Y}}` directive in
// a node's attribute schema.
//
// RefLiteral and AttributeProperty are populated by
// parseSubstitutionRefsFromAttributes so the registration-time coverage
// check (`validateSubstitutionRefCoverage`) can build self-contained
// `substitution_ref_uncovered` entries — the operator sees the exact
// directive text and the schema property path the directive appeared
// in. Per decision:uncovered-substitution-error-shape.
type substitutionRef struct {
	SenderNodeType string
	// @constraint: TopicKind is "attribute"; Name is the attribute key
	// ("" indicates a whole-attribute pull);
	// RefLiteral is the exact "{{nodes.X.attribute.Y}}" text as it
	// appears in the schema; AttributeProperty is the schema property
	// path the ref appeared in (e.g. "properties.foo.source"). RefLiteral
	// and AttributeProperty are populated by
	// parseSubstitutionRefsFromAttributes so the registration-time
	// coverage check (validateSubstitutionRefCoverage) can build
	// self-contained substitution_ref_uncovered entries.
	TopicKind         string
	Name              string
	RefLiteral        string
	AttributeProperty string
}

// messageRef is one parsed `{{messages.<type>.<field>}}` directive in a
// node's attribute schema (or other substitution site). The receiver
// implicitly subscribes to the message-virtual-node `<type>`'s
// `terminal/success`; the substitution engine resolves `<field>` against
// the triggering message body when the frame's triggering type matches.
//
// Distinct from substitutionRef so the BuildSubscriptionEdges loop can
// route the implicit auto-subscription against the message-virtual-node
// key rather than the regular node-type key, and so the validator can
// cross-check `<type>` against the template's `messages:` registry
// rather than against the declared node set.
//
// @concept: message-schema
// @concept: node-subscription
type messageRef struct {
	// @constraint: MessageType is the declared `messages:` entry type
	// the directive names; Field is the body field at the top of the
	// path (empty for the whole-body bare form).
	MessageType string
	Field       string
}

// substitutionDirectiveRe is the same shape as
// graph/attribute/substitution.go::directivePattern. Duplicated here so
// the validator does not import the attribute package (cyclic).
var substitutionDirectiveRe = regexp.MustCompile(`\{\{([^}]*)\}\}`)

// ExtractSubstitutionRefsFromTemplate scans every node's Attributes
// schema recursively, walks every `source:` string in the JSON-Schema
// tree, parses the {{...}} directives in each source string, and
// returns a map keyed by receiver node-type. Skips `claim.*` and
// `params.*` (those are intrinsically per-frame or instance-scoped and
// do not participate in the subscription edge surface) and skips refs
// that name the receiver itself.
//
// Exported so the validator's substitution-ref coverage check can call
// it; the algorithm is also available per-node via
// parseSubstitutionRefsFromAttributes.
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

// ExtractMessageRefsFromTemplate scans every node's Attributes schema
// (and acquisition-time substitution sites: stores selectors, lock
// names) for `{{messages.<type>.<field>}}` directives and returns a map
// keyed by receiver node-type. Bare-form `{{messages.<type>}}` is
// admitted with an empty Field.
//
// Used by the template validator to drive two things:
//
//  1. The auto-subscribe rule extension: each ref produces an implicit
//     `(node: <type>, type: terminal/success)` subscription edge against
//     the message-virtual-node, indexed by BuildSubscriptionEdges.
//  2. The registration-time cross-check: `<type>` must be declared in
//     `messages:`, and `<field>` (if non-empty) must be a property in
//     that entry's body_schema.
//
// Exported alongside ExtractSubstitutionRefsFromTemplate; the two
// extractors share `walkSchemaForSourcesWithPath` and parse the same
// source strings with different per-directive parsers.
//
// @concept: message-schema
// @concept: node-subscription
func ExtractMessageRefsFromTemplate(tmpl spec.TemplateSpec) map[string][]messageRef {
	out := map[string][]messageRef{}
	for _, n := range tmpl.Nodes {
		refs := parseMessageRefsFromAttributes(n)
		if len(refs) > 0 {
			out[n.Type] = refs
		}
	}
	return out
}

// MessageRefSpec is the exported per-directive message-ref shape:
// message-type + field-name (top-of-path; empty for whole-body bare
// form). Symmetric with SubstitutionRefSpec; consumers outside the
// graph package consume the exported form.
//
// @concept: message-schema
type MessageRefSpec struct {
	MessageType string
	Field       string
}

// MessageRefsFromAttributes returns the per-directive message refs
// parsed from the receiver's attribute schema and acquisition-time
// substitution sites. Symmetric with SubstitutionRefsFromAttributes.
//
// @concept: message-schema
func MessageRefsFromAttributes(n TemplateNodeDef) []MessageRefSpec {
	refs := parseMessageRefsFromAttributes(n)
	out := make([]MessageRefSpec, 0, len(refs))
	for _, r := range refs {
		out = append(out, MessageRefSpec(r))
	}
	return out
}

// parseMessageRefsFromAttributes scans every site on the node where a
// `{{messages.<type>.<field>}}` directive can appear — the same set
// parseSubstitutionRefsFromAttributes scans for the `nodes.*` family
// (attribute-schema `source:` strings, stores selectors, lock names) —
// and returns the parsed refs in declaration order, deduplicated.
//
// One substitution engine, two surfaces: the validator's cross-check
// path (this extractor) and the runtime resolver (the `messages` arm in
// resolveDirectiveValueRaw) must agree on which directives produce
// auto-subscriptions and which fields a receiver can read.
func parseMessageRefsFromAttributes(n TemplateNodeDef) []messageRef {
	var out []messageRef
	seen := map[messageRef]struct{}{}
	scanSrc := func(src string) {
		for _, m := range substitutionDirectiveRe.FindAllStringSubmatch(src, -1) {
			body := strings.TrimSpace(m[1])
			if body == "" {
				continue
			}
			if idx := strings.Index(body, "|"); idx >= 0 {
				body = strings.TrimSpace(body[:idx])
			}
			body = strings.TrimSpace(strings.TrimSuffix(body, "?"))
			ref, ok := parseMessageDirective(body)
			if !ok {
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
		// @deliberate: the schema-path arg is irrelevant for the
		// message-ref extractor — message refs feed the auto-subscribe
		// surface, not the schema-location-aware coverage check —
		// so wrap the path-threading walker and discard the path.
		walkSchemaForSourcesWithPath(n.Attributes.Schema, "", func(src, _ string) {
			scanSrc(src)
		})
	}
	for _, s := range n.Stores {
		scanSrc(s.Selector)
	}
	for _, l := range n.Locks {
		scanSrc(l.Name)
	}
	return out
}

// parseMessageDirective parses one directive body (the text between
// `{{...}}`) and returns a messageRef when the form is
// `messages.<type>(.<field>?…)`. Returns ok=false for any other shape.
//
// The directive grammar treats the second dot-separated segment as the
// message-type and the third (if present) as the top-of-path field.
// Bare form `messages.<type>` (no field path) produces Field="" for the
// whole-body pull, mirroring the runtime resolver's bare-form behaviour.
// Mirrors the validator's checkAttributeDirectiveBody "messages" arm so
// every directive accepted at registration also produces an inverse-edge
// entry.
func parseMessageDirective(body string) (messageRef, bool) {
	parts := strings.Split(body, ".")
	if len(parts) < 2 || parts[0] != "messages" {
		return messageRef{}, false
	}
	mtype := parts[1]
	if mtype == "" {
		return messageRef{}, false
	}
	field := ""
	if len(parts) >= 3 {
		field = parts[2]
	}
	return messageRef{MessageType: mtype, Field: field}, true
}

// SubstitutionRefSpec is the exported per-directive substitution-ref
// shape: sender node-type + topic kind (`attribute`) + the attribute
// key the directive read. Returned by `SubstitutionRefsFromAttributes`
// for callers outside the graph package (e.g. the lineage writer in
// `runtime/lineage_writer.go`) that need the full directive shape, not
// just the upstream sender set.
//
//	@concept: node-subscription
type SubstitutionRefSpec struct {
	// @constraint: SenderNodeType is the upstream node-type the directive
	// named; TopicKind is "attribute" (the `event` kind is retired per
	// TD-remove-event-substitution-path); Name is the attribute key.
	SenderNodeType string
	TopicKind      string
	Name           string
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
		out = append(out, SubstitutionRefSpec{
			SenderNodeType: r.SenderNodeType,
			TopicKind:      r.TopicKind,
			Name:           r.Name,
		})
	}
	return out
}

// UpstreamNodeTypesFromAttributes returns the distinct sender
// node-types referenced by `{{nodes.<X>.attribute.<Y>}}` directives in
// the receiver's attribute schema. Excludes self-references. Exported
// for instance-creation callers that need the receiver's upstream set
// without exposing the full substitution-ref type.
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
// attribute-schema `source:` strings, store selectors, and lock names.
// Each ref discovered here is enumerated for the registration-time
// coverage check; the receiver MUST declare a matching `subscribes:`
// entry whose sender and type would deliver the implied signal, or
// registration is rejected.
//
//	@concept: node-subscription
//	@decision: substitution-ref-coverage-required
func parseSubstitutionRefsFromAttributes(n TemplateNodeDef) []substitutionRef {
	var out []substitutionRef
	// @deliberate: dedup key is the logical-identity tuple
	// (sender, kind, name, property) — not the full struct, since
	// RefLiteral is a per-occurrence value that must not influence
	// dedup. Two text occurrences of the same logical ref at the same
	// schema property collapse; the first wins.
	type dedupKey struct {
		sender   string
		kind     string
		name     string
		property string
	}
	seen := map[dedupKey]struct{}{}
	// @deliberate: scanSrc accepts any directive shape
	// parseSubstitutionDirective admits (attribute only).
	// `propertyPath` is the schema-path string the source belongs to
	// (e.g. "properties.foo.source"); used to populate
	// substitutionRef.AttributeProperty.
	scanSrc := func(src, propertyPath string) {
		for _, m := range substitutionDirectiveRe.FindAllStringSubmatch(src, -1) {
			literal := m[0]
			body := strings.TrimSpace(m[1])
			if body == "" {
				continue
			}
			// @deliberate: Strip an optional fallback `| <literal>` suffix so the
			// so the upstream-ref parser sees only the directive proper.
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
			key := dedupKey{ref.SenderNodeType, ref.TopicKind, ref.Name, propertyPath}
			if _, dup := seen[key]; dup {
				continue
			}
			seen[key] = struct{}{}
			ref.RefLiteral = literal
			ref.AttributeProperty = propertyPath
			out = append(out, ref)
		}
	}
	// @deliberate: scanSrcAttributeOnly mirrors scanSrc but discards
	// any directive whose TopicKind is not "attribute". Store-selector
	// and lock-name reads enumerate only attribute refs.
	scanSrcAttributeOnly := func(src, propertyPath string) {
		for _, m := range substitutionDirectiveRe.FindAllStringSubmatch(src, -1) {
			literal := m[0]
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
			key := dedupKey{ref.SenderNodeType, ref.TopicKind, ref.Name, propertyPath}
			if _, dup := seen[key]; dup {
				continue
			}
			seen[key] = struct{}{}
			ref.RefLiteral = literal
			ref.AttributeProperty = propertyPath
			out = append(out, ref)
		}
	}
	if n.Attributes != nil && len(n.Attributes.Schema) > 0 {
		walkSchemaForSourcesWithPath(n.Attributes.Schema, "", scanSrc)
	}
	// @deliberate: Stores selectors and lock names are also
	// acquisition-time substitution sites; attribute reads there must
	// each be matched by an explicit `subscribes:` entry so the
	// dispatch-time substitution context contains the referenced
	// upstream attribute rows. Event reads at these sites are out of
	// scope for the coverage check.
	for i, s := range n.Stores {
		scanSrcAttributeOnly(s.Selector, fmt.Sprintf("stores[%d].selector", i))
	}
	for i, l := range n.Locks {
		scanSrcAttributeOnly(l.Name, fmt.Sprintf("locks[%d].name", i))
	}
	return out
}

// parseSubstitutionDirective parses one directive body (the text between
// `{{...}}`) and returns a substitutionRef when the form is
// `nodes.<X>.attribute(.<Y>?…)`. Returns ok=false for any other shape
// (claim/params/legacy/etc.).
//
// Bare-form pulls (`nodes.<X>.attribute` with no trailing field path)
// per spec §Item 3 "Empty trailing path" produce a substitutionRef with
// Name="" (whole-attribute pull; the coverage check requires a covering
// subscription on the wildcard `attribute/*` shape).
//
// Matches the validator's checkAttributeSource grammar in
// `graph/node/template_validator.go::checkAttributeSource` so every
// directive accepted at registration is enumerated for the
// coverage check.
func parseSubstitutionDirective(body string) (substitutionRef, bool) {
	// @deliberate: Strip the optional fallback `| <literal>` and the optional `?`
	// optional `?` lenient marker so the source-kind dispatch below sees
	// a clean directive body. The validator already rejected `?` + `|`
	// combined.
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
		// @deliberate: bare form nodes.<X>.attribute → Name=""
		// (whole-attribute pull); field-path form
		// nodes.<X>.attribute.<field>... → Name=<field>.
		name := ""
		if len(parts) >= 4 {
			name = parts[3]
		}
		return substitutionRef{
			SenderNodeType: sender, TopicKind: "attribute", Name: name,
		}, true
	default:
		return substitutionRef{}, false
	}
}

// walkSchemaForSourcesWithPath recurses through a JSON-Schema map
// looking for any `"source"` string value at any depth. cb receives
// both the source string and the dot-joined path of map keys that lead
// to it (e.g. "properties.foo.source",
// "properties.outer.properties.inner.source"). Array indices are
// appended as "[i]" segments. The path threading lets the substitution-
// ref coverage check record the schema location of each parsed ref.
func walkSchemaForSourcesWithPath(node any, path string, cb func(src, path string)) {
	switch v := node.(type) {
	case map[string]any:
		for k, child := range v {
			childPath := k
			if path != "" {
				childPath = path + "." + k
			}
			if k == "source" {
				if s, ok := child.(string); ok {
					cb(s, childPath)
				}
				continue
			}
			walkSchemaForSourcesWithPath(child, childPath, cb)
		}
	case []any:
		for i, child := range v {
			childPath := fmt.Sprintf("%s[%d]", path, i)
			walkSchemaForSourcesWithPath(child, childPath, cb)
		}
	}
}

// edgeFromSubscription converts a parsed spec.SubscriptionEntry into a
// SubscriptionEdge. Returns an error if the entry's CEL when:
// expression fails to compile (the validator catches this earlier;
// surfaced here defensively for callers that bypass the validator) or
// if either of the required cascade-shape flags is absent.
func edgeFromSubscription(s spec.SubscriptionEntry, receiverType string) (SubscriptionEdge, error) {
	scope := spec.SubscriptionScopeDirect
	if s.Instance {
		scope = spec.SubscriptionScopeInstance
	}
	pattern := signal.TypePath(s.Type)
	when, err := signal.CompileWhen(pattern, s.When)
	if err != nil {
		return SubscriptionEdge{}, fmt.Errorf("compile when: %w", err)
	}
	// @constraint: the cascade-shape flags are required (no defaults).
	// Pass 2's validator rejects nil values first, but the edge-builder
	// refuses to silently coerce nil to false — a missing flag is a
	// precondition failure, not a benign default.
	if s.WakeOnChange == nil {
		return SubscriptionEdge{}, fmt.Errorf("subscription on %q to %q missing wake_on_change", receiverType, s.Type)
	}
	if s.ForceUpstreamRefresh == nil {
		return SubscriptionEdge{}, fmt.Errorf("subscription on %q to %q missing force_upstream_refresh", receiverType, s.Type)
	}
	return SubscriptionEdge{
		ReceiverNodeType:     receiverType,
		TypePattern:          pattern,
		WhenExpr:             when,
		SubscriptionScope:    scope,
		WakeOnChange:         *s.WakeOnChange,
		ForceUpstreamRefresh: *s.ForceUpstreamRefresh,
	}, nil
}
