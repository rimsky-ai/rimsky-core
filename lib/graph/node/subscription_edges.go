// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// @concept: node-subscription
// @concept: signal
// @concept: cascade
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
	TypePattern      signal.TypePath
	WhenExpr         *signal.CompiledPredicate

	//	@concept: cascade
	//	@concept: node-subscription
	ForceUpstreamRefresh bool
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

func (m *SubscriptionEdgeMap) Match(senderNodeType string, signalType signal.TypePath) []SubscriptionEdge {
	if m == nil || len(m.bySender) == 0 {
		return nil
	}
	return appendMatches(nil, m.bySender[senderNodeType], signalType)
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
	walkAllEdges(m.bySender[senderNodeType], func(e SubscriptionEdge) {
		seen[e.ReceiverNodeType] = struct{}{}
	})
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
	walkAllEdges(m.bySender[senderNodeType], func(e SubscriptionEdge) {
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

func appendMatches(out []SubscriptionEdge, root *prefixNode, typ signal.TypePath) []SubscriptionEdge {
	if root == nil {
		return out
	}
	out = append(out, root.wildcard...)
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
			existing.ForceUpstreamRefresh == e.ForceUpstreamRefresh {
			return true
		}
	}
	return false
}

// @concept: node-subscription
// @concept: signal
// @concept: message-schema
// @concept: cascade
// @decision: structural-root-edge-injection-at-registration
// @story: empty-message-wakes-roots
func BuildSubscriptionEdges(
	tmpl spec.TemplateSpec,
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
			ForceUpstreamRefresh: false,
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
// @decision: substitution-ref-coverage-required
type substitutionRef struct {
	Prefix            string
	TypeName          string
	FieldPath         string
	TopicKind         string
	RefLiteral        string
	AttributeProperty string
}

// @concept: message-schema
type messageRef = substitutionRef

var substitutionDirectiveRe = regexp.MustCompile(`\{\{([^}]*)\}\}`)

func ExtractSubstitutionRefsFromTemplate(tmpl spec.TemplateSpec) map[string][]substitutionRef {
	out := map[string][]substitutionRef{}
	for _, n := range tmpl.Nodes {
		var refs []substitutionRef
		for _, r := range parseSubstitutionRefsFromAttributes(n) {
			if r.Prefix != "nodes" {
				continue
			}
			refs = append(refs, r)
		}
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
		var refs []substitutionRef
		for _, r := range parseSubstitutionRefsFromAttributes(n) {
			if r.Prefix != "messages" {
				continue
			}
			refs = append(refs, r)
		}
		if len(refs) > 0 {
			out[n.Type] = refs
		}
	}
	return out
}

// @concept: node-subscription
type SubstitutionRefSpec struct {
	SenderNodeType string
	TopicKind      string
	Name           string
}

// @concept: node-subscription
func SubstitutionRefsFromAttributes(n TemplateNodeDef) []SubstitutionRefSpec {
	refs := parseSubstitutionRefsFromAttributes(n)
	out := make([]SubstitutionRefSpec, 0, len(refs))
	for _, r := range refs {
		if r.Prefix != "nodes" {
			continue
		}
		out = append(out, SubstitutionRefSpec{
			SenderNodeType: r.TypeName,
			TopicKind:      r.TopicKind,
			Name:           r.FieldPath,
		})
	}
	return out
}

// @concept: node-subscription
func UpstreamNodeTypesFromAttributes(n TemplateNodeDef) []string {
	seen := make(map[string]struct{})
	for _, ref := range parseSubstitutionRefsFromAttributes(n) {
		if ref.Prefix != "nodes" {
			continue
		}
		if ref.TypeName == "" || ref.TypeName == n.Type {
			continue
		}
		seen[ref.TypeName] = struct{}{}
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

// @concept: message-schema
// @concept: node-subscription
// @decision: substitution-ref-coverage-required
func parseSubstitutionRefsFromAttributes(n TemplateNodeDef) []substitutionRef {
	var out []substitutionRef
	type dedupKey struct {
		prefix   string
		typeName string
		field    string
		property string
	}
	seen := map[dedupKey]struct{}{}
	scan := func(src, propertyPath string) {
		for _, m := range substitutionDirectiveRe.FindAllStringSubmatch(src, -1) {
			literal := m[0]
			body := strings.TrimSpace(m[1])
			if body == "" {
				continue
			}
			if idx := strings.Index(body, "|"); idx >= 0 {
				body = strings.TrimSpace(body[:idx])
			}
			body = strings.TrimSpace(strings.TrimSuffix(body, "?"))
			ref, ok := parseSubstitutionDirective(body)
			if !ok {
				continue
			}
			if ref.Prefix == "nodes" && (ref.TypeName == "" || ref.TypeName == n.Type) {
				continue
			}
			key := dedupKey{ref.Prefix, ref.TypeName, ref.FieldPath, propertyPath}
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
		walkSchemaForSourcesWithPath(n.Attributes.Schema, "", scan)
	}
	for i, s := range n.ClaimProducers {
		scan(s.Selector, fmt.Sprintf("claim_producers[%d].selector", i))
	}
	for i, l := range n.Locks {
		scan(l.Name, fmt.Sprintf("locks[%d].name", i))
	}
	if n.FanOut != nil && n.FanOut.PartitionRequest != "" {
		scan(n.FanOut.PartitionRequest, "fan_out.partition_request")
	}
	return out
}

// @concept: message-schema
// @concept: node-subscription
func parseSubstitutionDirective(body string) (substitutionRef, bool) {
	body = strings.TrimSpace(body)
	if idx := strings.Index(body, "|"); idx >= 0 {
		body = strings.TrimSpace(body[:idx])
	}
	body = strings.TrimSpace(strings.TrimSuffix(body, "?"))
	parts := strings.Split(body, ".")
	if len(parts) < 2 {
		return substitutionRef{}, false
	}
	switch parts[0] {
	case "nodes":
		if len(parts) < 3 {
			return substitutionRef{}, false
		}
		typeName := parts[1]
		if typeName == "" {
			return substitutionRef{}, false
		}
		if parts[2] != "attribute" {
			return substitutionRef{}, false
		}
		field := ""
		if len(parts) >= 4 {
			field = parts[3]
		}
		return substitutionRef{
			Prefix:    "nodes",
			TypeName:  typeName,
			FieldPath: field,
			TopicKind: "attribute",
		}, true
	case "messages":
		typeName := parts[1]
		if typeName == "" {
			return substitutionRef{}, false
		}
		field := ""
		if len(parts) >= 3 {
			field = parts[2]
		}
		return substitutionRef{
			Prefix:    "messages",
			TypeName:  typeName,
			FieldPath: field,
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
	pattern := signal.TypePath(s.Type)
	when, err := signal.CompileWhen(pattern, s.When)
	if err != nil {
		return SubscriptionEdge{}, fmt.Errorf("compile when: %w", err)
	}
	if s.ForceUpstreamRefresh == nil {
		return SubscriptionEdge{}, fmt.Errorf("subscription on %q to %q missing force_upstream_refresh", receiverType, s.Type)
	}
	return SubscriptionEdge{
		ReceiverNodeType:     receiverType,
		TypePattern:          pattern,
		WhenExpr:             when,
		ForceUpstreamRefresh: *s.ForceUpstreamRefresh,
	}, nil
}
