// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package node

import (
	"bytes"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/santhosh-tekuri/jsonschema/v5"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/signal"
)

type ValidationError struct {
	Path string
	Msg  string
}

type ValidationWarning struct {
	Path string
	Msg  string
}

type ValidationResult struct {
	Errors           []ValidationError
	Warnings         []ValidationWarning
	StructuredErrors []map[string]any
}

func (r ValidationResult) Ok() bool {
	return len(r.Errors) == 0 && len(r.StructuredErrors) == 0
}

var instantiationPlaceholderRe = regexp.MustCompile(`\{params\.[a-zA-Z_][a-zA-Z0-9_]*\}`)

var anyBraceRe = regexp.MustCompile(`\{[^{}]*\}`)

var dispatchDirectiveRe = regexp.MustCompile(`\{\{([^{}]+)\}\}`)

var directiveBodyRe = regexp.MustCompile(`^(claim|params|nodes|trigger|child|messages)\.(.+)$`)

//	@concept: template
type RefValidationMode int

const (
	RefValidateAll RefValidationMode = iota
	RefValidateAvailable
	RefValidateNone
)

func (m RefValidationMode) String() string {
	switch m {
	case RefValidateAll:
		return "all"
	case RefValidateAvailable:
		return "available"
	case RefValidateNone:
		return "none"
	}
	return fmt.Sprintf("RefValidationMode(%d)", int(m))
}

func refValidationModeRejection(refDesc string, mode RefValidationMode) string {
	return fmt.Sprintf(
		"%s; reference validation failed under mode %q — mode %q makes a reference that "+
			"cannot be validated at registration fatal; for register-before-provision workflows "+
			"set templates.ref_validation_mode to \"available\" (skip not-yet-provisioned "+
			"references) or \"none\" (skip registration-time reference validation entirely)",
		refDesc, mode, mode)
}

type RegistryHooks struct {
	RefValidationMode RefValidationMode

	StoreDeclared func(name string) bool
	NamedLockDeclared func(name string) bool
	ExecutorDeclared func(name string) bool

	ExecutorDeclaredTags func(name string) ([]string, bool)

	//	@concept: signal
	ExecutorDeclaredErrorClasses func(name string) ([]string, bool)

	//	@concept: signal
	//	@concept: error-policy
	StoreDeclaredErrorClasses func(name string) ([]string, bool)

	ExecutorExpectedAttributesSchema func(name string) ([]byte, bool)

	StoreAdvertisesDataProcessing func(name string) bool

	StoreAdvertisesSplitScope func(name string) bool

	// @concept: node
	KindAliases *KindAliasMap
}

func ValidateTemplate(spec *TemplateSpec, hooks RegistryHooks) ValidationResult {
	var res ValidationResult
	if spec == nil {
		res.Errors = append(res.Errors, ValidationError{Path: "", Msg: "spec is nil"})
		return res
	}
	if strings.TrimSpace(spec.Name) == "" {
		res.Errors = append(res.Errors, ValidationError{Path: "name", Msg: "name is required"})
	}
	if strings.TrimSpace(spec.Version) == "" {
		res.Errors = append(res.Errors, ValidationError{Path: "version", Msg: "version is required"})
	}
	validateFrameTimeout(spec, &res)

	canonicalizeGraphs(spec, &res)

	if len(spec.Nodes) == 0 {
		res.Errors = append(res.Errors, ValidationError{Path: "nodes", Msg: "template must declare at least one node"})
		return res
	}

	validateDefaults(spec, hooks, &res)

	declared := make(map[string]int, len(spec.Nodes))
	for i, n := range spec.Nodes {
		if strings.TrimSpace(n.Type) == "" {
			res.Errors = append(res.Errors, ValidationError{
				Path: fmt.Sprintf("nodes[%d].type", i), Msg: "type is required",
			})
			continue
		}
		if _, dup := declared[n.Type]; dup {
			res.Errors = append(res.Errors, ValidationError{
				Path: fmt.Sprintf("nodes[%d].type", i),
				Msg:  fmt.Sprintf("duplicate node type %q", n.Type),
			})
			continue
		}
		declared[n.Type] = i
	}

	declaredMessages := validateMessages(spec, declared, &res)
	messageBodyFieldsForCEL := buildMessageBodyFieldSet(spec)

	for i, n := range spec.Nodes {
		base := fmt.Sprintf("nodes[%d]", i)
		validateSubscribes(n, base, declared, declaredMessages, messageBodyFieldsForCEL, hooks, spec, &res)
		validateErrorTypes(n, base, declared, hooks, &res)
		validateExecutorCoherence(n, base, hooks, &res)
		validateExecutorDeclared(n, base, hooks, &res)
		validateKindDeclaration(n, base, hooks, &res)
		validateEmitsMessage(n, base, spec, declaredMessages, &res)
		validateStores(n, base, hooks, &res)
		validateLocks(n, base, hooks, &res)
		validateAttributesSchema(n, base, declared, spec, hooks, &res)
		validateAcquireUnavailablePolicyAdvised(n, base, &res)
		validateMaxParkDuration(n, base, &res)
		validateDispatchDeadlines(n, base, &res)
		validateHolds(n, base, spec, declared, &res)
		validateFanOut(n, base, hooks, &res)
		validateTagsAtRegistration(n, base, spec, &res)
	}

	validatePublishers(spec, declared, declaredMessages, &res)

	// @concept: message-schema
	messageRefs := ExtractMessageRefsFromTemplate(*spec)
	messageBodyFields := buildMessageBodyFieldSet(spec)
	for receiverType, list := range messageRefs {
		for _, ref := range list {
			path := fmt.Sprintf("nodes[%s].attributes.schema (substitution ref)", receiverType)
			if _, ok := declaredMessages[ref.MessageType]; !ok {
				res.Errors = append(res.Errors, ValidationError{
					Path: path,
					Msg: fmt.Sprintf("substitution ref `messages.%s.%s` references unknown message type %q (not declared in `messages:`)",
						ref.MessageType, ref.Field, ref.MessageType),
				})
				continue
			}
			if ref.Field == "" {
				continue
			}
			fields, ok := messageBodyFields[ref.MessageType]
			if !ok {
				res.Errors = append(res.Errors, ValidationError{
					Path: path,
					Msg: fmt.Sprintf("substitution ref `messages.%s.%s` reads a field but message type %q declares no body_schema (empty body)",
						ref.MessageType, ref.Field, ref.MessageType),
				})
				continue
			}
			if _, ok := fields[ref.Field]; !ok {
				res.Errors = append(res.Errors, ValidationError{
					Path: path,
					Msg: fmt.Sprintf("substitution ref `messages.%s.%s` reads a field %q not declared in message type %q's body_schema",
						ref.MessageType, ref.Field, ref.Field, ref.MessageType),
				})
			}
		}
	}

	refs := ExtractSubstitutionRefsFromTemplate(*spec)
	validateSubstitutionRefExistence(spec, declared, hooks, refs, &res)
	validateSubstitutionRefCoverage(spec, refs, &res)

	if _, err := BuildHardDepEdges(*spec); err != nil {
		res.Errors = append(res.Errors, ValidationError{
			Path: "graphs",
			Msg:  err.Error(),
		})
	}

	return res
}

func attributeKeyDeclared(sender TemplateNodeDef, key string) bool {
	if sender.Attributes == nil || len(sender.Attributes.Schema) == 0 {
		return false
	}
	props, ok := sender.Attributes.Schema["properties"].(map[string]any)
	if !ok {
		return false
	}
	_, declared := props[key]
	return declared
}

func validateFrameTimeout(spec *TemplateSpec, res *ValidationResult) {
	if spec.FrameTimeoutMs != 0 && spec.FrameTimeoutMs < FrameTimeoutMinMs {
		res.Errors = append(res.Errors, ValidationError{
			Path: "frame_timeout_ms",
			Msg: fmt.Sprintf("frame_timeout_ms = %d is below hard floor %d",
				spec.FrameTimeoutMs, FrameTimeoutMinMs),
		})
	}
}

// @concept: node
func effectiveExecutor(n TemplateNodeDef, hooks RegistryHooks) string {
	if n.Executor != "" {
		return n.Executor
	}
	if n.Kind == "" || hooks.KindAliases == nil {
		return ""
	}
	alias, _ := hooks.KindAliases.Resolve(n.Kind)
	return alias
}

// @concept: attribute
// @concept: node
func validateDefaults(spec *TemplateSpec, hooks RegistryHooks, res *ValidationResult) {
	if spec.Defaults == nil || spec.Defaults.Attributes == nil {
		return
	}
	if len(spec.Defaults.Attributes.ByExecutor) == 0 {
		return
	}
	known := make(map[string]struct{}, len(spec.Nodes))
	for _, n := range spec.Nodes {
		if ex := effectiveExecutor(n, hooks); ex != "" {
			known[ex] = struct{}{}
		}
	}
	for execName := range spec.Defaults.Attributes.ByExecutor {
		if _, ok := known[execName]; !ok {
			res.Errors = append(res.Errors, ValidationError{
				Path: fmt.Sprintf("defaults.attributes.by_executor[%q]", execName),
				Msg:  fmt.Sprintf("executor name %q does not match any node's executor", execName),
			})
		}
	}
}

func ApplyFrameResolutionDefaults(spec *TemplateSpec) {
	if spec == nil {
		return
	}
	if spec.FrameTimeoutMs == 0 {
		spec.FrameTimeoutMs = FrameTimeoutDefaultMs
	}
}

//	@concept: error-policy
//	@concept: signal
func validateErrorTypes(n TemplateNodeDef, base string, _ map[string]int, hooks RegistryHooks, res *ValidationResult) {
	validActions := map[string]bool{
		"pass":                      true,
		"give_up":                   true,
		"retry":                     true,
		"discard_claims_then_retry": true,
	}
	executorForClasses := effectiveExecutor(n, hooks)
	var executorClasses []string
	vocabularyKnown := false
	if executorForClasses != "" && hooks.ExecutorDeclaredErrorClasses != nil {
		if classes, ok := hooks.ExecutorDeclaredErrorClasses(executorForClasses); ok {
			executorClasses = classes
			vocabularyKnown = true
		}
	}
	var producerClasses []string
	if hooks.StoreDeclaredErrorClasses != nil {
		for _, storeName := range RequiredStores(n) {
			if classes, ok := hooks.StoreDeclaredErrorClasses(storeName); ok {
				producerClasses = append(producerClasses, classes...)
				vocabularyKnown = true
			}
		}
	}
	for className, policy := range n.ErrorTypes {
		for ai, action := range policy.Policy {
			if validActions[action.Action] {
				continue
			}
			res.Errors = append(res.Errors, ValidationError{
				Path: fmt.Sprintf("%s.error_types[%s].policy[%d].action", base, className, ai),
				Msg:  fmt.Sprintf("unknown action %q; valid actions are: pass | give_up | retry | discard_claims_then_retry", action.Action),
			})
		}
		if !vocabularyKnown {
			continue
		}
		if isRuntimeSynthesizedErrorClass(className) {
			continue
		}
		if errorClassMatchesDeclared(className, executorClasses) {
			continue
		}
		if errorClassMatchesDeclared(className, producerClasses) {
			continue
		}
		res.Warnings = append(res.Warnings, ValidationWarning{
			Path: fmt.Sprintf("%s.error_types[%s]", base, className),
			Msg: fmt.Sprintf("error class %q is not in any declared vocabulary — not declared by executor %q (declared: %v), "+
				"not in the acquire/* synthetic family, and not declared by any producer in this node's stores: block (declared: %v); "+
				"the policy registers but will only match if a peer emits this exact class",
				className, executorForClasses, executorClasses, producerClasses),
		})
	}
}

func isRuntimeSynthesizedErrorClass(className string) bool {
	if strings.HasPrefix(className, "acquire/") {
		return true
	}
	switch className {
	case "template_resolution_failed",
		"template_validation_failed",
		"executor_schema_unavailable",
		"attributes_schema_failed",
		"retry_loop_no_progress",
		"unresolved_executor":
		return true
	}
	return false
}

//	@concept: signal
func errorClassMatchesDeclared(class string, declared []string) bool {
	for _, d := range declared {
		if d == class {
			return true
		}
		if strings.HasSuffix(d, "/*") {
			prefix := strings.TrimSuffix(d, "*")
			if strings.HasPrefix(class, prefix) {
				return true
			}
		}
	}
	return false
}

//	@concept: error-policy
func validateAcquireUnavailablePolicyAdvised(n TemplateNodeDef, base string, res *ValidationResult) {
	if len(n.Stores) == 0 {
		return
	}
	for key := range n.ErrorTypes {
		if key == "acquire/unavailable" {
			return
		}
		if strings.HasSuffix(key, "/*") {
			prefix := strings.TrimSuffix(key, "*")
			if strings.HasPrefix("acquire/unavailable", prefix) {
				return
			}
		}
	}
	res.Warnings = append(res.Warnings, ValidationWarning{
		Path: base + ".error_types",
		Msg: "node uses claim-producers but declares no \"acquire/unavailable\" error_types entry; " +
			"the default behavior on acquisition failure is fail-fast, not implicit retry. " +
			"Declare error_types: {\"acquire/unavailable\": ...} to choose a policy (e.g. retry).",
	})
}

//	@concept: node-subscription
//	@concept: signal
//	@concept: message-schema
func validateSubscribes(n TemplateNodeDef, base string, declared map[string]int, declaredMessages map[string]struct{}, messageBodyFields map[string]map[string]struct{}, hooks RegistryHooks, tmpl *TemplateSpec, res *ValidationResult) {
	for i, s := range n.Subscribes {
		sbase := fmt.Sprintf("%s.subscribes[%d]", base, i)
		wakeKnown := s.WakeOnChange != nil
		refreshKnown := s.ForceUpstreamRefresh != nil
		if !wakeKnown {
			res.Errors = append(res.Errors, ValidationError{
				Path: sbase + ".wake_on_change",
				Msg:  "wake_on_change is required (true or false); no default applies",
			})
		}
		if !refreshKnown {
			res.Errors = append(res.Errors, ValidationError{
				Path: sbase + ".force_upstream_refresh",
				Msg:  "force_upstream_refresh is required (true or false); no default applies",
			})
		}
		if s.Node == "" && !s.Instance {
			res.Errors = append(res.Errors, ValidationError{
				Path: sbase,
				Msg:  "must declare either `node:` or `instance: true`",
			})
			continue
		}
		if s.Node != "" && s.Instance {
			res.Errors = append(res.Errors, ValidationError{
				Path: sbase,
				Msg:  "`node:` and `instance: true` are mutually exclusive",
			})
			continue
		}
		if refreshKnown && s.Instance && *s.ForceUpstreamRefresh {
			res.Errors = append(res.Errors, ValidationError{
				Path: sbase,
				Msg:  "force_upstream_refresh: true cannot be combined with instance: true (cross-cutting subscriptions are sender-agnostic; there is no specific upstream to refresh)",
			})
			continue
		}
		if s.Node != "" {
			_, isDeclaredNode := declared[s.Node]
			_, isDeclaredMessage := declaredMessages[s.Node]
			if !isDeclaredNode && !isDeclaredMessage {
				if strings.Contains(s.Node, "/") {
					res.Errors = append(res.Errors, ValidationError{
						Path: sbase + ".node",
						Msg: fmt.Sprintf("subscription `node: %q` is shaped like a message-type-path but is not declared in the template's `messages:` registry",
							s.Node),
					})
				} else {
					res.Errors = append(res.Errors, ValidationError{
						Path: sbase + ".node",
						Msg:  fmt.Sprintf("subscription `node: %q` does not reference a declared node", s.Node),
					})
				}
				continue
			}
		}
		if strings.TrimSpace(s.Type) == "" {
			res.Errors = append(res.Errors, ValidationError{
				Path: sbase + ".type",
				Msg:  "`type:` is required",
			})
			continue
		}
		if err := signal.ValidateSubscriptionType(signal.TypePath(s.Type)); err != nil {
			res.Errors = append(res.Errors, ValidationError{
				Path: sbase + ".type",
				Msg:  err.Error(),
			})
			continue
		}
		if s.When != "" {
			var compileErr error
			if _, isMessageType := declaredMessages[s.Node]; isMessageType {
				bodyFields, present := messageBodyFields[s.Node]
				if !present {
					bodyFields = map[string]struct{}{}
				}
				_, compileErr = signal.CompileWhenWithBodyFields(signal.TypePath(s.Type), s.When, bodyFields)
			} else {
				_, compileErr = signal.CompileWhen(signal.TypePath(s.Type), s.When)
			}
			if compileErr != nil {
				res.Errors = append(res.Errors, ValidationError{
					Path: sbase + ".when",
					Msg:  compileErr.Error(),
				})
			}
		}
		_, isRealNode := declared[s.Node]
		if isRealNode && tmpl != nil && strings.HasPrefix(s.Type, "terminal/error/") &&
			!strings.HasSuffix(s.Type, "*") {
			senderIdx := declared[s.Node]
			sender := tmpl.Nodes[senderIdx]
			leaf := strings.TrimPrefix(s.Type, "terminal/error/")
			if !isRuntimeSynthesizedErrorClass(leaf) {
				vocabularyKnown := false
				matched := false
				if sender.Executor != "" && hooks.ExecutorDeclaredErrorClasses != nil {
					if classes, ok := hooks.ExecutorDeclaredErrorClasses(sender.Executor); ok {
						vocabularyKnown = true
						matched = matched || errorClassMatchesDeclared(leaf, classes)
					}
				}
				if hooks.StoreDeclaredErrorClasses != nil {
					for _, storeName := range RequiredStores(sender) {
						if classes, ok := hooks.StoreDeclaredErrorClasses(storeName); ok {
							vocabularyKnown = true
							matched = matched || errorClassMatchesDeclared(leaf, classes)
						}
					}
				}
				if vocabularyKnown && !matched {
					res.Warnings = append(res.Warnings, ValidationWarning{
						Path: sbase + ".type",
						Msg: fmt.Sprintf("error class %q is not in any vocabulary declared by sender %q "+
							"(executor %q or its stores: producers); the subscription registers but will only "+
							"fire if a peer emits this exact class", leaf, s.Node, sender.Executor),
					})
				}
			}
		}
		if isRealNode && tmpl != nil && s.When != "" && hooks.ExecutorDeclaredTags != nil {
			senderIdx := declared[s.Node]
			sender := tmpl.Nodes[senderIdx]
			if sender.Executor != "" {
				if declaredTags, ok := hooks.ExecutorDeclaredTags(sender.Executor); ok {
					declaredSet := map[string]struct{}{}
					for _, dt := range declaredTags {
						declaredSet[dt] = struct{}{}
					}
					for _, tag := range extractPayloadTagLiterals(s.When) {
						if _, present := declaredSet[tag]; !present {
							res.Warnings = append(res.Warnings, ValidationWarning{
								Path: sbase + ".when",
								Msg: fmt.Sprintf("tag %q referenced in `payload.tags` filter is not declared by sender %q's executor %q; "+
									"the subscription registers but will only fire if the emitter sends this exact tag",
									tag, s.Node, sender.Executor),
							})
						}
					}
				}
			}
		}
	}
	type subKey struct {
		node     string
		instance bool
		typ      string
		when     string
	}
	type subEntry struct {
		idx     int
		wake    bool
		refresh bool
	}
	groups := map[subKey][]subEntry{}
	for i, s := range n.Subscribes {
		if s.WakeOnChange == nil || s.ForceUpstreamRefresh == nil {
			continue
		}
		k := subKey{
			node:     s.Node,
			instance: s.Instance,
			typ:      s.Type,
			when:     s.When,
		}
		groups[k] = append(groups[k], subEntry{
			idx: i, wake: *s.WakeOnChange, refresh: *s.ForceUpstreamRefresh,
		})
	}
	for k, entries := range groups {
		if len(entries) < 2 {
			continue
		}
		first := entries[0]
		for _, e := range entries[1:] {
			if e.wake == first.wake && e.refresh == first.refresh {
				continue
			}
			res.Errors = append(res.Errors, ValidationError{
				Path: fmt.Sprintf("%s.subscribes[%d]", base, e.idx),
				Msg: fmt.Sprintf(
					"conflicting cascade-shape flags for subscription key "+
						"(node:%q instance:%t type:%q when:%q): entry %d has "+
						"wake_on_change=%t force_upstream_refresh=%t but entry %d has "+
						"wake_on_change=%t force_upstream_refresh=%t — a single subscription "+
						"key must declare one coherent cascade contract",
					k.node, k.instance, k.typ, k.when,
					first.idx, first.wake, first.refresh,
					e.idx, e.wake, e.refresh),
			})
		}
	}
}

//	@concept: attribute
//	@concept: node-subscription
func validateSubstitutionRefExistence(
	spec *TemplateSpec,
	declared map[string]int,
	hooks RegistryHooks,
	refs map[string][]substitutionRef,
	res *ValidationResult,
) {
	for receiverType, list := range refs {
		for _, ref := range list {
			path := fmt.Sprintf("nodes[%s].attributes.schema (substitution ref)", receiverType)
			senderIdx, declaredOk := declared[ref.SenderNodeType]
			if !declaredOk {
				res.Errors = append(res.Errors, ValidationError{
					Path: path,
					Msg:  fmt.Sprintf("substitution ref `nodes.%s.%s.%s` references unknown node %q", ref.SenderNodeType, ref.TopicKind, ref.Name, ref.SenderNodeType),
				})
				continue
			}
			sender := spec.Nodes[senderIdx]
			switch ref.TopicKind {
			case "attribute":
				if ref.Name == "" {
					continue
				}
				if !attributeKeyDeclared(sender, ref.Name) {
					res.Errors = append(res.Errors, ValidationError{
						Path: path,
						Msg:  fmt.Sprintf("substitution ref `nodes.%s.attribute.%s` references an attribute key not declared on the sender", ref.SenderNodeType, ref.Name),
					})
				}
			}
		}
	}
}

//	@concept: node-subscription
//	@concept: attribute
//	@decision: substitution-ref-coverage-required
//	@decision: coverage-wildcard-asymmetry
//	@decision: uncovered-substitution-error-shape
func validateSubstitutionRefCoverage(tmpl *TemplateSpec, refs map[string][]substitutionRef, res *ValidationResult) {
	if tmpl == nil {
		return
	}
	indexByReceiver := make(map[string]map[coverageEntryKey]struct{}, len(tmpl.Nodes))
	for _, n := range tmpl.Nodes {
		idx := map[coverageEntryKey]struct{}{}
		for _, s := range n.Subscribes {
			if s.Instance {
				continue
			}
			if s.Node == "" {
				continue
			}
			idx[coverageEntryKey{sender: s.Node, typ: s.Type}] = struct{}{}
		}
		indexByReceiver[n.Type] = idx
	}
	for receiverType, list := range refs {
		idx := indexByReceiver[receiverType]
		for _, ref := range list {
			suggestedType, covered := coverageMatch(idx, ref)
			if covered {
				continue
			}
			res.StructuredErrors = append(res.StructuredErrors, map[string]any{
				"kind":               "substitution_ref_uncovered",
				"receiver_node_type": receiverType,
				"ref":                ref.RefLiteral,
				"attribute_property": ref.AttributeProperty,
				"suggested_subscribes_entry": map[string]any{
					"node":                   ref.SenderNodeType,
					"type":                   suggestedType,
					"wake_on_change":         false,
					"force_upstream_refresh": false,
				},
				"suggested_subscribes_note": fmt.Sprintf(
					"set wake_on_change: true if this ref should also fire this receiver; set force_upstream_refresh: true if %s should be re-evaluated when this receiver is invalidated",
					ref.SenderNodeType,
				),
			})
		}
	}
}

type coverageEntryKey struct {
	sender string
	typ    string
}

func coverageMatch(idx map[coverageEntryKey]struct{}, ref substitutionRef) (suggestedType string, covered bool) {
	switch ref.TopicKind {
	case "attribute":
		if ref.Name == "" {
			suggestedType = "attribute/*"
			if _, ok := idx[coverageEntryKey{sender: ref.SenderNodeType, typ: "attribute/*"}]; ok {
				return suggestedType, true
			}
			return suggestedType, false
		}
		suggestedType = fmt.Sprintf("attribute/%s/changed", ref.Name)
		if _, ok := idx[coverageEntryKey{sender: ref.SenderNodeType, typ: suggestedType}]; ok {
			return suggestedType, true
		}
		if _, ok := idx[coverageEntryKey{sender: ref.SenderNodeType, typ: "attribute/*"}]; ok {
			return suggestedType, true
		}
		return suggestedType, false
	}
	return "", true
}

func validateExecutorCoherence(n TemplateNodeDef, base string, hooks RegistryHooks, res *ValidationResult) {
	hasExecutor := n.Executor != ""
	hasDelegate := n.Delegate != ""
	hasEmitsMessage := n.EmitsMessage != ""
	if hasExecutor && hasDelegate && !n.IsSubgraphEntryAbsorbed {
		res.Errors = append(res.Errors, ValidationError{
			Path: fmt.Sprintf("%s.delegate", base),
			Msg: fmt.Sprintf(
				"delegate and executor are mutually exclusive (executor=%q, delegate=%q)",
				n.Executor, n.Delegate),
		})
	}
	if hasExecutor && hasEmitsMessage {
		res.Errors = append(res.Errors, ValidationError{
			Path: fmt.Sprintf("%s.emits_message", base),
			Msg: fmt.Sprintf(
				"emits_message and executor are mutually exclusive (executor=%q, emits_message=%q)",
				n.Executor, n.EmitsMessage),
		})
	}
	if hasDelegate && hasEmitsMessage {
		res.Errors = append(res.Errors, ValidationError{
			Path: fmt.Sprintf("%s.emits_message", base),
			Msg: fmt.Sprintf(
				"emits_message and delegate are mutually exclusive (delegate=%q, emits_message=%q)",
				n.Delegate, n.EmitsMessage),
		})
	}
	if !hasExecutor && !hasDelegate && !hasEmitsMessage && effectiveExecutor(n, hooks) == "" {
		if n.Attributes != nil && len(n.Attributes.Schema) > 0 {
			res.Warnings = append(res.Warnings, ValidationWarning{
				Path: fmt.Sprintf("%s.attributes", base),
				Msg:  "pure-cascade node declares attributes; attribute values are only consumed by executors",
			})
		}
	}
}

// @concept: node
func validateKindDeclaration(n TemplateNodeDef, base string, hooks RegistryHooks, res *ValidationResult) {
	if n.Kind == "" {
		return
	}
	if n.Executor != "" {
		res.Errors = append(res.Errors, ValidationError{
			Path: base + ".kind",
			Msg:  "node declares both kind and executor; pick one",
		})
		return
	}
	if n.Delegate != "" {
		res.Errors = append(res.Errors, ValidationError{
			Path: base + ".kind",
			Msg: fmt.Sprintf(
				"node declares both kind and delegate; kind is incompatible with subgraph delegation (kind=%q, delegate=%q)",
				n.Kind, n.Delegate),
		})
		return
	}
	if hooks.KindAliases == nil {
		res.Errors = append(res.Errors, ValidationError{
			Path: base + ".kind",
			Msg:  fmt.Sprintf("kind %q is not registered (no kind aliases configured)", n.Kind),
		})
		return
	}
	if _, ok := hooks.KindAliases.Resolve(n.Kind); !ok {
		res.Errors = append(res.Errors, ValidationError{
			Path: base + ".kind",
			Msg:  fmt.Sprintf("kind %q is not registered", n.Kind),
		})
	}
}

func validateExecutorDeclared(n TemplateNodeDef, base string, hooks RegistryHooks, res *ValidationResult) {
	if n.Executor == "" || hooks.ExecutorDeclared == nil {
		return
	}
	if hooks.RefValidationMode == RefValidateNone {
		return
	}
	if hooks.ExecutorDeclared(n.Executor) {
		return
	}
	if hooks.RefValidationMode == RefValidateAvailable {
		return
	}
	res.Errors = append(res.Errors, ValidationError{
		Path: base + ".executor",
		Msg: refValidationModeRejection(
			fmt.Sprintf("executor %q is not declared in the operator's executors: block", n.Executor),
			hooks.RefValidationMode),
	})
}

// @concept: message-emitter-node
func validateEmitsMessage(n TemplateNodeDef, base string, spec *TemplateSpec, declaredMessages map[string]struct{}, res *ValidationResult) {
	if n.EmitsMessage == "" {
		return
	}
	mt := strings.TrimSpace(n.EmitsMessage)
	if mt == "" {
		res.Errors = append(res.Errors, ValidationError{
			Path: base + ".emits_message",
			Msg:  "emits_message must not be whitespace-only",
		})
		return
	}
	if _, ok := declaredMessages[mt]; !ok {
		declaredList := make([]string, 0, len(declaredMessages))
		for k := range declaredMessages {
			declaredList = append(declaredList, k)
		}
		sort.Strings(declaredList)
		res.Errors = append(res.Errors, ValidationError{
			Path: base + ".emits_message",
			Msg: fmt.Sprintf(
				"emits_message references unknown message type %q (declared types: %v)",
				mt, declaredList),
		})
		return
	}
	var dest *MessageSchema
	if spec != nil {
		for i := range spec.Messages {
			if strings.TrimSpace(spec.Messages[i].Type) == mt {
				dest = &spec.Messages[i]
				break
			}
		}
	}
	if dest == nil {
		res.Errors = append(res.Errors, ValidationError{
			Path: base + ".emits_message",
			Msg: fmt.Sprintf(
				"emits_message %q is in the declared set but not resolvable in messages: registry (internal validator drift)",
				mt),
		})
		return
	}
	var nodeSchema map[string]any
	if n.Attributes != nil {
		nodeSchema = n.Attributes.Schema
	}
	// @concept: message-emitter-node — the attributes block IS the body.
	var bodyShape map[string]any
	if len(dest.BodySchema) > 0 {
		var raw any
		if err := json.Unmarshal(dest.BodySchema, &raw); err == nil {
			if m, ok := raw.(map[string]any); ok {
				bodyShape = m
			}
		}
	}
	bodyProps := emitsMessageProperties(bodyShape)
	nodeProps := emitsMessageProperties(nodeSchema)
	bodyRequired := emitsMessageRequiredSet(bodyShape)
	nodeRequired := emitsMessageRequiredSet(nodeSchema)

	for name := range nodeProps {
		if _, ok := bodyProps[name]; !ok {
			res.Errors = append(res.Errors, ValidationError{
				Path: fmt.Sprintf("%s.attributes.schema.properties.%s", base, name),
				Msg: fmt.Sprintf(
					"emit-node attribute %q is not declared in destination message type %q's body_schema (the attribute set must match the body shape exactly)",
					name, mt),
			})
		}
	}
	for name := range bodyProps {
		if _, ok := nodeProps[name]; !ok {
			res.Errors = append(res.Errors, ValidationError{
				Path: fmt.Sprintf("%s.attributes.schema.properties", base),
				Msg: fmt.Sprintf(
					"emit-node attributes schema is missing field %q declared in destination message type %q's body_schema (the attribute set must match the body shape exactly)",
					name, mt),
			})
		}
	}
	for name, np := range nodeProps {
		bp, ok := bodyProps[name]
		if !ok {
			continue
		}
		npType, npHasType := np["type"]
		bpType, bpHasType := bp["type"]
		if npHasType && bpHasType && !jsonValuesEqual(npType, bpType) {
			res.Errors = append(res.Errors, ValidationError{
				Path: fmt.Sprintf("%s.attributes.schema.properties.%s.type", base, name),
				Msg: fmt.Sprintf(
					"emit-node attribute %q declares type %v but destination message type %q's body_schema declares type %v (types must match exactly)",
					name, npType, mt, bpType),
			})
		}
	}
	for r := range nodeRequired {
		if _, ok := bodyRequired[r]; !ok {
			res.Errors = append(res.Errors, ValidationError{
				Path: fmt.Sprintf("%s.attributes.schema.required", base),
				Msg: fmt.Sprintf(
					"emit-node requires %q but destination message type %q's body_schema does not require it (required: sets must match exactly)",
					r, mt),
			})
		}
	}
	for r := range bodyRequired {
		if _, ok := nodeRequired[r]; !ok {
			res.Errors = append(res.Errors, ValidationError{
				Path: fmt.Sprintf("%s.attributes.schema.required", base),
				Msg: fmt.Sprintf(
					"destination message type %q's body_schema requires %q but emit-node attributes schema does not require it (required: sets must match exactly)",
					mt, r),
			})
		}
	}
}

func emitsMessageProperties(schema map[string]any) map[string]map[string]any {
	out := map[string]map[string]any{}
	if schema == nil {
		return out
	}
	props, _ := schema["properties"].(map[string]any)
	if props == nil {
		return out
	}
	for name, raw := range props {
		if m, ok := raw.(map[string]any); ok {
			out[name] = m
		} else {
			out[name] = map[string]any{}
		}
	}
	return out
}

func emitsMessageRequiredSet(schema map[string]any) map[string]struct{} {
	out := map[string]struct{}{}
	if schema == nil {
		return out
	}
	raw, ok := schema["required"]
	if !ok {
		return out
	}
	list, ok := raw.([]any)
	if !ok {
		return out
	}
	for _, v := range list {
		if s, ok := v.(string); ok {
			out[s] = struct{}{}
		}
	}
	return out
}

func validateStores(n TemplateNodeDef, base string, hooks RegistryHooks, res *ValidationResult) {
	seenAlias := make(map[string]int, len(n.Stores))
	for j, s := range n.Stores {
		sbase := fmt.Sprintf("%s.stores[%d]", base, j)
		name := strings.TrimSpace(s.Name)
		if name == "" {
			res.Errors = append(res.Errors, ValidationError{
				Path: sbase + ".name", Msg: "store name is required",
			})
			continue
		}
		if hooks.StoreDeclared != nil && hooks.RefValidationMode != RefValidateNone {
			if !hooks.StoreDeclared(name) && hooks.RefValidationMode != RefValidateAvailable {
				res.Errors = append(res.Errors, ValidationError{
					Path: sbase + ".name",
					Msg: refValidationModeRejection(
						fmt.Sprintf("store %q is not declared in the operator's stores: block", name),
						hooks.RefValidationMode),
				})
				continue
			}
		}
		switch s.Intent {
		case "r", "rw":
		case "":
			res.Errors = append(res.Errors, ValidationError{
				Path: sbase + ".intent",
				Msg:  "intent is required (\"r\" or \"rw\")",
			})
		default:
			res.Errors = append(res.Errors, ValidationError{
				Path: sbase + ".intent",
				Msg:  fmt.Sprintf("intent = %q is not valid (one of: \"r\", \"rw\")", s.Intent),
			})
		}
		if strings.TrimSpace(s.Selector) == "" {
			res.Errors = append(res.Errors, ValidationError{
				Path: sbase + ".selector",
				Msg:  "selector is required",
			})
		} else {
			checkScopeDirectives(s.Selector, sbase+".selector", res)
		}
		alias := s.AliasOf()
		if prev, dup := seenAlias[alias]; dup {
			res.Errors = append(res.Errors, ValidationError{
				Path: sbase + ".alias",
				Msg:  fmt.Sprintf("duplicate claim alias %q (already at stores[%d])", alias, prev),
			})
			continue
		}
		seenAlias[alias] = j

		switch s.Lifetime {
		case "", ClaimLifetimeSubgraph, ClaimLifetimeDurable:
		default:
			res.Errors = append(res.Errors, ValidationError{
				Path: sbase + ".lifetime",
				Msg: fmt.Sprintf(
					"lifetime = %q is not valid (one of: %q, %q)",
					s.Lifetime, ClaimLifetimeSubgraph, ClaimLifetimeDurable),
			})
		}
		if s.Lifetime == ClaimLifetimeDurable && hooks.StoreAdvertisesDataProcessing != nil {
			if !hooks.StoreAdvertisesDataProcessing(name) {
				res.Errors = append(res.Errors, ValidationError{
					Path: sbase + ".lifetime",
					Msg: fmt.Sprintf(
						"lifetime = %q requires store %q to advertise the data_processing protocol (asset pattern)",
						ClaimLifetimeDurable, name),
				})
			}
		}
	}
}

const (
	ClaimLifetimeSubgraph = "subgraph"
	ClaimLifetimeDurable  = "durable"
)

func validateLocks(n TemplateNodeDef, base string, hooks RegistryHooks, res *ValidationResult) {
	seen := make(map[string]int, len(n.Locks))
	for j, l := range n.Locks {
		lbase := fmt.Sprintf("%s.locks[%d]", base, j)
		name := strings.TrimSpace(l.Name)
		if name == "" {
			res.Errors = append(res.Errors, ValidationError{
				Path: lbase + ".name", Msg: "lock name is required",
			})
			continue
		}
		checkLockNameDirectives(name, lbase+".name", res)
		if prev, dup := seen[name]; dup {
			res.Errors = append(res.Errors, ValidationError{
				Path: lbase + ".name",
				Msg:  fmt.Sprintf("duplicate lock name %q (already at locks[%d])", name, prev),
			})
			continue
		}
		seen[name] = j
		if hooks.NamedLockDeclared != nil && hooks.RefValidationMode != RefValidateNone {
			if !strings.ContainsAny(name, "{") && !hooks.NamedLockDeclared(name) &&
				hooks.RefValidationMode != RefValidateAvailable {
				res.Errors = append(res.Errors, ValidationError{
					Path: lbase + ".name",
					Msg: refValidationModeRejection(
						fmt.Sprintf("named lock %q is not declared in the operator's named_locks: block", name),
						hooks.RefValidationMode),
				})
			}
		}
	}
}

// @concept: attribute
func validateAttributesSchema(n TemplateNodeDef, base string, declared map[string]int, spec *TemplateSpec, hooks RegistryHooks, res *ValidationResult) {
	if n.Attributes == nil || len(n.Attributes.Schema) == 0 {
		return
	}
	sbase := fmt.Sprintf("%s.attributes.schema", base)

	schemaBytes, err := json.Marshal(n.Attributes.Schema)
	if err != nil {
		res.Errors = append(res.Errors, ValidationError{
			Path: sbase, Msg: fmt.Sprintf("failed to marshal schema for parse: %v", err),
		})
		return
	}
	compiler := jsonschema.NewCompiler()
	if err := compiler.AddResource("template-attrs.json", bytes.NewReader(schemaBytes)); err != nil {
		res.Errors = append(res.Errors, ValidationError{
			Path: sbase, Msg: fmt.Sprintf("schema is not valid JSON: %v", err),
		})
		return
	}
	if _, err := compiler.Compile("template-attrs.json"); err != nil {
		res.Errors = append(res.Errors, ValidationError{
			Path: sbase, Msg: fmt.Sprintf("schema does not compile: %v", err),
		})
	}

	directAliases := make(map[string]struct{}, len(n.Stores))
	for _, s := range n.Stores {
		directAliases[s.AliasOf()] = struct{}{}
	}
	heldAliases := make(map[string]struct{}, len(n.Holds))
	for alias := range n.Holds {
		heldAliases[alias] = struct{}{}
	}

	properties, ok := n.Attributes.Schema["properties"].(map[string]any)
	if !ok {
		return
	}
	for fname, raw := range properties {
		propMap, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		srcRaw, hasSource := propMap["source"]
		if hasSource {
			src, ok := srcRaw.(string)
			if !ok {
				res.Errors = append(res.Errors, ValidationError{
					Path: fmt.Sprintf("%s.properties.%s.source", sbase, fname),
					Msg:  "source must be a string (array-form multi-source is not admitted)",
				})
				continue
			}
			checkAttributeSource(src, fmt.Sprintf("%s.properties.%s.source", sbase, fname), declared, directAliases, heldAliases, res)
		}
	}

	if hooks.RefValidationMode == RefValidateNone {
		effective := MergeAttributeDefaults(nil, nil, n.Attributes.Schema)
		checkAttributesSchema(effective, n.Attributes.Schema, nil, map[string]bool{}, false, sbase, res)
		return
	}

	executorForSchema := effectiveExecutor(n, hooks)

	var execSchema map[string]any
	var execReadOnlyProps map[string]bool
	execSchemaVisible := false
	if executorForSchema != "" && hooks.ExecutorExpectedAttributesSchema != nil {
		if execBytes, ok := hooks.ExecutorExpectedAttributesSchema(executorForSchema); ok && len(execBytes) > 0 {
			if err := json.Unmarshal(execBytes, &execSchema); err != nil {
				res.Errors = append(res.Errors, ValidationError{
					Path: base + ".attributes",
					Msg:  fmt.Sprintf("executor %q expected_attributes_schema is not valid JSON: %v", executorForSchema, err),
				})
				return
			}
			execReadOnlyProps = extractReadOnlyProps(execSchema)
			execSchemaVisible = true
		}
	}
	if execReadOnlyProps == nil {
		execReadOnlyProps = map[string]bool{}
	}

	if !execSchemaVisible && hooks.RefValidationMode == RefValidateAll &&
		executorForSchema != "" && hooks.ExecutorExpectedAttributesSchema != nil {
		res.Errors = append(res.Errors, ValidationError{
			Path: base + ".attributes",
			Msg: refValidationModeRejection(
				fmt.Sprintf("executor %q expected_attributes_schema is not visible at registration", executorForSchema),
				hooks.RefValidationMode),
		})
	}

	var l1Defaults map[string]any
	if spec != nil && spec.Defaults != nil && spec.Defaults.Attributes != nil && executorForSchema != "" {
		l1Defaults = spec.Defaults.Attributes.ByExecutor[executorForSchema]
	}

	effective := MergeAttributeDefaults(execSchema, l1Defaults, n.Attributes.Schema)
	checkAttributesSchema(effective, n.Attributes.Schema, execSchema, execReadOnlyProps, execSchemaVisible, sbase, res)
	if execSchemaVisible {
		validateCompositionAgainstExecutor(execSchema, l1Defaults, n.Attributes.Schema, sbase, res)
	}
}

// @concept: attribute
func validateCompositionAgainstExecutor(execSchema, l1Defaults, nodeSchema map[string]any, sbase string, res *ValidationResult) {
	if execSchema == nil {
		return
	}
	execProps, _ := execSchema["properties"].(map[string]any)
	nodeProps, _ := nodeSchema["properties"].(map[string]any)
	additionalProperties, hasAddProps := execSchema["additionalProperties"]
	closed := false
	if hasAddProps {
		if b, ok := additionalProperties.(bool); ok && !b {
			closed = true
		}
	}

	for name, raw := range nodeProps {
		nodeProp, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		nodeType, nodeHasType := nodeProp["type"]
		if !nodeHasType {
			continue
		}
		execProp, _ := execProps[name].(map[string]any)
		if execProp == nil {
			continue
		}
		execType, execHasType := execProp["type"]
		if !execHasType {
			continue
		}
		if !jsonValuesEqual(nodeType, execType) {
			res.Errors = append(res.Errors, ValidationError{
				Path: fmt.Sprintf("%s.properties.%s.type", sbase, name),
				Msg: fmt.Sprintf(
					"template declares property %s.type: %v but executor's expected_attributes_schema declares type: %v — the executor is authoritative on types",
					name, nodeType, execType),
			})
		}
	}

	if closed && execProps != nil {
		for name := range nodeProps {
			if _, declared := execProps[name]; !declared {
				res.Errors = append(res.Errors, ValidationError{
					Path: fmt.Sprintf("%s.properties.%s", sbase, name),
					Msg: fmt.Sprintf(
						"property %s is not declared in executor's expected_attributes_schema and the executor's schema is closed (additionalProperties: false)",
						name),
				})
			}
		}
		for name := range l1Defaults {
			if _, declared := execProps[name]; declared {
				continue
			}
			if _, declaredInNode := nodeProps[name]; declaredInNode {
				continue
			}
			res.Errors = append(res.Errors, ValidationError{
				Path: fmt.Sprintf("defaults.attributes.by_executor.%s", name),
				Msg: fmt.Sprintf(
					"property %s is not declared in executor's expected_attributes_schema and the executor's schema is closed (additionalProperties: false)",
					name),
			})
		}
	}

	defaultsBag := map[string]any{}
	for name, val := range l1Defaults {
		defaultsBag[name] = val
	}
	for name, raw := range nodeProps {
		nodeProp, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		if defaultVal, hasDefault := nodeProp["default"]; hasDefault {
			defaultsBag[name] = defaultVal
		}
	}
	if len(defaultsBag) == 0 {
		return
	}
	schemaForDefaults := schemaWithoutTopLevelRequired(execSchema)
	if err := validateAgainstSchema(schemaForDefaults, defaultsBag); err != nil {
		res.Errors = append(res.Errors, ValidationError{
			Path: fmt.Sprintf("%s.defaults", sbase),
			Msg: fmt.Sprintf(
				"composed default values violate executor's expected_attributes_schema: %v",
				err),
		})
	}
}

func schemaWithoutTopLevelRequired(schema map[string]any) map[string]any {
	if schema == nil {
		return nil
	}
	if _, hasRequired := schema["required"]; !hasRequired {
		return schema
	}
	out := make(map[string]any, len(schema))
	for k, v := range schema {
		if k == "required" {
			continue
		}
		out[k] = v
	}
	return out
}

func validateAgainstSchema(schema, data map[string]any) error {
	schemaBytes, err := json.Marshal(schema)
	if err != nil {
		return fmt.Errorf("marshal schema: %w", err)
	}
	compiler := jsonschema.NewCompiler()
	if err := compiler.AddResource("composition.json", bytes.NewReader(schemaBytes)); err != nil {
		return fmt.Errorf("add resource: %w", err)
	}
	compiled, err := compiler.Compile("composition.json")
	if err != nil {
		return fmt.Errorf("compile schema: %w", err)
	}
	dataBytes, err := json.Marshal(data)
	if err != nil {
		return fmt.Errorf("marshal data: %w", err)
	}
	var normalized any
	if err := json.Unmarshal(dataBytes, &normalized); err != nil {
		return fmt.Errorf("unmarshal data: %w", err)
	}
	return compiled.Validate(normalized)
}

func jsonValuesEqual(a, b any) bool {
	ab, errA := json.Marshal(a)
	bb, errB := json.Marshal(b)
	if errA != nil || errB != nil {
		return false
	}
	return bytes.Equal(ab, bb)
}

func isValidFallbackLiteral(s string) bool {
	if s == "null" || s == "true" || s == "false" {
		return true
	}
	if len(s) >= 2 && s[0] == '"' && s[len(s)-1] == '"' {
		var str string
		if err := json.Unmarshal([]byte(s), &str); err == nil {
			return true
		}
		return false
	}
	var n float64
	return json.Unmarshal([]byte(s), &n) == nil
}

// @concept: attribute
func checkAttributeSource(src, path string, declared map[string]int, directAliases, heldAliases map[string]struct{}, res *ValidationResult) {
	trimmed := strings.TrimSpace(src)
	if trimmed == "" {
		res.Errors = append(res.Errors, ValidationError{
			Path: path, Msg: "source is empty",
		})
		return
	}
	matches := dispatchDirectiveRe.FindAllStringSubmatchIndex(trimmed, -1)
	if len(matches) == 0 {
		res.Errors = append(res.Errors, ValidationError{
			Path: path,
			Msg:  fmt.Sprintf("source must contain at least one {{...}} directive, got %q", trimmed),
		})
		return
	}
	for _, m := range matches {
		body := strings.TrimSpace(trimmed[m[2]:m[3]])
		checkAttributeDirectiveBody(body, path, declared, directAliases, heldAliases, res)
	}
}

func checkAttributeDirectiveBody(body, path string, declared map[string]int, directAliases, heldAliases map[string]struct{}, res *ValidationResult) {
	body = strings.TrimSpace(body)
	hasFallback := false
	hasLenient := false
	if idx := strings.Index(body, "|"); idx >= 0 {
		left := strings.TrimSpace(body[:idx])
		right := strings.TrimSpace(body[idx+1:])
		if strings.Contains(right, "|") {
			res.Errors = append(res.Errors, ValidationError{
				Path: path,
				Msg:  fmt.Sprintf("source directive %q has a multi-pipe fallback chain (only one literal admitted)", body),
			})
			return
		}
		if !isValidFallbackLiteral(right) {
			res.Errors = append(res.Errors, ValidationError{
				Path: path,
				Msg:  fmt.Sprintf("source directive %q fallback literal %q must be null, true, false, a JSON number, or a quoted string", body, right),
			})
			return
		}
		body = left
		hasFallback = true
	}
	if strings.HasSuffix(body, "?") {
		hasLenient = true
		body = strings.TrimSpace(strings.TrimSuffix(body, "?"))
	}
	if hasLenient && hasFallback {
		res.Errors = append(res.Errors, ValidationError{
			Path: path,
			Msg:  "source directive has both `?` marker and `| <literal>` fallback — pick one (incoherent: `?` says null on missing, `|` says literal on missing)",
		})
		return
	}
	bodyMatch := directiveBodyRe.FindStringSubmatch(body)
	if bodyMatch == nil {
		res.Errors = append(res.Errors, ValidationError{
			Path: path,
			Msg:  fmt.Sprintf("source directive %q must start with claim.|params.|nodes.", body),
		})
		return
	}
	kind := bodyMatch[1]
	rest := bodyMatch[2]
	parts := strings.Split(rest, ".")
	switch kind {
	case "claim":
		if len(parts) < 2 || parts[0] == "" {
			res.Errors = append(res.Errors, ValidationError{
				Path: path,
				Msg:  fmt.Sprintf("claim directive %q must be claim.<alias>.{address|claim_scope|payload[.<field>]}", body),
			})
			return
		}
		alias := parts[0]
		switch parts[1] {
		case "address", "claim_scope":
			if len(parts) != 2 {
				res.Errors = append(res.Errors, ValidationError{
					Path: path,
					Msg:  fmt.Sprintf("claim.<alias>.%s takes no further field path", parts[1]),
				})
			}
		case "payload":
			if len(parts) > 2 && parts[2] == "" {
				res.Errors = append(res.Errors, ValidationError{
					Path: path,
					Msg:  fmt.Sprintf("claim directive %q has an empty trailing segment", body),
				})
			}
		default:
			res.Errors = append(res.Errors, ValidationError{
				Path: path,
				Msg:  fmt.Sprintf("claim directive %q second segment must be address|claim_scope|payload", body),
			})
		}
		_, isOwn := directAliases[alias]
		_, isHeld := heldAliases[alias]
		if !isOwn && !isHeld {
			res.Errors = append(res.Errors, ValidationError{
				Path: path,
				Msg:  fmt.Sprintf("claim directive references alias %q which is neither acquired here (stores:) nor declared in holds:", alias),
			})
		}
	case "params":
		if len(parts) < 1 || parts[0] == "" {
			res.Errors = append(res.Errors, ValidationError{
				Path: path,
				Msg:  fmt.Sprintf("params directive %q must be params.<key>", body),
			})
		}
	case "nodes":
		if len(parts) < 2 || parts[0] == "" {
			res.Errors = append(res.Errors, ValidationError{
				Path: path,
				Msg:  fmt.Sprintf("nodes directive %q must be nodes.<node>.attribute[.<...>]", body),
			})
			return
		}
		switch parts[1] {
		case "attribute":
			if len(parts) > 2 && parts[2] == "" {
				res.Errors = append(res.Errors, ValidationError{
					Path: path,
					Msg:  fmt.Sprintf("nodes directive %q has an empty trailing segment", body),
				})
				return
			}
			if _, ok := declared[parts[0]]; !ok {
				res.Errors = append(res.Errors, ValidationError{
					Path: path,
					Msg:  fmt.Sprintf("nodes directive references unknown node %q", parts[0]),
				})
			}
		default:
			res.Errors = append(res.Errors, ValidationError{
				Path: path,
				Msg:  fmt.Sprintf("nodes directive %q second segment must be 'attribute'", body),
			})
		}
	case "trigger":
		if len(parts) < 2 || parts[0] != "message" {
			res.Errors = append(res.Errors, ValidationError{
				Path: path,
				Msg:  fmt.Sprintf("trigger directive %q must be trigger.message.payload[.<field>]", body),
			})
			return
		}
		if len(parts) < 2 || parts[1] != "payload" {
			res.Errors = append(res.Errors, ValidationError{
				Path: path,
				Msg:  fmt.Sprintf("trigger directive %q must be trigger.message.payload[.<field>]", body),
			})
			return
		}
		if len(parts) > 2 && parts[2] == "" {
			res.Errors = append(res.Errors, ValidationError{
				Path: path,
				Msg:  fmt.Sprintf("trigger directive %q has an empty trailing segment", body),
			})
		}
	case "child":
		if len(parts) != 1 || parts[0] != "partition_key" {
			res.Errors = append(res.Errors, ValidationError{
				Path: path,
				Msg:  fmt.Sprintf("child directive %q must be child.partition_key", body),
			})
		}
	case "messages":
		if len(parts) < 1 || parts[0] == "" {
			res.Errors = append(res.Errors, ValidationError{
				Path: path,
				Msg:  fmt.Sprintf("messages directive %q must be messages.<type>[.<field>]", body),
			})
			return
		}
		if len(parts) > 2 && parts[2] == "" {
			res.Errors = append(res.Errors, ValidationError{
				Path: path,
				Msg:  fmt.Sprintf("messages directive %q has an empty trailing segment", body),
			})
		}
	default:
		res.Errors = append(res.Errors, ValidationError{
			Path: path,
			Msg:  fmt.Sprintf("unknown directive kind %q", kind),
		})
	}
}

func checkScopeDirectives(s, path string, res *ValidationResult) {
	checkDispatchDirectives(s, path, res)
	stripped := dispatchDirectiveRe.ReplaceAllString(s, "")
	for _, m := range anyBraceRe.FindAllString(stripped, -1) {
		if !instantiationPlaceholderRe.MatchString(m) {
			res.Errors = append(res.Errors, ValidationError{
				Path: path,
				Msg:  fmt.Sprintf("invalid placeholder %q (expected {params.<key>} or {{...}} directive)", m),
			})
		}
	}
}

func checkLockNameDirectives(s, path string, res *ValidationResult) {
	checkScopeDirectives(s, path, res)
}

func checkDispatchDirectives(s, path string, res *ValidationResult) {
	for _, m := range dispatchDirectiveRe.FindAllStringSubmatch(s, -1) {
		body := strings.TrimSpace(m[1])
		if body == "" {
			res.Errors = append(res.Errors, ValidationError{
				Path: path,
				Msg:  "empty {{...}} directive",
			})
			continue
		}
		if !directiveBodyRe.MatchString(body) {
			res.Errors = append(res.Errors, ValidationError{
				Path: path,
				Msg:  fmt.Sprintf("invalid directive %q (expected claim.<a>.{address|claim_scope|payload[.<f>]}, params.<k>, nodes.<n>.attribute[.<f>], trigger.message.payload[.<f>], child.partition_key, or messages.<type>[.<field>])", body),
			})
		}
	}
}

func validateMaxParkDuration(n TemplateNodeDef, base string, res *ValidationResult) {
	if n.MaxParkDuration == "" {
		return
	}
	if _, err := parseDurationStrict(n.MaxParkDuration); err != nil {
		res.Errors = append(res.Errors, ValidationError{
			Path: base + ".max_park_duration",
			Msg:  fmt.Sprintf("invalid duration %q: %v", n.MaxParkDuration, err),
		})
	}
}

// @concept: dispatch-deadlines
func validateDispatchDeadlines(n TemplateNodeDef, base string, res *ValidationResult) {
	for _, kv := range []struct {
		value string
		field string
	}{
		{n.SyncRPCDeadline, "sync_rpc_deadline"},
		{n.MaxQuietPeriod, "max_quiet_period"},
		{n.MaxRuntime, "max_runtime"},
	} {
		if kv.value == "" {
			continue
		}
		d, err := parseDurationStrict(kv.value)
		if err != nil {
			res.Errors = append(res.Errors, ValidationError{
				Path: base + "." + kv.field,
				Msg:  fmt.Sprintf("invalid duration %q: %v", kv.value, err),
			})
			continue
		}
		if d < 0 {
			res.Errors = append(res.Errors, ValidationError{
				Path: base + "." + kv.field,
				Msg:  fmt.Sprintf("negative duration %q: deadlines must be >= 0", kv.value),
			})
		}
	}
}

type AttributesSchemaCheckError struct {
	Path string
	Msg  string
}

// @concept: attribute
func CheckEffectiveAttributesSchema(effective, nodeSchema, execSchema map[string]any, executorReadOnlyProps map[string]bool, execSchemaVisible bool) []AttributesSchemaCheckError {
	var out []AttributesSchemaCheckError
	if effective == nil {
		return nil
	}
	effProps, _ := effective["properties"].(map[string]any)
	nodeProps, _ := nodeSchema["properties"].(map[string]any)
	execProps, _ := execSchema["properties"].(map[string]any)
	schemaHasNoProps := IsPermissiveExecutorSchema(execSchema)
	execOpen := executorSchemaAllowsExtensions(execSchema)
	for name, raw := range effProps {
		prop, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		_, hasSource := prop["source"]
		_, hasDefault := prop["default"]
		execRO := executorReadOnlyProps[name]
		if hasSource && hasDefault {
			out = append(out, AttributesSchemaCheckError{
				Path: fmt.Sprintf("properties.%s", name),
				Msg:  "property declares both `source:` and `default:` — pick one",
			})
			continue
		}
		_, execEnumerates := execProps[name]
		propUnconstrained := schemaHasNoProps || (execOpen && !execEnumerates)
		if !hasSource && !hasDefault && !execRO && execSchemaVisible && !propUnconstrained {
			out = append(out, AttributesSchemaCheckError{
				Path: fmt.Sprintf("properties.%s", name),
				Msg:  "property has no `source:`, no `default:`, and is not marked `readOnly: true` in the executor's expected_attributes_schema — declare one of these or the property is unpopulated at dispatch",
			})
			continue
		}
		if nodeProps != nil && execSchemaVisible && !propUnconstrained {
			if rawNode, present := nodeProps[name]; present {
				if nodeProp, ok := rawNode.(map[string]any); ok {
					if ro, _ := nodeProp["readOnly"].(bool); ro && !execRO {
						out = append(out, AttributesSchemaCheckError{
							Path: fmt.Sprintf("properties.%s", name),
							Msg:  "template marks property `readOnly: true` but the executor's expected_attributes_schema does not — the executor is authoritative on which properties it produces",
						})
					}
				}
			}
		}
	}
	return out
}

// @concept: attribute
func IsPermissiveExecutorSchema(execSchema map[string]any) bool {
	if execSchema == nil {
		return false
	}
	_, hasProps := execSchema["properties"]
	return !hasProps
}

// @concept: attribute
func executorSchemaAllowsExtensions(execSchema map[string]any) bool {
	if execSchema == nil {
		return false
	}
	add, has := execSchema["additionalProperties"]
	if !has {
		return false
	}
	if b, ok := add.(bool); ok {
		return b
	}
	return true
}

// @concept: attribute
func checkAttributesSchema(effective, nodeSchema, execSchema map[string]any, executorReadOnlyProps map[string]bool, execSchemaVisible bool, sbase string, res *ValidationResult) {
	if effective == nil {
		return
	}
	effProps, _ := effective["properties"].(map[string]any)
	nodeProps, _ := nodeSchema["properties"].(map[string]any)
	execProps, _ := execSchema["properties"].(map[string]any)
	schemaHasNoProps := IsPermissiveExecutorSchema(execSchema)
	execOpen := executorSchemaAllowsExtensions(execSchema)
	for name, raw := range effProps {
		prop, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		_, hasSource := prop["source"]
		_, hasDefault := prop["default"]
		execRO := executorReadOnlyProps[name]
		if hasSource && hasDefault {
			res.Errors = append(res.Errors, ValidationError{
				Path: fmt.Sprintf("%s.properties.%s", sbase, name),
				Msg:  "property declares both `source:` and `default:` — pick one",
			})
			continue
		}
		_, execEnumerates := execProps[name]
		propUnconstrained := schemaHasNoProps || (execOpen && !execEnumerates)
		if !hasSource && !hasDefault && !execRO && execSchemaVisible && !propUnconstrained {
			res.Errors = append(res.Errors, ValidationError{
				Path: fmt.Sprintf("%s.properties.%s", sbase, name),
				Msg:  "property has no `source:`, no `default:`, and is not marked `readOnly: true` in the executor's expected_attributes_schema — declare one of these or the property is unpopulated at dispatch",
			})
			continue
		}
		if nodeProps != nil && execSchemaVisible && !propUnconstrained {
			if rawNode, present := nodeProps[name]; present {
				if nodeProp, ok := rawNode.(map[string]any); ok {
					if ro, _ := nodeProp["readOnly"].(bool); ro && !execRO {
						res.Errors = append(res.Errors, ValidationError{
							Path: fmt.Sprintf("%s.properties.%s", sbase, name),
							Msg:  "template marks property `readOnly: true` but the executor's expected_attributes_schema does not — the executor is authoritative on which properties it produces",
						})
					}
				}
			}
		}
	}
}

// @concept: attribute
func extractReadOnlyProps(schema map[string]any) map[string]bool {
	out := map[string]bool{}
	if schema == nil {
		return out
	}
	props, ok := schema["properties"].(map[string]any)
	if !ok {
		return out
	}
	for name, raw := range props {
		prop, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		if ro, _ := prop["readOnly"].(bool); ro {
			out[name] = true
		}
	}
	return out
}

// @concept: attribute
func MergeAttributeDefaults(execSchema map[string]any, l1Defaults map[string]any, nodeSchema map[string]any) map[string]any {
	out := deepCopyJSON(execSchema)
	if out == nil {
		out = map[string]any{}
	}
	props, _ := out["properties"].(map[string]any)
	if props == nil {
		props = map[string]any{}
		out["properties"] = props
	}
	for attr, val := range l1Defaults {
		prop, _ := props[attr].(map[string]any)
		if prop == nil {
			prop = map[string]any{}
			props[attr] = prop
		}
		prop["default"] = val
	}
	if nodeSchema != nil {
		if nodeProps, ok := nodeSchema["properties"].(map[string]any); ok {
			for attr, raw := range nodeProps {
				nodeProp, _ := raw.(map[string]any)
				if nodeProp == nil {
					continue
				}
				existing, _ := props[attr].(map[string]any)
				if existing == nil {
					props[attr] = deepCopyJSON(nodeProp)
					continue
				}
				if _, l2HasSource := nodeProp["source"]; l2HasSource {
					delete(existing, "default")
				}
				if _, l2HasDefault := nodeProp["default"]; l2HasDefault {
					delete(existing, "source")
				}
				for k, v := range nodeProp {
					existing[k] = v
				}
			}
		}
		if nodeReq, ok := nodeSchema["required"].([]any); ok && len(nodeReq) > 0 {
			existingReq, _ := out["required"].([]any)
			seen := map[string]bool{}
			for _, r := range existingReq {
				if s, ok := r.(string); ok {
					seen[s] = true
				}
			}
			for _, r := range nodeReq {
				if s, ok := r.(string); ok && !seen[s] {
					existingReq = append(existingReq, r)
					seen[s] = true
				}
			}
			out["required"] = existingReq
		}
		if _, execHas := out["additionalProperties"]; !execHas {
			if v, ok := nodeSchema["additionalProperties"]; ok {
				out["additionalProperties"] = v
			}
		}
	}
	return out
}

func deepCopyJSON(v map[string]any) map[string]any {
	if v == nil {
		return nil
	}
	b, err := json.Marshal(v)
	if err != nil {
		return map[string]any{}
	}
	var out map[string]any
	_ = json.Unmarshal(b, &out)
	return out
}

func parseDurationStrict(s string) (time.Duration, error) {
	return time.ParseDuration(s)
}

// @concept: node
func validateTagsAtRegistration(n TemplateNodeDef, base string, spec *TemplateSpec, res *ValidationResult) {
	if len(n.Tags) == 0 {
		return
	}
	props := paramsSchemaProperties(spec)
	for i, tag := range n.Tags {
		path := fmt.Sprintf("%s.tags[%d]", base, i)
		matches := dispatchDirectiveRe.FindAllStringSubmatch(tag, -1)
		for _, m := range matches {
			inside := strings.TrimSpace(m[1])
			body := directiveBodyRe.FindStringSubmatch(inside)
			if body == nil {
				res.Errors = append(res.Errors, ValidationError{
					Path: path,
					Msg:  fmt.Sprintf("tag directive {{%s}} is malformed (expected `params.<key>`)", inside),
				})
				continue
			}
			kind, rest := body[1], body[2]
			if kind != "params" {
				res.Errors = append(res.Errors, ValidationError{
					Path: path,
					Msg:  fmt.Sprintf("tag directive {{%s}} uses unsupported kind %q at materialization time (tags accept only params.<key>)", inside, kind),
				})
				continue
			}
			topKey := rest
			if dot := strings.Index(rest, "."); dot >= 0 {
				topKey = rest[:dot]
			}
			if _, ok := props[topKey]; !ok {
				res.Errors = append(res.Errors, ValidationError{
					Path: path,
					Msg:  fmt.Sprintf("tag references undeclared params key %q (declare it under params_schema.properties)", topKey),
				})
			}
		}
	}
}

func paramsSchemaProperties(spec *TemplateSpec) map[string]any {
	if spec == nil || spec.ParamsSchema == nil {
		return nil
	}
	props, _ := spec.ParamsSchema["properties"].(map[string]any)
	return props
}

// @concept: message-schema
func validatePublishers(spec *TemplateSpec, declared map[string]int, declaredMessages map[string]struct{}, res *ValidationResult) {
	seenNames := make(map[string]struct{}, len(spec.Publishers))
	for i, p := range spec.Publishers {
		base := fmt.Sprintf("publishers[%d]", i)
		if strings.TrimSpace(p.Name) == "" {
			res.Errors = append(res.Errors, ValidationError{
				Path: base + ".name", Msg: "name is required",
			})
		} else if _, dup := seenNames[p.Name]; dup {
			res.Errors = append(res.Errors, ValidationError{
				Path: base + ".name",
				Msg:  fmt.Sprintf("duplicate publisher name %q", p.Name),
			})
		} else {
			seenNames[p.Name] = struct{}{}
		}
		if strings.TrimSpace(p.Kind) == "" {
			res.Errors = append(res.Errors, ValidationError{
				Path: base + ".kind", Msg: "kind is required",
			})
		}
		if strings.TrimSpace(p.TargetNode) == "" {
			res.Errors = append(res.Errors, ValidationError{
				Path: base + ".target_node",
				Msg:  "target_node is required (cannot be empty)",
			})
			continue
		}
		if _, ok := declared[p.TargetNode]; !ok {
			res.Errors = append(res.Errors, ValidationError{
				Path: base + ".target_node",
				Msg:  fmt.Sprintf("target_node %q does not reference a declared node type", p.TargetNode),
			})
		}
		mt := strings.TrimSpace(p.MessageType)
		if mt == "" {
			res.Errors = append(res.Errors, ValidationError{
				Path: base + ".message_type",
				Msg:  "message_type is required (cannot be empty)",
			})
			continue
		}
		if _, ok := declaredMessages[mt]; !ok {
			declaredList := make([]string, 0, len(declaredMessages))
			for k := range declaredMessages {
				declaredList = append(declaredList, k)
			}
			sort.Strings(declaredList)
			res.Errors = append(res.Errors, ValidationError{
				Path: base + ".message_type",
				Msg: fmt.Sprintf(
					"message_type %q is not declared in the template's `messages:` registry (declared types: %v)",
					mt, declaredList),
			})
		}
	}
}

// @concept: message-schema
func buildMessageBodyFieldSet(spec *TemplateSpec) map[string]map[string]struct{} {
	out := map[string]map[string]struct{}{}
	for _, m := range spec.Messages {
		if len(m.BodySchema) == 0 {
			continue
		}
		var shape map[string]any
		if err := json.Unmarshal(m.BodySchema, &shape); err != nil {
			continue
		}
		props, ok := shape["properties"].(map[string]any)
		if !ok {
			out[m.Type] = map[string]struct{}{}
			continue
		}
		fields := make(map[string]struct{}, len(props))
		for k := range props {
			fields[k] = struct{}{}
		}
		out[m.Type] = fields
	}
	return out
}

// @concept: message-schema
func validateMessages(spec *TemplateSpec, declared map[string]int, res *ValidationResult) map[string]struct{} {
	declaredMessages := make(map[string]struct{}, len(spec.Messages))
	if len(spec.Messages) == 0 {
		return declaredMessages
	}
	for i, m := range spec.Messages {
		base := fmt.Sprintf("messages[%d]", i)
		t := strings.TrimSpace(m.Type)
		if t == "" {
			res.Errors = append(res.Errors, ValidationError{
				Path: base + ".type",
				Msg:  `type "" is reserved-for-runtime (the implicit empty-message wake trigger seeded automatically at registration; author-declared empty-type entries are refused)`,
			})
			continue
		}
		if strings.ContainsAny(t, " \t\n\r") {
			res.Errors = append(res.Errors, ValidationError{
				Path: base + ".type",
				Msg:  fmt.Sprintf("type %q must not contain whitespace", t),
			})
			continue
		}
		if strings.HasPrefix(t, "/") || strings.HasSuffix(t, "/") {
			res.Errors = append(res.Errors, ValidationError{
				Path: base + ".type",
				Msg:  fmt.Sprintf("type %q must not start or end with `/`", t),
			})
			continue
		}
		segmentsValid := true
		segmentHasDot := false
		for _, seg := range strings.Split(t, "/") {
			if seg == "" {
				segmentsValid = false
				continue
			}
			if strings.Contains(seg, ".") {
				segmentHasDot = true
			}
		}
		segmentErrored := false
		if !segmentsValid {
			res.Errors = append(res.Errors, ValidationError{
				Path: base + ".type",
				Msg:  fmt.Sprintf("type %q has empty segment(s)", t),
			})
			segmentErrored = true
		}
		if segmentHasDot {
			res.Errors = append(res.Errors, ValidationError{
				Path: base + ".type",
				Msg: fmt.Sprintf(
					"type %q segments must not contain `.` (the substitution-directive parser splits on `.`)",
					t),
			})
			segmentErrored = true
		}
		if segmentErrored {
			continue
		}
		if !strings.Contains(t, "/") {
			res.Errors = append(res.Errors, ValidationError{
				Path: base + ".type",
				Msg: fmt.Sprintf(
					"type %q must be a slash-bearing type-path (e.g. `category/action`) so it cannot collide with a node-type",
					t),
			})
			continue
		}
		if _, dup := declaredMessages[t]; dup {
			res.Errors = append(res.Errors, ValidationError{
				Path: base + ".type",
				Msg:  fmt.Sprintf("duplicate message type %q", t),
			})
			continue
		}
		if _, nodeCollision := declared[t]; nodeCollision {
			res.Errors = append(res.Errors, ValidationError{
				Path: base + ".type",
				Msg: fmt.Sprintf(
					"message type %q collides with a declared node type of the same name; pick a distinct type-path so subscriptions can disambiguate",
					t),
			})
			continue
		}
		declaredMessages[t] = struct{}{}
		if len(m.BodySchema) == 0 {
			continue
		}
		var schemaShape any
		if err := json.Unmarshal(m.BodySchema, &schemaShape); err != nil {
			res.Errors = append(res.Errors, ValidationError{
				Path: base + ".body_schema",
				Msg:  fmt.Sprintf("body_schema is not valid JSON: %v", err),
			})
			continue
		}
		if _, ok := schemaShape.(map[string]any); !ok {
			res.Errors = append(res.Errors, ValidationError{
				Path: base + ".body_schema",
				Msg:  "body_schema must be a JSON Schema object",
			})
			continue
		}
		compiler := jsonschema.NewCompiler()
		if err := compiler.AddResource("body-schema.json", bytes.NewReader(m.BodySchema)); err != nil {
			res.Errors = append(res.Errors, ValidationError{
				Path: base + ".body_schema",
				Msg:  fmt.Sprintf("body_schema is not valid JSON Schema: %v", err),
			})
			continue
		}
		if _, err := compiler.Compile("body-schema.json"); err != nil {
			res.Errors = append(res.Errors, ValidationError{
				Path: base + ".body_schema",
				Msg:  fmt.Sprintf("body_schema does not compile: %v", err),
			})
			continue
		}
	}
	return declaredMessages
}

// @concept: terminal-tag
var payloadTagsLiteralRE = regexp.MustCompile(`["']([^"']+)["']\s+in\s+payload\.tags|payload\.tags\.contains\(\s*["']([^"']+)["']\s*\)`)

// @concept: terminal-tag
func extractPayloadTagLiterals(when string) []string {
	if when == "" {
		return nil
	}
	var tags []string
	seen := map[string]struct{}{}
	for _, match := range payloadTagsLiteralRE.FindAllStringSubmatch(when, -1) {
		for _, g := range match[1:] {
			if g == "" {
				continue
			}
			if _, dup := seen[g]; dup {
				continue
			}
			seen[g] = struct{}{}
			tags = append(tags, g)
		}
	}
	return tags
}
