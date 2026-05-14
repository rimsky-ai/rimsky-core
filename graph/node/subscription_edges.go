// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// Subscription-edge inverse map. Computed at template registration
// by the validator (template_validator.go::validateSubscribes plus the
// substitution-ref auto-subscribe inference). Cached on the registry
// per template_hash for cascade-walk lookup at runtime.
//
// `Reason` is a passive observation filter (used by the diagnostics
// endpoint + cascade-fire matching at observation time); not an
// invalidate gate (the pessimistic-invalidate rule inserts wait-set
// rows regardless of filter compatibility — see spec Piece 1).
//
//	@concept: subscription
//	@concept: wait-set
package node

import (
	"regexp"
	"strings"

	"github.com/fallguy/rimsky/foundation/spec"
)

// SubscriptionEdge is one entry in the inverse map: from a sender's
// node-type, the list of (receiver_node_type, topic_kind, topic_filter)
// that the cascade walk should match against the sender's transition.
type SubscriptionEdge struct {
	ReceiverNodeType  string
	TopicKind         string
	SubscriptionScope string // "direct" | "instance"
	Filter            SubscriptionFilter
	Frame             string // "in" | "next"
}

// SubscriptionFilter captures the optional filter dimensions of a
// SubscriptionEntry that the cascade walk evaluates against a sender's
// transition.
type SubscriptionFilter struct {
	When       string // "" | node-state
	Outcome    string // "" | last_outcome
	ErrorClass string // "" | error_class
	Reason     string // "" | snake_case ParkReason
	Name       string // "" | attribute key OR event name
}

// SubscriptionEdgeMap is keyed by sender node-type. The empty key ""
// holds cross-cutting (instance: true) subscriptions.
type SubscriptionEdgeMap map[string][]SubscriptionEdge

// BuildSubscriptionEdges walks every node's Subscribes block plus the
// substitution refs parsed from its attribute schema, and produces the
// inverse map. Called by the template validator at registration.
//
// The substitution refs are passed in as the validator's already-parsed
// map (receiver-node-type → []substitutionRef). Explicit entries are
// unioned with implicit entries; duplicate (sender, topic_kind, filter,
// frame) tuples are de-duplicated.
func BuildSubscriptionEdges(
	tmpl spec.TemplateSpec,
	substitutionRefs map[string][]substitutionRef,
) SubscriptionEdgeMap {
	out := SubscriptionEdgeMap{}
	for _, n := range tmpl.Nodes {
		receiverType := n.Type
		// Explicit subscriptions.
		for _, s := range n.Subscribes {
			edge := edgeFromSubscription(s, receiverType)
			senderKey := s.Node // empty for cross-cutting
			out[senderKey] = appendUniqueEdge(out[senderKey], edge)
		}
		// Implicit subscriptions from substitution refs.
		for _, ref := range substitutionRefs[receiverType] {
			edge := edgeFromSubstitutionRef(ref, receiverType)
			out[ref.SenderNodeType] = appendUniqueEdge(out[ref.SenderNodeType], edge)
		}
	}
	return out
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

// UpstreamNodeTypesFromAttributes returns the distinct sender
// node-types referenced by `{{nodes.<X>.attribute.<Y>}}` /
// `{{nodes.<X>.event.<Y>}}` directives in the receiver's attribute
// schema. Excludes self-references. Exported for instance-creation
// callers that need the receiver's upstream set without exposing the
// full substitution-ref type.
//
//	@concept: subscription
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

// parseSubstitutionRefsFromAttributes scans the per-node Attributes
// schema for `source:` strings, extracts each `{{...}}` directive, and
// returns the receiver-facing substitution refs that auto-subscribe.
func parseSubstitutionRefsFromAttributes(n TemplateNodeDef) []substitutionRef {
	if n.Attributes == nil || len(n.Attributes.Schema) == 0 {
		return nil
	}
	var out []substitutionRef
	seen := map[substitutionRef]struct{}{}
	walkSchemaForSources(n.Attributes.Schema, func(src string) {
		for _, m := range substitutionDirectiveRe.FindAllStringSubmatch(src, -1) {
			body := strings.TrimSpace(m[1])
			if body == "" {
				continue
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
	})
	return out
}

// parseSubstitutionDirective parses one directive body (the text between
// `{{...}}`) and returns a substitutionRef when the form is
// `nodes.<X>.attribute.<Y>...` or `nodes.<X>.event.<Y>...`. Returns
// ok=false for any other shape (claim/params/legacy/etc.).
//
// The floor for both topic kinds is `len(parts) >= 4` — i.e. the body
// must name the sender, the topic kind, AND the attribute/event name.
// Matches the validator's checkAttributeSource floor in
// `graph/node/template_validator.go::checkAttributeSource`; a partial
// directive like `nodes.X.attribute` (no field) is rejected by both
// surfaces so the inverse-edge map never carries a zero-Name auto-
// subscription that won't match at the cascade walk.
func parseSubstitutionDirective(body string) (substitutionRef, bool) {
	parts := strings.Split(body, ".")
	if len(parts) < 4 || parts[0] != "nodes" {
		return substitutionRef{}, false
	}
	sender := parts[1]
	if sender == "" {
		return substitutionRef{}, false
	}
	switch parts[2] {
	case "attribute":
		return substitutionRef{
			SenderNodeType: sender, TopicKind: "attribute", Name: parts[3],
		}, true
	case "event":
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

func edgeFromSubscription(s spec.SubscriptionEntry, receiverType string) SubscriptionEdge {
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
	return SubscriptionEdge{
		ReceiverNodeType:  receiverType,
		TopicKind:         s.On,
		SubscriptionScope: scope,
		Filter: SubscriptionFilter{
			When: s.When, Outcome: s.Outcome,
			ErrorClass: s.ErrorClass, Reason: s.Reason,
			Name: s.Name,
		},
		Frame: frame,
	}
}

func edgeFromSubstitutionRef(ref substitutionRef, receiverType string) SubscriptionEdge {
	return SubscriptionEdge{
		ReceiverNodeType:  receiverType,
		TopicKind:         ref.TopicKind,
		SubscriptionScope: spec.SubscriptionScopeDirect,
		Filter:            SubscriptionFilter{Name: ref.Name},
		Frame:             "in",
	}
}

func appendUniqueEdge(edges []SubscriptionEdge, e SubscriptionEdge) []SubscriptionEdge {
	for _, existing := range edges {
		if existing == e {
			return edges
		}
	}
	return append(edges, e)
}
