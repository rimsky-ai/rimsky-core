// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package node

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/cascade"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/signal"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/spec"
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

var directiveBodyRe = regexp.MustCompile(`^(claim|params|nodes|child|messages|env)\.(.+)$`)

var envVarNameRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

type RegistryHooks struct {
	StoreDeclared     func(name string) bool
	NamedLockDeclared func(name string) bool
	ExecutorDeclared  func(name string) bool

	ExecutorDeclaredTags func(name string) ([]string, bool)

	//	@concept: signal
	ExecutorDeclaredErrorClasses func(name string) ([]string, bool)

	//	@concept: signal
	//	@concept: error-policy
	StoreDeclaredErrorClasses func(name string) ([]string, bool)

	ExecutorExpectedAttributesSchema func(name string) ([]byte, bool)

	ClaimProducerAdvertisesSplitScope func(name string) bool

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
	validateMessageQueueMode(spec, &res)

	rejectAuthorSetInternalFlags(spec, &res)
	canonicalizeGraphs(spec, &res)
	validateDelegateTargets(spec, &res)

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
		validateErrorTypes(n, base, hooks, &res)
		validateExecutorCoherence(n, base, hooks, &res)
		validateExecutorDeclared(n, base, hooks, &res)
		validateKindDeclaration(n, base, hooks, &res)
		validateSendsMessage(n, base, spec, declaredMessages, &res)
		validateClaimProducers(n, base, hooks, &res)
		validateLocks(n, base, hooks, &res)
		validateAttributesSchema(n, base, declared, spec, hooks, &res)
		validateAcquireUnavailablePolicyAdvised(n, base, &res)
		validateCascadeMode(n, base, &res)
		validateDispatchDeadlines(n, base, &res)
		validateHolds(n, base, spec, declared, &res)
		validateFanOut(n, base, spec, declared, hooks, &res)
		validateTagsAtRegistration(n, base, spec, &res)
	}

	validateHoldsAcyclic(spec, &res)
	validatePublishers(spec, declaredMessages, &res)

	// @concept: message-schema
	messageRefs := ExtractMessageRefsFromTemplate(*spec)
	messageBodyFields := buildMessageBodyFieldSet(spec)
	for _, receiverType := range sortedMessageRefKeys(messageRefs) {
		list := messageRefs[receiverType]
		for _, ref := range list {
			path := substitutionRefPath(declared, receiverType, ref)
			if _, ok := declaredMessages[ref.TypeName]; !ok {
				res.Errors = append(res.Errors, ValidationError{
					Path: path,
					Msg: fmt.Sprintf("substitution ref `messages.%s.%s` references unknown message type %q (not declared in `messages:`)",
						ref.TypeName, ref.FieldPath, ref.TypeName),
				})
				continue
			}
			if ref.FieldPath == "" {
				continue
			}
			fields, ok := messageBodyFields[ref.TypeName]
			if !ok {
				res.Errors = append(res.Errors, ValidationError{
					Path: path,
					Msg: fmt.Sprintf("substitution ref `messages.%s.%s` reads a field but message type %q declares no body_schema (empty body)",
						ref.TypeName, ref.FieldPath, ref.TypeName),
				})
				continue
			}
			if _, ok := fields[ref.FieldPath]; !ok {
				res.Errors = append(res.Errors, ValidationError{
					Path: path,
					Msg: fmt.Sprintf("substitution ref `messages.%s.%s` reads a field %q not declared in message type %q's body_schema",
						ref.TypeName, ref.FieldPath, ref.FieldPath, ref.TypeName),
				})
			}
		}
	}

	refs := ExtractSubstitutionRefsFromTemplate(*spec)
	validateSubstitutionRefExistence(spec, declared, hooks, refs, &res)
	validateSubstitutionRefCoverage(spec, refs, messageRefs, &res)

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

// @concept: instance
func validateMessageQueueMode(spec *TemplateSpec, res *ValidationResult) {
	switch spec.MessageQueueMode {
	case "", "backlog", "coalesce":
	default:
		res.Errors = append(res.Errors, ValidationError{
			Path: "message_queue_mode",
			Msg: fmt.Sprintf("message_queue_mode = %q; want one of backlog | coalesce (default backlog)",
				spec.MessageQueueMode),
		})
		return
	}
	if spec.MessageQueueMode != "coalesce" {
		return
	}
	distinct := make(map[string]struct{}, len(spec.Messages))
	for _, m := range spec.Messages {
		if m.Type != "" {
			distinct[m.Type] = struct{}{}
		}
	}
	if len(distinct) < 2 {
		return
	}
	types := make([]string, 0, len(distinct))
	for t := range distinct {
		types = append(types, t)
	}
	sort.Strings(types)
	res.Warnings = append(res.Warnings, ValidationWarning{
		Path: "message_queue_mode",
		Msg: fmt.Sprintf("message_queue_mode = coalesce with %d declared message types %v: coalesce cancels ALL "+
			"pending messages per instance regardless of type — a newly received message of one type cancels pending "+
			"messages of every other type; use backlog if distinct message types must not cancel each other",
			len(types), types),
	})
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
	execNames := make([]string, 0, len(spec.Defaults.Attributes.ByExecutor))
	for execName := range spec.Defaults.Attributes.ByExecutor {
		execNames = append(execNames, execName)
	}
	sort.Strings(execNames)
	for _, execName := range execNames {
		if _, ok := known[execName]; !ok {
			res.Errors = append(res.Errors, ValidationError{
				Path: fmt.Sprintf("defaults.attributes.by_executor[%q]", execName),
				Msg:  fmt.Sprintf("executor name %q does not match any node's executor", execName),
			})
		}
	}
}

func ApplyFrameResolutionDefaults(*TemplateSpec) {
}

// @concept: error-policy
// @concept: signal
func validateErrorTypes(n TemplateNodeDef, base string, hooks RegistryHooks, res *ValidationResult) {
	validActions := map[string]bool{
		spec.ActionPass:              true,
		spec.ActionGiveUp:            true,
		spec.ActionRetry:             true,
		spec.ActionReleaseAndRequeue: true,
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
		for _, storeName := range RequiredClaimProducers(n) {
			if classes, ok := hooks.StoreDeclaredErrorClasses(storeName); ok {
				producerClasses = append(producerClasses, classes...)
				vocabularyKnown = true
			}
		}
	}
	errorClassNames := make([]string, 0, len(n.ErrorTypes))
	for className := range n.ErrorTypes {
		errorClassNames = append(errorClassNames, className)
	}
	sort.Strings(errorClassNames)
	for _, className := range errorClassNames {
		policy := n.ErrorTypes[className]
		if !validActions[policy.Action] {
			res.Errors = append(res.Errors, ValidationError{
				Path: fmt.Sprintf("%s.error_types[%s].action", base, className),
				Msg:  fmt.Sprintf("unknown action %q; valid actions are: pass | give_up | retry | release_and_requeue", policy.Action),
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
				"not in the acquire/* synthetic family, and not declared by any producer in this node's claim_producers: block (declared: %v); "+
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
		"unresolved_executor",
		"executor_sync_timeout":
		return true
	}
	return false
}

// @concept: signal
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

// @concept: error-policy
func validateAcquireUnavailablePolicyAdvised(n TemplateNodeDef, base string, res *ValidationResult) {
	if len(n.ClaimProducers) == 0 {
		return
	}
	declared := make([]string, 0, len(n.ErrorTypes))
	for key := range n.ErrorTypes {
		declared = append(declared, key)
	}
	if errorClassMatchesDeclared("acquire/unavailable", declared) {
		return
	}
	res.Warnings = append(res.Warnings, ValidationWarning{
		Path: base + ".error_types",
		Msg: "node uses claim-producers but declares no \"acquire/unavailable\" error_types entry; " +
			"the default behavior on acquisition failure is fail-fast, not implicit retry. " +
			"Declare error_types: {\"acquire/unavailable\": ...} to choose a policy (e.g. retry).",
	})
}

// @concept: node-subscription
// @concept: signal
// @concept: message-schema
func validateSubscribes(n TemplateNodeDef, base string, declared map[string]int, declaredMessages map[string]struct{}, messageBodyFields map[string]map[string]struct{}, hooks RegistryHooks, tmpl *TemplateSpec, res *ValidationResult) {
	for i, s := range n.Subscribes {
		sbase := fmt.Sprintf("%s.subscribes[%d]", base, i)
		refreshKnown := s.ForceUpstreamRefresh != nil
		if !refreshKnown {
			res.Errors = append(res.Errors, ValidationError{
				Path: sbase + ".force_upstream_refresh",
				Msg:  "force_upstream_refresh is required (true or false); no default applies",
			})
		}
		if s.Node == "" {
			res.Errors = append(res.Errors, ValidationError{
				Path: sbase,
				Msg:  "must declare `node:`",
			})
			continue
		}
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
					for _, storeName := range RequiredClaimProducers(sender) {
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
							"(executor %q or its claim_producers: producers); the subscription registers but will only "+
							"fire if a peer emits this exact class", leaf, s.Node, sender.Executor),
					})
				}
			}
		}
		if isRealNode && tmpl != nil {
			senderIdx := declared[s.Node]
			sender := tmpl.Nodes[senderIdx]
			validateSubscriptionDeclaredTags(s, sbase, sender, hooks, res)
		}
	}
	type subKey struct {
		node string
		typ  string
		when string
	}
	type subEntry struct {
		idx     int
		refresh bool
	}
	groups := map[subKey][]subEntry{}
	for i, s := range n.Subscribes {
		if s.ForceUpstreamRefresh == nil {
			continue
		}
		k := subKey{
			node: s.Node,
			typ:  s.Type,
			when: s.When,
		}
		groups[k] = append(groups[k], subEntry{
			idx: i, refresh: *s.ForceUpstreamRefresh,
		})
	}
	groupKeys := make([]subKey, 0, len(groups))
	for k := range groups {
		groupKeys = append(groupKeys, k)
	}
	sort.Slice(groupKeys, func(i, j int) bool {
		return groups[groupKeys[i]][0].idx < groups[groupKeys[j]][0].idx
	})
	for _, k := range groupKeys {
		entries := groups[k]
		if len(entries) < 2 {
			continue
		}
		first := entries[0]
		for _, e := range entries[1:] {
			if e.refresh == first.refresh {
				continue
			}
			res.Errors = append(res.Errors, ValidationError{
				Path: fmt.Sprintf("%s.subscribes[%d]", base, e.idx),
				Msg: fmt.Sprintf(
					"conflicting cascade-shape flags for subscription key "+
						"(node:%q type:%q when:%q): entry %d has "+
						"force_upstream_refresh=%t but entry %d has "+
						"force_upstream_refresh=%t — a single subscription "+
						"key must declare one coherent cascade contract",
					k.node, k.typ, k.when,
					first.idx, first.refresh,
					e.idx, e.refresh),
			})
		}
	}
}

// @concept: attribute
// @concept: node-subscription
func substitutionRefPath(declared map[string]int, receiverType string, ref substitutionRef) string {
	origin := "attributes.schema"
	switch {
	case strings.HasPrefix(ref.AttributeProperty, "claim_producers["):
		origin = ref.AttributeProperty
	case strings.HasPrefix(ref.AttributeProperty, "locks["):
		origin = ref.AttributeProperty
	case ref.AttributeProperty == "fan_out.partition_request":
		origin = ref.AttributeProperty
	case ref.AttributeProperty != "":
		origin = "attributes.schema." + ref.AttributeProperty
	}
	if idx, ok := declared[receiverType]; ok {
		return fmt.Sprintf("nodes[%d].%s (substitution ref)", idx, origin)
	}
	return fmt.Sprintf("nodes[%s].%s (substitution ref)", receiverType, origin)
}

func sortedMessageRefKeys[T any](m map[string][]T) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func validateSubstitutionRefExistence(
	spec *TemplateSpec,
	declared map[string]int,
	hooks RegistryHooks,
	refs map[string][]substitutionRef,
	res *ValidationResult,
) {
	for _, receiverType := range sortedMessageRefKeys(refs) {
		list := refs[receiverType]
		for _, ref := range list {
			path := substitutionRefPath(declared, receiverType, ref)
			senderIdx, declaredOk := declared[ref.TypeName]
			if !declaredOk {
				res.Errors = append(res.Errors, ValidationError{
					Path: path,
					Msg:  fmt.Sprintf("substitution ref `nodes.%s.%s.%s` references unknown node %q", ref.TypeName, ref.TopicKind, ref.FieldPath, ref.TypeName),
				})
				continue
			}
			sender := spec.Nodes[senderIdx]
			switch ref.TopicKind {
			case "attribute":
				if ref.FieldPath == "" {
					continue
				}
				if !attributeKeyDeclared(sender, ref.FieldPath) {
					res.Errors = append(res.Errors, ValidationError{
						Path: path,
						Msg:  fmt.Sprintf("substitution ref `nodes.%s.attribute.%s` references an attribute key not declared on the sender", ref.TypeName, ref.FieldPath),
					})
				}
			}
		}
	}
}

// @concept: node-subscription
// @concept: attribute
// @decision: substitution-ref-coverage-required
// @decision: coverage-wildcard-asymmetry
// @decision: uncovered-substitution-error-shape
func validateSubstitutionRefCoverage(
	tmpl *TemplateSpec,
	refs map[string][]substitutionRef,
	messageRefsByReceiver map[string][]messageRef,
	res *ValidationResult,
) {
	if tmpl == nil {
		return
	}
	indexByReceiver := make(map[string]map[coverageEntryKey]struct{}, len(tmpl.Nodes))
	for _, n := range tmpl.Nodes {
		idx := map[coverageEntryKey]struct{}{}
		for _, s := range n.Subscribes {
			if s.Node == "" {
				continue
			}
			idx[coverageEntryKey{sender: s.Node, typ: s.Type}] = struct{}{}
		}
		indexByReceiver[n.Type] = idx
	}
	for _, receiverType := range sortedMessageRefKeys(refs) {
		list := refs[receiverType]
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
					"node":                   ref.TypeName,
					"type":                   suggestedType,
					"force_upstream_refresh": false,
				},
				"suggested_subscribes_note": fmt.Sprintf(
					"set force_upstream_refresh: true if %s should be re-evaluated when this receiver is invalidated",
					ref.TypeName,
				),
			})
		}
	}
	// @story: typed-message-substitution
	for _, receiverType := range sortedMessageRefKeys(messageRefsByReceiver) {
		list := messageRefsByReceiver[receiverType]
		idx := indexByReceiver[receiverType]
		for _, ref := range list {
			adapted := substitutionRef{
				Prefix:    "messages",
				TypeName:  ref.TypeName,
				TopicKind: "message",
			}
			suggestedType, covered := coverageMatch(idx, adapted)
			if covered {
				continue
			}
			refLiteral := "{{messages." + ref.TypeName
			if ref.FieldPath != "" {
				refLiteral += "." + ref.FieldPath
			}
			refLiteral += "}}"
			res.StructuredErrors = append(res.StructuredErrors, map[string]any{
				"kind":               "substitution_ref_uncovered",
				"receiver_node_type": receiverType,
				"ref":                refLiteral,
				"attribute_property": ref.AttributeProperty,
				"suggested_subscribes_entry": map[string]any{
					"node":                   ref.TypeName,
					"type":                   suggestedType,
					"force_upstream_refresh": false,
				},
				"suggested_subscribes_note": fmt.Sprintf(
					"set force_upstream_refresh: true if %s should be re-evaluated when this receiver is invalidated",
					ref.TypeName,
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
		if ref.FieldPath == "" {
			suggestedType = "attribute/*"
			if _, ok := idx[coverageEntryKey{sender: ref.TypeName, typ: "attribute/*"}]; ok {
				return suggestedType, true
			}
			return suggestedType, false
		}
		suggestedType = fmt.Sprintf("attribute/%s/changed", ref.FieldPath)
		if _, ok := idx[coverageEntryKey{sender: ref.TypeName, typ: suggestedType}]; ok {
			return suggestedType, true
		}
		if _, ok := idx[coverageEntryKey{sender: ref.TypeName, typ: "attribute/*"}]; ok {
			return suggestedType, true
		}
		return suggestedType, false
	case "message":
		suggestedType = "terminal/success"
		if _, ok := idx[coverageEntryKey{sender: ref.TypeName, typ: suggestedType}]; ok {
			return suggestedType, true
		}
		if _, ok := idx[coverageEntryKey{sender: ref.TypeName, typ: "terminal/*"}]; ok {
			return suggestedType, true
		}
		if _, ok := idx[coverageEntryKey{sender: ref.TypeName, typ: "*"}]; ok {
			return suggestedType, true
		}
		return suggestedType, false
	}
	return "", true
}

func rejectAuthorSetInternalFlags(spec *TemplateSpec, res *ValidationResult) {
	report := func(path string, n TemplateNodeDef) {
		if n.IsSubgraphEntryAbsorbed {
			res.Errors = append(res.Errors, ValidationError{
				Path: path + ".is_subgraph_entry_absorbed",
				Msg:  "is_subgraph_entry_absorbed is set by subgraph canonicalization and may not be declared by a template author",
			})
		}
		if n.IsSubgraphExit {
			res.Errors = append(res.Errors, ValidationError{
				Path: path + ".is_subgraph_exit",
				Msg:  "is_subgraph_exit is set by subgraph canonicalization and may not be declared by a template author",
			})
		}
		for si, s := range n.Subscribes {
			if s.ResolvesViaCallingNode {
				res.Errors = append(res.Errors, ValidationError{
					Path: fmt.Sprintf("%s.subscribes[%d].resolves_via_calling_node", path, si),
					Msg:  "resolves_via_calling_node is set by subgraph canonicalization and may not be declared by a template author",
				})
			}
		}
	}
	for i := range spec.Nodes {
		report(fmt.Sprintf("nodes[%d]", i), spec.Nodes[i])
	}
	for gi := range spec.Graphs {
		for ni := range spec.Graphs[gi].Nodes {
			report(fmt.Sprintf("graphs[%d].nodes[%d]", gi, ni), spec.Graphs[gi].Nodes[ni])
		}
	}
}

func validateExecutorCoherence(n TemplateNodeDef, base string, hooks RegistryHooks, res *ValidationResult) {
	hasExecutor := n.Executor != ""
	hasDelegate := n.Delegate != ""
	hasSendsMessage := n.SendsMessage != ""
	if hasExecutor && hasDelegate && !n.IsSubgraphEntryAbsorbed {
		res.Errors = append(res.Errors, ValidationError{
			Path: fmt.Sprintf("%s.delegate", base),
			Msg: fmt.Sprintf(
				"delegate and executor are mutually exclusive (executor=%q, delegate=%q)",
				n.Executor, n.Delegate),
		})
	}
	if hasExecutor && hasSendsMessage {
		res.Errors = append(res.Errors, ValidationError{
			Path: fmt.Sprintf("%s.sends_message", base),
			Msg: fmt.Sprintf(
				"sends_message and executor are mutually exclusive (executor=%q, sends_message=%q)",
				n.Executor, n.SendsMessage),
		})
	}
	if hasDelegate && hasSendsMessage {
		res.Errors = append(res.Errors, ValidationError{
			Path: fmt.Sprintf("%s.sends_message", base),
			Msg: fmt.Sprintf(
				"sends_message and delegate are mutually exclusive (delegate=%q, sends_message=%q)",
				n.Delegate, n.SendsMessage),
		})
	}
	if !hasExecutor && !hasDelegate && !hasSendsMessage && effectiveExecutor(n, hooks) == "" {
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
	if n.SendsMessage != "" {
		res.Errors = append(res.Errors, ValidationError{
			Path: base + ".kind",
			Msg: fmt.Sprintf(
				"node declares both kind and sends_message; a kind alias resolves to an executor, and executor is mutually exclusive with sends_message (kind=%q, sends_message=%q)",
				n.Kind, n.SendsMessage),
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
	if hooks.ExecutorDeclared(n.Executor) {
		return
	}
	res.Errors = append(res.Errors, ValidationError{
		Path: base + ".executor",
		Msg:  fmt.Sprintf("executor %q is not declared in the operator's executors: block", n.Executor),
	})
}

func validateClaimProducers(n TemplateNodeDef, base string, hooks RegistryHooks, res *ValidationResult) {
	seenAlias := make(map[string]int, len(n.ClaimProducers))
	for j, s := range n.ClaimProducers {
		sbase := fmt.Sprintf("%s.claim_producers[%d]", base, j)
		name := strings.TrimSpace(s.Name)
		if name == "" {
			res.Errors = append(res.Errors, ValidationError{
				Path: sbase + ".name", Msg: "store name is required",
			})
			continue
		}
		if hooks.StoreDeclared != nil && !hooks.StoreDeclared(name) {
			res.Errors = append(res.Errors, ValidationError{
				Path: sbase + ".name",
				Msg:  fmt.Sprintf("store %q is not declared in the operator's claim_producers: block", name),
			})
			continue
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
				Msg:  fmt.Sprintf("duplicate claim alias %q (already at claim_producers[%d])", alias, prev),
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
		// @concept: named-lock
		if hooks.NamedLockDeclared != nil && !strings.ContainsAny(name, "{") && !hooks.NamedLockDeclared(name) {
			res.Errors = append(res.Errors, ValidationError{
				Path: lbase + ".name",
				Msg:  fmt.Sprintf("named lock %q is not declared in the operator's named_locks: block", name),
			})
		}
	}
}

func validateCascadeMode(n TemplateNodeDef, base string, res *ValidationResult) {
	if n.CascadeMode == "" {
		return
	}
	switch cascade.CascadeMode(n.CascadeMode) {
	case cascade.CascadeModeMostRecent,
		cascade.CascadeModeSequenced,
		cascade.CascadeModeIdempotentQueue,
		cascade.CascadeModeIdempotentSettled:
		return
	}
	res.Errors = append(res.Errors, ValidationError{
		Path: base + ".cascade_mode",
		Msg: fmt.Sprintf(
			"unknown cascade_mode %q: must be one of %q, %q, %q, %q",
			n.CascadeMode,
			cascade.CascadeModeMostRecent,
			cascade.CascadeModeSequenced,
			cascade.CascadeModeIdempotentQueue,
			cascade.CascadeModeIdempotentSettled,
		),
	})
}

// @decision: three-dispatch-deadlines
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
