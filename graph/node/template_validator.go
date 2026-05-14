// Copyright © 2026 Fall Guy Consulting.
// Licensed under the Apache License, Version 2.0. See LICENSE.apache at the
// repo root, or http://www.apache.org/licenses/LICENSE-2.0.

package node

import (
	"bytes"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/robfig/cron/v3"
	"github.com/santhosh-tekuri/jsonschema/v5"
)

// ValidationError is a blocking problem with a template. Path locates
// the offending element using JSONPath-ish notation.
type ValidationError struct {
	Path string
	Msg  string
}

// ValidationWarning is a non-blocking problem.
type ValidationWarning struct {
	Path string
	Msg  string
}

// ValidationResult is returned by ValidateTemplate. Ok() is true when
// no errors were found (warnings are allowed).
type ValidationResult struct {
	Errors   []ValidationError
	Warnings []ValidationWarning
}

// Ok reports whether the template passed validation (no errors).
func (r ValidationResult) Ok() bool { return len(r.Errors) == 0 }

// instantiationPlaceholderRe matches `{params.<key>}` placeholders.
var instantiationPlaceholderRe = regexp.MustCompile(`\{params\.[a-zA-Z_][a-zA-Z0-9_]*\}`)

// anyBraceRe matches any single-`{...}` segment that isn't part of a
// double-`{{...}}` directive.
var anyBraceRe = regexp.MustCompile(`\{[^{}]*\}`)

// dispatchDirectiveRe matches `{{<inside>}}` directives.
var dispatchDirectiveRe = regexp.MustCompile(`\{\{([^{}]+)\}\}`)

// dispatchDirectiveRe / directiveBodyRe accept the three substitution
// kinds: `claim`, `params`, and `nodes` (the latter handles both
// attribute and event topic forms; see graph/attribute/substitution.go).
// The legacy `deps.X.Y` form retired post-2026-05-14.
//
// directiveBodyRe further parses the inside of `{{...}}` against the
// known source kinds.
var directiveBodyRe = regexp.MustCompile(`^(claim|params|nodes)\.(.+)$`)

// RegistryHooks bundles the registry-dependent lookups the validator
// uses. All fields may be nil; a nil hook short-circuits to "skip the
// corresponding check," which is useful for unit tests that don't wire
// a registry.
//
// Per the v3 stores-redesign, rimsky no longer recognises pick-policy
// selectors — the store is the only entity that does. The v2
// IsPickPolicySelector hook (and the "pick-policy claims must be intent:
// rw" check it drove) was deleted as part of the inertness cleanup.
type RegistryHooks struct {
	// StoreDeclared returns true when `name` is declared in the
	// operator's stores: block. Used by validateStores to reject
	// references to unknown stores.
	StoreDeclared func(name string) bool
	// NamedLockDeclared returns true when `name` is declared in the
	// operator's named_locks: block. Drives the "templates reference
	// named locks by name only" check.
	NamedLockDeclared func(name string) bool
	// ExecutorDeclared returns true when `name` is declared in the
	// operator's executors: block (rimsky.yml per docs/specs/2026-05-
	// 01-control-plane-and-store-lifecycle-design.md §3.1). Drives the
	// per-node executor-name check.
	ExecutorDeclared func(name string) bool

	// ExecutorDeclaredEvents returns the set of event names the named
	// executor advertises via ObservabilityCapabilities.declared_events
	// (plan A1 / F6). Used to reject templates whose on_event handler
	// names an event the executor does not declare. nil → skip the
	// check (e.g. tests that don't wire an observability cache).
	ExecutorDeclaredEvents func(name string) ([]string, bool)

	// ExecutorUserdataSchema returns the JSON Schema bytes the named
	// executor advertises via ObservabilityCapabilities.userdata_schema
	// (plan A1 / F7). Empty bytes mean "no schema; accept any
	// userdata." nil → skip the check.
	ExecutorUserdataSchema func(name string) ([]byte, bool)
}

// ValidateTemplate walks a parsed template and reports errors per spec
// §18. hooks supplies registry-dependent lookups; pass an empty
// RegistryHooks to skip them.
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
	validateFrameResolution(spec, &res)
	if len(spec.Nodes) == 0 {
		res.Errors = append(res.Errors, ValidationError{Path: "nodes", Msg: "template must declare at least one node"})
		return res
	}

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

	cronParser := cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow)

	for i, n := range spec.Nodes {
		base := fmt.Sprintf("nodes[%d]", i)
		validateSubscribes(n, base, declared, hooks, spec, &res)
		validateErrorTypes(n, base, declared, &res)
		validateSchedule(n, base, cronParser, &res)
		validateExecutorCoherence(n, base, &res)
		validateExecutorDeclared(n, base, hooks, &res)
		validateStores(n, base, hooks, &res)
		validateLocks(n, base, hooks, &res)
		validateAttributesSchema(n, base, declared, &res)
		validateOnAcquireUnavailable(n, base, &res)
		validateOnExecutorComplete(n, base, &res)
		validateOnExecutorTerminal(n, n.OnExecutorErrored, base+".on_executor_errored", &res)
		validateMaxParkDuration(n, base, &res)
		validateUserdataAgainstSchema(n, base, hooks, &res)
	}

	// Post-pass: substitution-ref cross-check (T17). Iterate all
	// `{{nodes.<X>.<kind>.<name>}}` directives in the attribute
	// schemas and confirm the sender + attribute/event name are
	// defined on the upstream node.
	refs := ExtractSubstitutionRefsFromTemplate(*spec)
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
			case "event":
				if hooks.ExecutorDeclaredEvents != nil && sender.Executor != "" {
					if names, ok := hooks.ExecutorDeclaredEvents(sender.Executor); ok {
						found := false
						for _, name := range names {
							if name == ref.Name {
								found = true
								break
							}
						}
						if !found {
							res.Errors = append(res.Errors, ValidationError{
								Path: path,
								Msg:  fmt.Sprintf("substitution ref `nodes.%s.event.%s` references an event not declared by executor %q", ref.SenderNodeType, ref.Name, sender.Executor),
							})
						}
					}
					// Silent skip when executor capabilities are not visible.
				}
			}
		}
	}

	ValidateInheritance(spec, &res)
	return res
}

// attributeKeyDeclared reports whether `key` appears under
// sender.Attributes.Schema.properties. Pure-cascade senders or senders
// without an attribute schema return false (no attribute to read).
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

// validateFrameResolution enforces the frame-resolution template
// requirements: frame_resolution required, one of coalesce|serial_queue;
// frame_timeout_ms ≥ 60000 when set.
func validateFrameResolution(spec *TemplateSpec, res *ValidationResult) {
	switch spec.FrameResolutionMode {
	case FrameResolutionCoalesce, FrameResolutionSerialQueue:
	case "":
		res.Errors = append(res.Errors, ValidationError{
			Path: "frame_resolution_mode",
			Msg: fmt.Sprintf("frame_resolution is required (one of: %q, %q)",
				FrameResolutionCoalesce, FrameResolutionSerialQueue),
		})
	default:
		res.Errors = append(res.Errors, ValidationError{
			Path: "frame_resolution_mode",
			Msg: fmt.Sprintf("frame_resolution = %q is not a valid value (one of: %q, %q)",
				spec.FrameResolutionMode, FrameResolutionCoalesce, FrameResolutionSerialQueue),
		})
	}

	if spec.FrameTimeoutMs != 0 && spec.FrameTimeoutMs < FrameTimeoutMinMs {
		res.Errors = append(res.Errors, ValidationError{
			Path: "frame_timeout_ms",
			Msg: fmt.Sprintf("frame_timeout_ms = %d is below hard floor %d",
				spec.FrameTimeoutMs, FrameTimeoutMinMs),
		})
	}
}

// ApplyFrameResolutionDefaults fills FrameTimeoutMs with the spec's
// default (FrameTimeoutDefaultMs) when zero.
func ApplyFrameResolutionDefaults(spec *TemplateSpec) {
	if spec == nil {
		return
	}
	if spec.FrameTimeoutMs == 0 {
		spec.FrameTimeoutMs = FrameTimeoutDefaultMs
	}
}

func validateErrorTypes(n TemplateNodeDef, base string, _ map[string]int, res *ValidationResult) {
	for className, policy := range n.ErrorTypes {
		for ai, action := range policy.Policy {
			if action.Action == "invalidate" {
				res.Errors = append(res.Errors, ValidationError{
					Path: fmt.Sprintf("%s.error_types[%s].policy[%d].action", base, className, ai),
					Msg:  "`action: invalidate` retired; receivers declare cascade coupling via `subscribes: [{node: <sender>, on: state, when: failed, error_class: <class>}]`. See spec .ok-planner/specs/2026-05-14-subscription-cascade-and-quality-of-life-design.md Piece 1 migration table.",
				})
				continue
			}
		}
	}
}

// validateOnAcquireUnavailable enforces the resolve vocabulary
// (pass | retry | error) and the error_class requirement when
// resolve=error. The invalidate-emit slot retired post-2026-05-14.
func validateOnAcquireUnavailable(n TemplateNodeDef, base string, res *ValidationResult) {
	h := n.OnAcquireUnavailable
	if h == nil {
		return
	}
	hbase := base + ".on_acquire_unavailable"
	if h.Resolve == "" {
		res.Errors = append(res.Errors, ValidationError{
			Path: hbase,
			Msg:  "handler is empty (resolve is required)",
		})
		return
	}
	switch h.Resolve {
	case ResolvePass, ResolveRetry:
	case ResolveError:
		if strings.TrimSpace(h.ErrorClass) == "" {
			res.Errors = append(res.Errors, ValidationError{
				Path: hbase + ".error_class",
				Msg:  "error_class is required when resolve = error",
			})
		} else if _, declaredClass := n.ErrorTypes[h.ErrorClass]; !declaredClass && !isBuiltinErrorClass(h.ErrorClass) {
			res.Errors = append(res.Errors, ValidationError{
				Path: hbase + ".error_class",
				Msg:  fmt.Sprintf("error_class %q does not match any error_types[...] key on this node", h.ErrorClass),
			})
		}
	default:
		res.Errors = append(res.Errors, ValidationError{
			Path: hbase + ".resolve",
			Msg:  fmt.Sprintf("resolve = %q is not valid (one of: %q, %q, %q)", h.Resolve, ResolvePass, ResolveRetry, ResolveError),
		})
	}
}

// validateOnExecutorComplete enforces the resolve vocabulary
// (by_changed | always_propagate | never_propagate). The invalidate-
// emit slot retired post-2026-05-14.
func validateOnExecutorComplete(n TemplateNodeDef, base string, res *ValidationResult) {
	h := n.OnExecutorComplete
	if h == nil {
		return
	}
	hbase := base + ".on_executor_complete"
	if h.Resolve == "" {
		res.Errors = append(res.Errors, ValidationError{
			Path: hbase,
			Msg:  "handler is empty (resolve is required)",
		})
		return
	}
	switch h.Resolve {
	case ResolveByChanged, ResolveAlwaysPropagate, ResolveNeverPropagate:
	default:
		res.Errors = append(res.Errors, ValidationError{
			Path: hbase + ".resolve",
			Msg:  fmt.Sprintf("resolve = %q is not valid (one of: %q, %q, %q)", h.Resolve, ResolveByChanged, ResolveAlwaysPropagate, ResolveNeverPropagate),
		})
	}
}

// validateOnExecutorTerminal enforces the resolve vocabulary
// (error | pass) for on_executor_errored, plus the error_class
// requirement when resolve=error. The invalidate-emit slot retired
// post-2026-05-14.
func validateOnExecutorTerminal(n TemplateNodeDef, h *OnExecutorTerminalHandler, hbase string, res *ValidationResult) {
	if h == nil {
		return
	}
	if h.Resolve == "" {
		res.Errors = append(res.Errors, ValidationError{
			Path: hbase,
			Msg:  "handler is empty (resolve is required)",
		})
		return
	}
	switch h.Resolve {
	case ResolvePass:
	case ResolveError:
		if strings.TrimSpace(h.ErrorClass) == "" {
			res.Errors = append(res.Errors, ValidationError{
				Path: hbase + ".error_class",
				Msg:  "error_class is required when resolve = error",
			})
		} else if _, declaredClass := n.ErrorTypes[h.ErrorClass]; !declaredClass && !isBuiltinErrorClass(h.ErrorClass) {
			res.Errors = append(res.Errors, ValidationError{
				Path: hbase + ".error_class",
				Msg:  fmt.Sprintf("error_class %q does not match any error_types[...] key on this node", h.ErrorClass),
			})
		}
	default:
		res.Errors = append(res.Errors, ValidationError{
			Path: hbase + ".resolve",
			Msg:  fmt.Sprintf("resolve = %q is not valid (one of: %q, %q)", h.Resolve, ResolveError, ResolvePass),
		})
	}
}

// validateSubscribes is T16: enforces SubscriptionEntry shape rules.
//
//	@concept: subscription
func validateSubscribes(n TemplateNodeDef, base string, declared map[string]int, hooks RegistryHooks, tmpl *TemplateSpec, res *ValidationResult) {
	for i, s := range n.Subscribes {
		sbase := fmt.Sprintf("%s.subscribes[%d]", base, i)
		// Mutual exclusion: Node and Instance.
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
		// `node:` must reference a declared node-type.
		if s.Node != "" {
			if _, ok := declared[s.Node]; !ok {
				res.Errors = append(res.Errors, ValidationError{
					Path: sbase + ".node",
					Msg:  fmt.Sprintf("subscription `node: %q` does not reference a declared node", s.Node),
				})
				continue
			}
		}
		// `on:` is required and must be a known topic kind.
		switch s.On {
		case "state", "attribute", "event":
		default:
			res.Errors = append(res.Errors, ValidationError{
				Path: sbase + ".on",
				Msg:  fmt.Sprintf("`on:` must be one of \"state\" | \"attribute\" | \"event\", got %q", s.On),
			})
			continue
		}
		// On == "event" requires `name:`.
		if s.On == "event" && s.Name == "" {
			res.Errors = append(res.Errors, ValidationError{
				Path: sbase + ".name",
				Msg:  "`on: event` requires `name:`",
			})
		}
		// On != "state" forbids state-only filters.
		if s.On != "state" {
			if s.When != "" || s.Outcome != "" || s.ErrorClass != "" || s.Reason != "" {
				res.Errors = append(res.Errors, ValidationError{
					Path: sbase,
					Msg:  "state-only filters (when/outcome/error_class/reason) require `on: state`",
				})
			}
		}
		// `when:` must be a valid node state.
		if s.When != "" {
			switch s.When {
			case "fresh", "stale", "running", "failed", "parked":
			default:
				res.Errors = append(res.Errors, ValidationError{
					Path: sbase + ".when",
					Msg:  fmt.Sprintf("`when: %q` is not a valid node state", s.When),
				})
			}
		}
		// `frame:` must be in | next | "".
		switch s.Frame {
		case "", FrameIn, FrameNext:
		default:
			res.Errors = append(res.Errors, ValidationError{
				Path: sbase + ".frame",
				Msg:  fmt.Sprintf("`frame:` must be empty | %q | %q, got %q", FrameIn, FrameNext, s.Frame),
			})
		}
		// Cross-check `on: event` `name:` against the sender's executor's
		// declared_events (silent-skip when unreachable).
		if s.Node != "" && s.On == "event" && hooks.ExecutorDeclaredEvents != nil && tmpl != nil {
			senderIdx := declared[s.Node]
			sender := tmpl.Nodes[senderIdx]
			if sender.Executor != "" {
				if names, ok := hooks.ExecutorDeclaredEvents(sender.Executor); ok {
					found := false
					for _, name := range names {
						if name == s.Name {
							found = true
							break
						}
					}
					if !found {
						res.Errors = append(res.Errors, ValidationError{
							Path: sbase + ".name",
							Msg:  fmt.Sprintf("event %q not declared by sender %q's executor %q", s.Name, s.Node, sender.Executor),
						})
					}
				}
			}
		}
	}
}

// isBuiltinErrorClass returns true for the error classes rimsky raises
// itself (vs. those declared in the template). Used to permit
// resolve=error to point at a built-in class like
// "template_resolution_failed" without requiring it to be redeclared
// in error_types: on the node.
func isBuiltinErrorClass(name string) bool {
	switch name {
	case "template_resolution_failed",
		"attributes_schema_failed",
		"quality_rule_failed",
		"executor_blocked",
		"executor_errored",
		"acquire_unavailable":
		return true
	}
	return false
}

func validateSchedule(n TemplateNodeDef, base string, parser cron.Parser, res *ValidationResult) {
	if n.Schedule == "" {
		return
	}
	if _, err := parser.Parse(n.Schedule); err != nil {
		res.Errors = append(res.Errors, ValidationError{
			Path: fmt.Sprintf("%s.schedule", base),
			Msg:  fmt.Sprintf("invalid cron expression %q: %v", n.Schedule, err),
		})
	}
}

func validateExecutorCoherence(n TemplateNodeDef, base string, res *ValidationResult) {
	if n.Executor != "" {
		return
	}
	if len(n.Userdata) > 0 {
		res.Warnings = append(res.Warnings, ValidationWarning{
			Path: fmt.Sprintf("%s.userdata", base),
			Msg:  "pure-cascade node has userdata; userdata is only consumed by executors",
		})
	}
}

// validateExecutorDeclared rejects nodes that reference an executor not
// declared in the operator's rimsky.yml executors block. No-op when
// the node has no executor (pure-cascade) or when the hook is not
// supplied (unit tests that don't wire a registry).
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

// validateStores enforces the per-node store-usage rules from spec §18:
//   - Each store name must resolve via storeKindOf (when supplied).
//   - Aliases are unique within a node.
//   - Intent must be "r" or "rw".
//   - Selectors may carry {{...}} directives; this pass is grammar-only.
//   - {{params.x}} placeholders inside selectors are accepted.
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
		if hooks.StoreDeclared != nil {
			if !hooks.StoreDeclared(name) {
				res.Errors = append(res.Errors, ValidationError{
					Path: sbase + ".name",
					Msg:  fmt.Sprintf("unknown store %q", name),
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
	}
}

// validateLocks enforces the named-lock declarations. Limit lives in
// operator config (named_locks: block); the template only references
// the lock by name. Validator checks for non-empty name and uniqueness
// within a node, plus (when the registry hook is supplied) that every
// referenced name is declared in the operator's named_locks: block.
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
		if hooks.NamedLockDeclared != nil {
			// Skip the check when the name carries an unresolved
			// substitution placeholder (e.g. "model-{params.tier}");
			// the resolved name is unknown until dispatch.
			if !strings.ContainsAny(name, "{") && !hooks.NamedLockDeclared(name) {
				res.Errors = append(res.Errors, ValidationError{
					Path: lbase + ".name",
					Msg:  fmt.Sprintf("named lock %q is not declared in the operator's named_locks: block", name),
				})
			}
		}
	}
}

// validateAttributesSchema parses the JSON Schema and checks that
// every `source:` directive in `properties[*].source` is syntactically
// valid: a single `{{...}}` body matching deps/claim/params shapes.
// Referenced upstream node names must exist in the template;
// referenced claim aliases must be acquired by this node OR present
// via inherits: declarations (the latter is checked alongside the
// holding-subgraph computation in ValidateInheritance).
func validateAttributesSchema(n TemplateNodeDef, base string, declared map[string]int, res *ValidationResult) {
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

	// Aliases this node directly acquires.
	directAliases := make(map[string]struct{}, len(n.Stores))
	for _, s := range n.Stores {
		directAliases[s.AliasOf()] = struct{}{}
	}
	// Aliases this node inherits.
	inheritedAliases := make(map[string]struct{}, len(n.Inherits))
	for _, ie := range n.Inherits {
		if ie.Claim != "" {
			inheritedAliases[ie.Claim] = struct{}{}
		}
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
		srcRaw, ok := propMap["source"]
		if !ok {
			continue
		}
		src, ok := srcRaw.(string)
		if !ok {
			res.Errors = append(res.Errors, ValidationError{
				Path: fmt.Sprintf("%s.properties.%s.source", sbase, fname),
				Msg:  "source must be a string",
			})
			continue
		}
		checkAttributeSource(src, fmt.Sprintf("%s.properties.%s.source", sbase, fname), declared, directAliases, inheritedAliases, res)
	}
}

// checkAttributeSource enforces directive syntax + reference validity
// for a single `source:` value. Per spec §16 the value must be exactly
// one `{{...}}` directive (no surrounding text and no multiple
// directives).
func checkAttributeSource(src, path string, declared map[string]int, directAliases, inheritedAliases map[string]struct{}, res *ValidationResult) {
	trimmed := strings.TrimSpace(src)
	if trimmed == "" {
		res.Errors = append(res.Errors, ValidationError{
			Path: path, Msg: "source is empty",
		})
		return
	}
	matches := dispatchDirectiveRe.FindAllStringSubmatchIndex(trimmed, -1)
	if len(matches) != 1 {
		res.Errors = append(res.Errors, ValidationError{
			Path: path,
			Msg:  fmt.Sprintf("source must be exactly one {{...}} directive, got %q", trimmed),
		})
		return
	}
	m := matches[0]
	if m[0] != 0 || m[1] != len(trimmed) {
		res.Errors = append(res.Errors, ValidationError{
			Path: path,
			Msg:  fmt.Sprintf("source must be exactly one {{...}} directive with no surrounding text, got %q", trimmed),
		})
		return
	}
	body := strings.TrimSpace(trimmed[m[2]:m[3]])
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
		// Valid forms: claim.<alias>.address, claim.<alias>.scope,
		// claim.<alias>.payload.<field-path>.
		if len(parts) < 2 || parts[0] == "" {
			res.Errors = append(res.Errors, ValidationError{
				Path: path,
				Msg:  fmt.Sprintf("claim directive %q must be claim.<alias>.{address|scope|payload.<field>}", body),
			})
			return
		}
		alias := parts[0]
		switch parts[1] {
		case "address", "scope":
			if len(parts) != 2 {
				res.Errors = append(res.Errors, ValidationError{
					Path: path,
					Msg:  fmt.Sprintf("claim.<alias>.%s takes no further field path", parts[1]),
				})
			}
		case "payload":
			if len(parts) < 3 || parts[2] == "" {
				res.Errors = append(res.Errors, ValidationError{
					Path: path,
					Msg:  fmt.Sprintf("claim directive %q must be claim.<alias>.payload.<field>", body),
				})
			}
		default:
			res.Errors = append(res.Errors, ValidationError{
				Path: path,
				Msg:  fmt.Sprintf("claim directive %q second segment must be address|scope|payload", body),
			})
		}
		// Alias must be acquired here OR inherited.
		if _, isOwn := directAliases[alias]; !isOwn {
			if _, isInherited := inheritedAliases[alias]; !isInherited {
				res.Errors = append(res.Errors, ValidationError{
					Path: path,
					Msg:  fmt.Sprintf("claim directive references alias %q which is neither acquired here nor declared in inherits:", alias),
				})
			}
		}
	case "params":
		if len(parts) < 1 || parts[0] == "" {
			res.Errors = append(res.Errors, ValidationError{
				Path: path,
				Msg:  fmt.Sprintf("params directive %q must be params.<key>", body),
			})
		}
	case "nodes":
		// Post-2026-05-14 substitution forms:
		//   nodes.<node>.attribute.<field>...      (replaces deps.X.Y)
		//   nodes.<emitter>.event.<event_name>.<json-path>
		if len(parts) < 3 || parts[0] == "" || parts[2] == "" {
			res.Errors = append(res.Errors, ValidationError{
				Path: path,
				Msg:  fmt.Sprintf("nodes directive %q must be nodes.<node>.{attribute|event}.<...>", body),
			})
			return
		}
		switch parts[1] {
		case "attribute":
			// nodes.<node>.attribute.<field>...
			if _, ok := declared[parts[0]]; !ok {
				res.Errors = append(res.Errors, ValidationError{
					Path: path,
					Msg:  fmt.Sprintf("nodes directive references unknown node %q", parts[0]),
				})
			}
		case "event":
			if len(parts) < 4 {
				res.Errors = append(res.Errors, ValidationError{
					Path: path,
					Msg:  fmt.Sprintf("nodes directive %q must be nodes.<node>.event.<name>.<path>", body),
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
				Msg:  fmt.Sprintf("nodes directive %q second segment must be 'attribute' or 'event'", body),
			})
		}
	default:
		res.Errors = append(res.Errors, ValidationError{
			Path: path,
			Msg:  fmt.Sprintf("unknown directive kind %q", kind),
		})
	}
}

// checkScopeDirectives spot-checks a scope pattern. Scope patterns
// may contain dispatch-time `{{...}}` directives and instantiation-time
// `{params.x}` placeholders. Stray single-brace tokens that aren't
// `{params.x}` are flagged as malformed.
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

// checkDispatchDirectives validates every `{{...}}` body in s against
// the substitution grammar. Resolution is dispatch-time; this pass is
// grammar-only.
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
				Msg:  fmt.Sprintf("invalid directive %q (expected claim.<a>.{address|scope|payload.<f>}, params.<k>, nodes.<n>.attribute.<f>, or nodes.<n>.event.<name>.<path>)", body),
			})
		}
	}
}

// (detectCycles + validateOnEvent removed by the 2026-05-14
// subscription-cascade resolution: cycles are now a runtime concern
// driven by the deferred `frame: next` queue, and `on_event:` is
// retired in favor of receiver-side subscriptions.)

// validateMaxParkDuration parses MaxParkDuration via time.ParseDuration
// and rejects malformed values. Empty string is valid (= "use deployment
// default").
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

// validateUserdataAgainstSchema runs the executor's advertised userdata
// schema (if any) against the template-level userdata bytes. Plan F7
// (registration-time gate). Dispatch-time re-validation lives in the
// foundation runner; this gate catches schema violations baked into the
// template even when no per-instance overrides are applied.
//
// Skipped silently when:
//   - The node has no executor.
//   - Hooks.ExecutorUserdataSchema is nil (e.g. unit tests).
//   - The advertised schema is empty.
//   - The node has no userdata.
func validateUserdataAgainstSchema(n TemplateNodeDef, base string, hooks RegistryHooks, res *ValidationResult) {
	if n.Executor == "" || hooks.ExecutorUserdataSchema == nil {
		return
	}
	schemaBytes, ok := hooks.ExecutorUserdataSchema(n.Executor)
	if !ok || len(schemaBytes) == 0 {
		return
	}
	// Empty userdata is equivalent to {} — cheap to validate; catches
	// schemas with required: [...]. We always run validation rather
	// than short-circuiting on len(Userdata)==0 because a missing
	// required field is a real schema violation that should surface
	// at registration.
	compiler := jsonschema.NewCompiler()
	if err := compiler.AddResource("inline://schema.json", bytes.NewReader(schemaBytes)); err != nil {
		res.Errors = append(res.Errors, ValidationError{
			Path: base + ".userdata",
			Msg:  fmt.Sprintf("executor %q advertises invalid userdata_schema: %v", n.Executor, err),
		})
		return
	}
	schema, err := compiler.Compile("inline://schema.json")
	if err != nil {
		res.Errors = append(res.Errors, ValidationError{
			Path: base + ".userdata",
			Msg:  fmt.Sprintf("executor %q userdata_schema does not compile: %v", n.Executor, err),
		})
		return
	}
	// Convert n.Userdata (map[string]any) into a JSON-decoded value.
	udBytes, err := json.Marshal(n.Userdata)
	if err != nil {
		res.Errors = append(res.Errors, ValidationError{
			Path: base + ".userdata",
			Msg:  fmt.Sprintf("marshal userdata: %v", err),
		})
		return
	}
	var doc any
	if err := json.Unmarshal(udBytes, &doc); err != nil {
		res.Errors = append(res.Errors, ValidationError{
			Path: base + ".userdata",
			Msg:  fmt.Sprintf("decode userdata: %v", err),
		})
		return
	}
	if err := schema.Validate(doc); err != nil {
		res.Errors = append(res.Errors, ValidationError{
			Path: base + ".userdata",
			Msg:  fmt.Sprintf("userdata fails executor %q schema: %v", n.Executor, err),
		})
	}
}

// parseDurationStrict wraps time.ParseDuration. Wrapped to keep the
// validator's call-sites uniform with other "parse and report" helpers.
func parseDurationStrict(s string) (time.Duration, error) {
	return time.ParseDuration(s)
}
