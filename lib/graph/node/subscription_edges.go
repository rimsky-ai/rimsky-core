// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

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

type SubscriptionEdge struct {
	ReceiverNodeType string
	TypePattern       signal.TypePath
	WhenExpr          *signal.CompiledPredicate
	SubscriptionScope string

	//	@concept: cascade
	//	@concept: node-subscription
	WakeOnChange bool

	//	@concept: cascade
	//	@concept: node-subscription
	ForceUpstreamRefresh bool

	//	@concept: node-subscription
	//	@concept: cascade
	SenderBoundToEmpty bool
}

type SubscriptionEdgeMap struct {
	bySender map[string]*prefixNode
}

type prefixNode struct {
	exact    []SubscriptionEdge
	wildcard []SubscriptionEdge
	children map[string]*prefixNode
}

func NewSubscriptionEdgeMap() *SubscriptionEdgeMap {
	return &SubscriptionEdgeMap{bySender: map[string]*prefixNode{}}
}

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

//	@decision: empty-sender-key-edge-disambiguation
type senderBoundFilter int

const (
	edgeFilterAll senderBoundFilter = iota
	edgeFilterCrossCuttingOnly
)

func (m *SubscriptionEdgeMap) Match(senderNodeType string, signalType signal.TypePath) []SubscriptionEdge {
	if m == nil || len(m.bySender) == 0 {
		return nil
	}
	var out []SubscriptionEdge
	if senderNodeType == "" {
		out = appendMatches(out, m.bySender[""], signalType, edgeFilterAll)
		return out
	}
	out = appendMatches(out, m.bySender[senderNodeType], signalType, edgeFilterAll)
	out = appendMatches(out, m.bySender[""], signalType, edgeFilterCrossCuttingOnly)
	return out
}

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

func appendMatches(out []SubscriptionEdge, root *prefixNode, typ signal.TypePath, filter senderBoundFilter) []SubscriptionEdge {
	if root == nil {
		return out
	}
	out = appendFiltered(out, root.wildcard, filter)
	segs := splitSegments(string(typ))
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

func splitSegments(s string) []string {
	s = strings.TrimSuffix(s, "/")
	if s == "" {
		return nil
	}
	return strings.Split(s, "/")
}

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
		_ = substitutionRefs
		for _, ref := range messageRefs[receiverType] {
			edge := edgeFromMessageRef(receiverType)
			out.Insert(ref.MessageType, edge)
		}
	}
	subgraphInternal := subgraphInternalNodeTypes(tmpl)
	for _, def := range tmpl.Nodes {
		if subgraphInternal[def.Type] {
			continue
		}
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

type substitutionRef struct {
	SenderNodeType string
	TopicKind         string
	Name              string
	RefLiteral        string
	AttributeProperty string
}

// @concept: message-schema
// @concept: node-subscription
type messageRef struct {
	MessageType string
	Field       string
}

var substitutionDirectiveRe = regexp.MustCompile(`\{\{([^}]*)\}\}`)

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

// @concept: message-schema
type MessageRefSpec struct {
	MessageType string
	Field       string
}

// @concept: message-schema
func MessageRefsFromAttributes(n TemplateNodeDef) []MessageRefSpec {
	refs := parseMessageRefsFromAttributes(n)
	out := make([]MessageRefSpec, 0, len(refs))
	for _, r := range refs {
		out = append(out, MessageRefSpec(r))
	}
	return out
}

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

//	@concept: node-subscription
type SubstitutionRefSpec struct {
	SenderNodeType string
	TopicKind      string
	Name           string
}

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

//	@concept: node-subscription
//	@decision: substitution-ref-coverage-required
func parseSubstitutionRefsFromAttributes(n TemplateNodeDef) []substitutionRef {
	var out []substitutionRef
	type dedupKey struct {
		sender   string
		kind     string
		name     string
		property string
	}
	seen := map[dedupKey]struct{}{}
	scanSrc := func(src, propertyPath string) {
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
	for i, s := range n.Stores {
		scanSrcAttributeOnly(s.Selector, fmt.Sprintf("stores[%d].selector", i))
	}
	for i, l := range n.Locks {
		scanSrcAttributeOnly(l.Name, fmt.Sprintf("locks[%d].name", i))
	}
	return out
}

func parseSubstitutionDirective(body string) (substitutionRef, bool) {
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
