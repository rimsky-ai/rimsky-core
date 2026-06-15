// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package node

import (
	"bytes"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/santhosh-tekuri/jsonschema/v5"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/signal"
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

// @deliberate: dispatchDirectiveRe / directiveBodyRe accept the five substitution
// kinds: `claim`, `params`, `nodes`, `trigger`, and `child`. The
// legacy `deps.X.Y` form retired post-2026-05-14. The `trigger` and
// `child` kinds were added by spec §E14 (data-platform-extensions).
//
// directiveBodyRe further parses the inside of `{{...}}` against the
// known source kinds.
var directiveBodyRe = regexp.MustCompile(`^(claim|params|nodes|trigger|child)\.(.+)$`)

// RefValidationMode is the operator-set, startup-level mode governing
// ALL registration-time reference validation across service types
// (executors, stores/claim-producers, named locks, executor schemas).
// It is set once by the operator, not per-template, and decides whether
// a reference whose target cannot be validated at registration is a hard
// error, a silent skip, or whether reference validation runs at all.
//
// The zero value is RefValidateAll (strict): a RegistryHooks left
// without an explicit mode validates every reference and hard-fails on
// any it cannot validate. This keeps unit tests and any caller that
// forgets to set the mode on the strictest, safest footing.
//
// Whatever a relaxed mode (available/none) skips at registration is not
// skipped forever — it is mandatory at instantiation (concept:instance),
// where all referenced services exist.
//
//	@concept: template
type RefValidationMode int

const (
	// RefValidateAll (default, zero value) validates every referenced
	// service and hard-fails registration if any reference cannot be
	// validated — including a not-yet-provisioned executor/store/lock
	// (declared=false) and an executor whose expected_attributes_schema
	// is not visible (the readOnly leg becomes a hard error instead of a
	// silent skip).
	RefValidateAll RefValidationMode = iota
	// @deliberate: RefValidateAvailable validates references to
	// provisioned services (declared=true / schema visible) and skips
	// references whose target is not yet provisioned. A
	// genuinely-invalid reference to a PROVISIONED service is still
	// rejected; this mode is the always-on soft-fail behaviour made
	// explicit and uniform across the executor / store / lock / schema
	// legs.
	RefValidateAvailable
	// @deliberate: RefValidateNone performs no registration-time
	// reference validation at all: the four reference legs
	// (executor-declared, store-declared, lock-declared, executor-schema)
	// are skipped entirely regardless of provisioning state.
	RefValidateNone
)

// String returns the operator-facing spelling of the mode — the same
// tokens accepted by the templates.ref_validation_mode config key
// (all / available / none).
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

// refValidationModeRejection builds the self-documenting message every
// reference-validation failure carries: it states the failing
// reference (refDesc), names the active mode, states that the mode is
// what made the unvalidatable reference fatal, and names the
// templates.ref_validation_mode config key with its relaxed settings —
// so the register-before-provision workflow is discoverable from the
// error message itself. All four reference legs (executor-declared,
// store-declared, named-lock-declared, executor-schema) share this
// builder so the four messages stay consistent.
func refValidationModeRejection(refDesc string, mode RefValidationMode) string {
	return fmt.Sprintf(
		"%s; reference validation failed under mode %q — mode %q makes a reference that "+
			"cannot be validated at registration fatal; for register-before-provision workflows "+
			"set templates.ref_validation_mode to \"available\" (skip not-yet-provisioned "+
			"references) or \"none\" (skip registration-time reference validation entirely)",
		refDesc, mode, mode)
}

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
	// RefValidationMode is the operator-set registration-time reference-
	// validation mode (all / available / none). Zero value =
	// RefValidateAll (strict). Governs validateExecutorDeclared,
	// validateStores' StoreDeclared leg, validateLocks' NamedLockDeclared
	// leg, and the executor-schema legs in validateAttributesSchema.
	RefValidationMode RefValidationMode

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

	// ExecutorDeclaredErrorClasses returns the set of error-class paths
	// the named executor advertises via
	// ObservabilityCapabilities.declared_error_classes. Mirrors
	// ExecutorDeclaredEvents. Used by validateErrorTypes' vocabulary
	// union and by the validator's range-check of terminal/error/*
	// subscriptions against the sender's executor. nil → the executor
	// contributes no vocabulary and the subscription check is skipped.
	//
	//	@concept: signal
	ExecutorDeclaredErrorClasses func(name string) ([]string, bool)

	// StoreDeclaredErrorClasses returns the set of error-class paths
	// the named claim producer advertises via
	// claim_producer.proto::CapabilitiesResponse.declared_error_classes
	// (captured by the startup capabilities handshake). Used by
	// validateErrorTypes: an `error_types:` key is attributable when it
	// matches the executor's declared classes, the `acquire/*` synthetic
	// family, or the declared classes of any producer reachable from the
	// node's `stores:` block. nil → producers contribute no vocabulary.
	//
	//	@concept: signal
	//	@concept: error-policy
	StoreDeclaredErrorClasses func(name string) ([]string, bool)

	// ExecutorExpectedAttributesSchema returns the JSON Schema bytes the
	// named executor advertises via
	// ObservabilityCapabilities.expected_attributes_schema. Empty bytes
	// mean "no schema; accept any attributes." nil → skip the check.
	//
	// Used by checkAttributesSchema to (a) merge into the per-node
	// effective attribute schema at registration and (b) recognise
	// properties the executor marks `readOnly: true` (executor-write-
	// back populates at commit; template need not declare a source or
	// default).
	ExecutorExpectedAttributesSchema func(name string) ([]byte, bool)

	// StoreAdvertisesDataProcessing returns true when the named store's
	// Capabilities.Protocols includes "data_processing". Used to gate
	// `claims: lifetime: durable` per spec §Lifetime and the asset
	// pattern (the asset pattern requires DataProcessing-capable
	// producers). nil → skip the check.
	StoreAdvertisesDataProcessing func(name string) bool

	// StoreAdvertisesSplitScope returns true when the named store's
	// Capabilities.SupportsSplitScope is set. Used to gate
	// `fan_out:` declarations per spec §Fan-out template DSL.
	// nil → skip the check.
	StoreAdvertisesSplitScope func(name string) bool
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

	// @deliberate: D1 — canonicalize nested `graphs:` shape into flat
	// Nodes for the downstream per-node validation. Pre-v1 accepts both
	// shapes; the canonicalizer rejects templates that mix them.
	canonicalizeGraphs(spec, &res)

	if len(spec.Nodes) == 0 {
		res.Errors = append(res.Errors, ValidationError{Path: "nodes", Msg: "template must declare at least one node"})
		return res
	}

	// @deliberate: Template-author defaults validation runs after
	// canonicalization so it sees the flattened Nodes list and can
	// cross-check each `defaults.attributes.by_executor.<name>` routing
	// key against the template's actual executor names. Per the
	// structural-inertness discipline (concept:inertness), only routing
	// keys are inspected; fragment values are never read.
	validateDefaults(spec, &res)

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

	for i, n := range spec.Nodes {
		base := fmt.Sprintf("nodes[%d]", i)
		validateSubscribes(n, base, declared, hooks, spec, &res)
		validateErrorTypes(n, base, declared, hooks, &res)
		validateExecutorCoherence(n, base, &res)
		validateExecutorDeclared(n, base, hooks, &res)
		validateStores(n, base, hooks, &res)
		validateLocks(n, base, hooks, &res)
		validateAttributesSchema(n, base, declared, spec, hooks, &res)
		validateAcquireUnavailablePolicyAdvised(n, base, &res)
		validateMaxParkDuration(n, base, &res)
		validateHolds(n, base, spec, declared, &res)
		validateFanOut(n, base, hooks, &res)
		// @deliberate: operator-facing tags admit only
		// `{{params.<key>}}` substitution at materialization time.
		validateTagsAtRegistration(n, base, spec, &res)
	}

	// @deliberate: Publishers block validation. The persisted column
	// is `target_node TEXT NOT NULL` with no empty-string sentinel, so
	// reject empty entries here rather than surfacing a pgx NOT NULL
	// violation at instance-create time.
	validatePublishers(spec, declared, &res)

	// @deliberate: post-pass substitution-ref cross-check — iterate
	// every `{{nodes.<X>.<kind>.<name>}}` directive in the attribute
	// schemas and confirm the sender + attribute/event name are defined
	// on the upstream node.
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
					// @deliberate: silent skip when executor capabilities
					// are not visible.
				}
			}
		}
	}

	// @deliberate: Hard-dep cycle detection. The cascade walker assumes the hard-dep
	// assumes the hard-dep edge graph is acyclic; surface cycles at
	// registration so they cannot reach runtime.
	if _, err := BuildHardDepEdges(*spec); err != nil {
		res.Errors = append(res.Errors, ValidationError{
			Path: "graphs",
			Msg:  err.Error(),
		})
	}

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

// validateDefaults inspects only the routing keys under
// `spec.Defaults.Attributes.ByExecutor`, rejecting entries whose executor
// name does not match any node's `Executor`. The fragment values are
// never inspected (preserves the structural-inertness discipline for
// attribute values; concept:inertness). Per spec
// @concept: attribute
func validateDefaults(spec *TemplateSpec, res *ValidationResult) {
	if spec.Defaults == nil || spec.Defaults.Attributes == nil {
		return
	}
	if len(spec.Defaults.Attributes.ByExecutor) == 0 {
		return
	}
	known := make(map[string]struct{}, len(spec.Nodes))
	for _, n := range spec.Nodes {
		if n.Executor != "" {
			known[n.Executor] = struct{}{}
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

// validateErrorTypes range-checks every policy action against the
// canonical 4-value vocabulary (`pass | give_up | retry |
// discard_claims_then_retry`). The pre-2026-05-23 vocabulary
// (`invalidate`, `discard_then_retry`, `resume_then_retry`) all reject
// through the generic check with the new error message. Per
// `spec:2026-05-23-signal-taxonomy-and-policy-decoupling-design`.
//
// Also range-checks each error-class key against the union of the
// declared vocabularies a key may legitimately come from:
//
//   - the node's executor's declared_error_classes
//     (executor_observability.proto),
//   - the reserved synthetic `acquire/*` family and the other
//     runtime-synthesized classes (they originate rimsky-side),
//   - the declared_error_classes of every claim producer reachable
//     from the node's `stores:` block (claim_producer.proto) — the
//     runtime routes producer-classified acquisition failures by
//     these, so the validator must accept what the runtime routes.
//
// A key attributable to no declared vocabulary is an advisory WARNING
// (res.Warnings), never a hard rejection: peers MAY declare nothing,
// and an undeclared vocabulary must not lock operators out of routing
// classes the system itself emits. Silent-skip only when no vocabulary
// information is available at all (every hook unwired or every lookup
// returns ok=false — e.g. unit tests without a registry, or every
// referenced peer unreachable at registration).
//
//	@concept: error-policy
//	@concept: signal
func validateErrorTypes(n TemplateNodeDef, base string, _ map[string]int, hooks RegistryHooks, res *ValidationResult) {
	validActions := map[string]bool{
		"pass":                      true,
		"give_up":                   true,
		"retry":                     true,
		"discard_claims_then_retry": true,
	}
	var executorClasses []string
	vocabularyKnown := false
	if n.Executor != "" && hooks.ExecutorDeclaredErrorClasses != nil {
		if classes, ok := hooks.ExecutorDeclaredErrorClasses(n.Executor); ok {
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
				className, n.Executor, executorClasses, producerClasses),
		})
	}
}

// isRuntimeSynthesizedErrorClass reports whether className is a
// runtime-emitted (not executor-emitted) error class. Operators may
// declare `error_types:` policies for these regardless of what the
// node's executor advertises; the range-check skips them. Includes
// the `acquire/*` synthetic prefix (pre-dispatch acquisition failure
// per concept:error-policy) and the attribute-pipeline error classes
// emitted by runtime/runner.go and runtime/runner_terminal.go.
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

// errorClassMatchesDeclared reports whether class matches any entry in
// declared. An entry matches if it equals class exactly, or if it ends
// with `*` and is a (slash-or-end-bounded) prefix of class. Per
// `proto:executor_observability.proto::ObservabilityCapabilities.declared_error_classes`.
//
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

// validateAcquireUnavailablePolicyAdvised emits an advisory warning
// (not an error) when a node declares `stores:` but does NOT declare an
// `error_types: { "acquire/unavailable": ... }` policy. Pre-dispatch
// acquisition failure routes through the operator's `error_types:`
// chain via synthetic class `acquire/unavailable`; absent a declared
// policy the default is fail-fast (give_up("unknown_error_class")), not
// implicit retry. Operators that want retry on contention must opt in
// explicitly.
//
//	@concept: error-policy
func validateAcquireUnavailablePolicyAdvised(n TemplateNodeDef, base string, res *ValidationResult) {
	if len(n.Stores) == 0 {
		return
	}
	// @deliberate: skip the advisory when an entry matches
	// `acquire/unavailable` exactly OR via a prefix wildcard (e.g.
	// `acquire/*`).
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

// validateSubscribes enforces SubscriptionEntry shape rules under the
// 2026-05-23 signal-taxonomy reshape: `type:` is a canonical signal
// type-path (exact or trailing-`*` prefix); `when:` is an optional CEL
// predicate over the signal payload. The pre-reshape structured filter
// dimensions (on/when/outcome/error_class/reason/name/kind/sender/
// sender_kind/target) are gone.
//
//	@concept: node-subscription
//	@concept: signal
func validateSubscribes(n TemplateNodeDef, base string, declared map[string]int, hooks RegistryHooks, tmpl *TemplateSpec, res *ValidationResult) {
	for i, s := range n.Subscribes {
		sbase := fmt.Sprintf("%s.subscribes[%d]", base, i)
		// @deliberate: node and Instance are mutually exclusive.
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
		// @deliberate: `node:` must reference a declared node-type.
		if s.Node != "" {
			if _, ok := declared[s.Node]; !ok {
				res.Errors = append(res.Errors, ValidationError{
					Path: sbase + ".node",
					Msg:  fmt.Sprintf("subscription `node: %q` does not reference a declared node", s.Node),
				})
				continue
			}
		}
		// @deliberate: `type:` is required and must match the canonical
		// taxonomy.
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
		// @deliberate: `frame:` must be in | next | "".
		switch s.Frame {
		case "", FrameIn, FrameNext:
		default:
			res.Errors = append(res.Errors, ValidationError{
				Path: sbase + ".frame",
				Msg:  fmt.Sprintf("`frame:` must be empty | %q | %q, got %q", FrameIn, FrameNext, s.Frame),
			})
		}
		// @deliberate: `when:` must compile against the resolved payload
		// schema.
		if s.When != "" {
			if _, err := signal.CompileWhen(signal.TypePath(s.Type), s.When); err != nil {
				res.Errors = append(res.Errors, ValidationError{
					Path: sbase + ".when",
					Msg:  err.Error(),
				})
			}
		}
		// @deliberate: cross-check `terminal/error/<class>` exact-path
		// subscriptions against the union of the sender's declared
		// vocabularies — its executor's declared_error_classes AND
		// every producer in its stores: block (producer-classified
		// acquisition failures land as `terminal/error/<producer
		// class>`, so the validator must accept what the runtime
		// routes). Match rule: leaf matches a declared class iff (a)
		// the class is exact and equals leaf, or (b) the class ends in
		// `*` and is a prefix of leaf. An unmatched leaf is an
		// advisory WARNING, never a hard rejection — same semantics as
		// `validateErrorTypes`: peers MAY declare nothing, and an
		// incomplete vocabulary must not lock operators out of routing
		// classes the system emits. Silent-skip when no vocabulary
		// information is available at all (hooks unwired / every
		// lookup ok=false).
		if s.Node != "" && tmpl != nil && strings.HasPrefix(s.Type, "terminal/error/") &&
			!strings.HasSuffix(s.Type, "*") {
			senderIdx := declared[s.Node]
			sender := tmpl.Nodes[senderIdx]
			leaf := strings.TrimPrefix(s.Type, "terminal/error/")
			// @deliberate: bypass runtime-synthesized error classes
			// (`acquire/*` and the attribute-pipeline classes) — they
			// are emitted by rimsky, not by any peer, so no vocabulary
			// declares them. Mirrors the bypass in
			// `validateErrorTypes` above.
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
		// @deliberate: Cross-check `event/<name>` exact-path subscriptions against
		// subscriptions against the sender's executor declared_events
		// (carry-forward from the pre-reshape `on: event` check).
		if s.Node != "" && hooks.ExecutorDeclaredEvents != nil && tmpl != nil {
			senderIdx := declared[s.Node]
			sender := tmpl.Nodes[senderIdx]
			if sender.Executor != "" && strings.HasPrefix(s.Type, "event/") &&
				!strings.HasSuffix(s.Type, "*") {
				name := strings.TrimPrefix(s.Type, "event/")
				if names, ok := hooks.ExecutorDeclaredEvents(sender.Executor); ok {
					found := false
					for _, n := range names {
						if n == name {
							found = true
							break
						}
					}
					if !found {
						res.Errors = append(res.Errors, ValidationError{
							Path: sbase + ".type",
							Msg:  fmt.Sprintf("event %q not declared by sender %q's executor %q", name, s.Node, sender.Executor),
						})
					}
				}
			}
		}
	}
}

func validateExecutorCoherence(n TemplateNodeDef, base string, res *ValidationResult) {
	// @deliberate: D2 — `delegate:` and `executor:` are mutually
	// exclusive. A node declares EITHER a leaf executor OR a sub-graph
	// delegation; both set or neither set is rejected. The "neither
	// set" rejection is constrained to nodes that have neither claims
	// nor holds — pure-cascade pseudo-nodes remain legal.
	//
	// Post-absorption skip: when canonicalizeGraphs has absorbed a
	// absorbed a sub-graph entry into this calling node, `Executor` is
	// the entry's (canonicalizer-merged), not the author's. The
	// mutual-exclusion check on the author's declaration moved to
	// `absorbEntryIntoCaller`, where it sees the original caller +
	// entry shapes before the merge collapses them. Skipping here
	// avoids a false positive on every absorbed caller.
	hasExecutor := n.Executor != ""
	hasDelegate := n.Delegate != ""
	if hasExecutor && hasDelegate && !n.IsSubgraphEntryAbsorbed {
		res.Errors = append(res.Errors, ValidationError{
			Path: fmt.Sprintf("%s.delegate", base),
			Msg: fmt.Sprintf(
				"delegate and executor are mutually exclusive (executor=%q, delegate=%q)",
				n.Executor, n.Delegate),
		})
	}
	if !hasExecutor && !hasDelegate {
		// @deliberate: Pure-cascade pseudo-node — legal. Warn only if an attribute
		// an attribute schema is declared (those properties have no
		// executor to consume them).
		if n.Attributes != nil && len(n.Attributes.Schema) > 0 {
			res.Warnings = append(res.Warnings, ValidationWarning{
				Path: fmt.Sprintf("%s.attributes", base),
				Msg:  "pure-cascade node declares attributes; attribute values are only consumed by executors",
			})
		}
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
	// @deliberate: Mode none drops the reference leg entirely.
	if hooks.RefValidationMode == RefValidateNone {
		return
	}
	if hooks.ExecutorDeclared(n.Executor) {
		return
	}
	// @deliberate: executor is not provisioned — mode available skips
	// the not-yet-provisioned reference (deferring it to the mandatory
	// instantiation gate); mode all hard-fails.
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
		// @deliberate: Store-declared reference leg. Governed by the operator-set
		// operator-set reference-validation mode — none skips it;
		// available skips a not-yet-provisioned store (falling through
		// to the structural intent/selector checks, which apply
		// regardless); all hard-fails on an undeclared store.
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

		// @deliberate: D5 — claim `lifetime:` validation per the
		// data-platform-extensions design (§Lifetime and the asset
		// pattern).
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
		// @deliberate: `lifetime: durable` requires the producer to
		// advertise the DataProcessing mix-in protocol. The hook
		// reports the per-store capability snapshot when available;
		// silent skip when the hook is nil (e.g. unit-test paths
		// without a registry).
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

// @deliberate: ClaimLifetimeSubgraph and ClaimLifetimeDurable mirror the constants
// in foundation/spec; they're re-declared here as local constants only
// for readability inside the validator. The canonical source is
// foundation/spec/graphs.go.
const (
	ClaimLifetimeSubgraph = "subgraph"
	ClaimLifetimeDurable  = "durable"
)

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
		// @deliberate: named-lock-declared reference leg, governed by
		// the operator-set reference-validation mode — none skips it;
		// available skips a not-yet-provisioned (undeclared) lock; all
		// hard-fails.
		if hooks.NamedLockDeclared != nil && hooks.RefValidationMode != RefValidateNone {
			// @deliberate: skip the check when the name carries an
			// unresolved substitution placeholder (e.g.
			// "model-{params.tier}") — the resolved name is unknown
			// until dispatch.
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

// validateAttributesSchema parses the JSON Schema and checks that
// every `source:` directive in `properties[*].source` is syntactically
// valid. Sources admit literal text and one or more {{...}} directives;
// each directive resolves independently against its source kind
// (`nodes`, `claim`, `params`, `trigger`, `child`). Per-directive
// strict-default with `?` opt-in to lenient.
//
// Also runs checkAttributesSchema: each property must declare at most
// one of `source:` or `default:`, and must satisfy one of "has source",
// "has default", or "is marked readOnly: true in the executor's
// expected_attributes_schema" (executor-write-back populates at commit).
//
// Also runs validateCompositionAgainstExecutor when the executor's
// expected_attributes_schema is visible: (a) type-redeclaration
// conflicts (L2 vs executor), (b) closed-schema-forbidden properties
// (L1 + L2 can't introduce undeclared properties when the executor's
// schema sets additionalProperties: false), (c) default-value-vs-
// executor-type checks via JSON Schema (catches deep-nested type
// mismatches in L1/L2 default values).
//
// Referenced upstream node names must exist in the template; referenced
// claim aliases must be acquired by this node OR co-held via `holds:`
// declarations (concept:claim-co-holdership).
//
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

	// @deliberate: directAliases enumerates the aliases this node
	// directly acquires.
	directAliases := make(map[string]struct{}, len(n.Stores))
	for _, s := range n.Stores {
		directAliases[s.AliasOf()] = struct{}{}
	}
	// @deliberate: heldAliases enumerates the aliases this node
	// co-holds via `holds:` (the sole co-holdership directive;
	// concept:claim-co-holdership). Each holds entry's local alias is
	// bound into the leaf's per-claim slot at dispatch, so
	// `{{claim.<alias>...}}` reads against it are valid — same as a
	// direct alias.
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

			if hd, present := propMap["hard_dep"]; present {
				hdBool, ok := hd.(bool)
				if !ok {
					res.Errors = append(res.Errors, ValidationError{
						Path: fmt.Sprintf("%s.properties.%s.hard_dep", sbase, fname),
						Msg:  "hard_dep must be a boolean",
					})
				} else if hdBool {
					// @deliberate: hard_dep only applies to
					// nodes.<X>.attribute.<Y> reads. Reject the flag on
					// other source kinds (claim, params, trigger, child,
					// event) — those are intrinsically per-frame or
					// instance-scoped and the flag is meaningless.
					if !isAttributeSourceDirective(src) {
						res.Errors = append(res.Errors, ValidationError{
							Path: fmt.Sprintf("%s.properties.%s.hard_dep", sbase, fname),
							Msg:  "hard_dep applies only to nodes.<X>.attribute.<Y> sources; other source kinds are intrinsically per-frame or instance-scoped and don't admit hard_dep",
						})
					}
				}
			}
		}
	}

	// @deliberate: Compute the effective schema (executor's expected schema ∪ L1
	// schema ∪ L1 defaults ∪ L2 node declaration) and run the unified-
	// attribute-surface check against it. The merged schema is also
	// what the runtime recomputes at dispatch (recompute-rather-than-
	// persist).
	//
	// @deliberate: the executor-schema reference legs (readOnly-
	// fallback, L2-readOnly-authorship, and
	// validateCompositionAgainstExecutor) are governed by the
	// operator-set reference-validation mode — none skips the legs
	// entirely (the schema isn't even looked up; the unified-surface
	// check runs with execSchemaVisible=false so only the
	// mode-independent "at most one of source/default" rule fires);
	// available validates against the executor's schema when it is
	// visible and soft-skips when it is not; all validates against the
	// executor's schema when visible and HARD-FAILS when the executor
	// has a schema reference that is not visible.
	if hooks.RefValidationMode == RefValidateNone {
		// @deliberate: mode none — no registration-time executor-schema
		// reference validation. Run only the executor-independent
		// unified-surface rule ("at most one of source/default") over
		// the L2 schema.
		effective := MergeAttributeDefaults(nil, nil, n.Attributes.Schema)
		checkAttributesSchema(effective, n.Attributes.Schema, nil, map[string]bool{}, false, sbase, res)
		return
	}

	var execSchema map[string]any
	var execReadOnlyProps map[string]bool
	execSchemaVisible := false
	if n.Executor != "" && hooks.ExecutorExpectedAttributesSchema != nil {
		if execBytes, ok := hooks.ExecutorExpectedAttributesSchema(n.Executor); ok && len(execBytes) > 0 {
			if err := json.Unmarshal(execBytes, &execSchema); err != nil {
				res.Errors = append(res.Errors, ValidationError{
					Path: base + ".attributes",
					Msg:  fmt.Sprintf("executor %q expected_attributes_schema is not valid JSON: %v", n.Executor, err),
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

	// @deliberate: Mode all hard-fails when the node references an executor whose
	// executor whose expected_attributes_schema cannot be validated at
	// registration (the executor is named but its schema is not
	// visible). Strict counterpart of the available-mode soft-skip
	// below: under `all`, an unvalidatable schema reference is a
	// missing-reference error rather than a deferral.
	if !execSchemaVisible && hooks.RefValidationMode == RefValidateAll &&
		n.Executor != "" && hooks.ExecutorExpectedAttributesSchema != nil {
		res.Errors = append(res.Errors, ValidationError{
			Path: base + ".attributes",
			Msg: refValidationModeRejection(
				fmt.Sprintf("executor %q expected_attributes_schema is not visible at registration", n.Executor),
				hooks.RefValidationMode),
		})
	}

	var l1Defaults map[string]any
	if spec != nil && spec.Defaults != nil && spec.Defaults.Attributes != nil && n.Executor != "" {
		l1Defaults = spec.Defaults.Attributes.ByExecutor[n.Executor]
	}

	effective := MergeAttributeDefaults(execSchema, l1Defaults, n.Attributes.Schema)
	checkAttributesSchema(effective, n.Attributes.Schema, execSchema, execReadOnlyProps, execSchemaVisible, sbase, res)
	if execSchemaVisible {
		validateCompositionAgainstExecutor(execSchema, l1Defaults, n.Attributes.Schema, sbase, res)
	}
}

// validateCompositionAgainstExecutor enforces three executor-authority
// rules over the composed (executor ∪ L1 ∪ L2) attribute schema:
//
//  1. **Type-redeclaration conflicts.** When L2 redeclares a property's
//     `type:` and the executor also declares one for the same property,
//     the types must match. The executor is authoritative on types.
//  2. **Closed-schema-forbidden properties.** When the executor's schema
//     sets `additionalProperties: false` and the executor does not
//     declare a property `X`, neither L1 nor L2 may introduce `X`. L1
//     adding a value for an undeclared property is symmetric to L2
//     declaring it.
//  3. **Default-value vs. executor-type checks.** L1 + L2 default values
//     compose into a "defaults-only" data bag (L2 wins on collision);
//     the bag is JSON-Schema-validated against the executor's raw
//     schema. Catches deep-nested type mismatches that the flat
//     property-type comparison in (1) cannot see.
//
// Only fires when the executor's expected schema is visible (per the
// soft-fail discipline elsewhere in this validator). Adds findings to
// `res.Errors`; surfaces at the operator layer as
// `template_validation_failed`.
//
// @concept: attribute
func validateCompositionAgainstExecutor(execSchema, l1Defaults, nodeSchema map[string]any, sbase string, res *ValidationResult) {
	if execSchema == nil {
		return
	}
	execProps, _ := execSchema["properties"].(map[string]any)
	nodeProps, _ := nodeSchema["properties"].(map[string]any)
	additionalProperties, hasAddProps := execSchema["additionalProperties"]
	// @deliberate: the closed-schema gate fires only when the
	// executor's schema both has a `properties` block (i.e. is not
	// permissive) and explicitly declares `additionalProperties:
	// false`. JSON Schema's default for `additionalProperties` is true;
	// absence ⇒ open.
	closed := false
	if hasAddProps {
		if b, ok := additionalProperties.(bool); ok && !b {
			closed = true
		}
	}

	// @deliberate: type-redeclaration conflicts — walk L2's declared
	// properties.
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

	// @deliberate: closed-schema-forbidden properties (L2 and L1).
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
			// @deliberate: avoid duplicate reporting if L2 also
			// declared it.
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

	// @deliberate: default-value-vs-executor-type checks. Compose L1 +
	// L2 default values into a single map; L2 wins on collision per the
	// most-specific-wins rule. Then JSON-Schema-validate the composed
	// bag against the executor's raw schema. Catches deep-nested type
	// mismatches the flat property-type check above cannot see.
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
	// @deliberate: Strip `required:` from the executor schema before validating the
	// validating the defaults bag. The defaults bag is an
	// intentionally-partial subset of what the dispatch bag will hold —
	// properties bound via `source:` and properties the executor will
	// write (`readOnly: true`) have no entry in defaults. Enforcing
	// `required:` against that subset would fire false-positive
	// missing-property errors at registration. This pass only catches
	// type / nested-shape mismatches on values that are present.
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

// schemaWithoutTopLevelRequired returns a shallow clone of `schema`
// with the top-level `required` key removed. Used by
// validateCompositionAgainstExecutor's defaults-bag pass: that bag is
// a proper subset of the dispatch-time bag (only static defaults
// populated; source-bound and executor-written properties are absent),
// so `required:` enforcement against it would fire false positives.
// The clone is shallow — nested schemas keep their `required:` keys.
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

// validateAgainstSchema compiles `schema` and validates `data` against
// it. Returns nil on success, the underlying validation error on
// failure. Used by validateCompositionAgainstExecutor's nested-default
// check; mirrors the call shape graph/attribute/validate.go::Validate
// uses but stays local because the validator layer does not depend on
// the runtime's phase taxonomy.
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

// jsonValuesEqual compares two JSON-decoded `type:` declarations. Both
// scalar string types ("string", "object") and array unions
// (["string","null"]) are admissible. Equality is structural after
// json round-trip to normalize map types.
func jsonValuesEqual(a, b any) bool {
	ab, errA := json.Marshal(a)
	bb, errB := json.Marshal(b)
	if errA != nil || errB != nil {
		return false
	}
	return bytes.Equal(ab, bb)
}

// isValidFallbackLiteral reports whether s is a JSON literal admitted
// on the right side of the substitution fallback operator: `null`,
// `true`, `false`, a JSON number, or a quoted JSON string. Composite
// literals (`{}`, `[]`) are rejected. Per spec
// Numeric admission goes through json.Unmarshal rather than
// strconv.ParseFloat so the validator rejects the same shapes the
// runtime rejects (`NaN`, `Inf`, `.5`, etc. — non-JSON-number forms).
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

// isAttributeSourceDirective returns true if src is a {{...}} directive
// whose body starts with "nodes.<X>.attribute". Used by the validator
// to gate the hard_dep flag.
func isAttributeSourceDirective(src string) bool {
	trimmed := strings.TrimSpace(src)
	m := dispatchDirectiveRe.FindStringSubmatch(trimmed)
	if m == nil {
		return false
	}
	body := strings.TrimSpace(m[1])
	// @deliberate: strip an optional fallback `| <literal>` suffix —
	// the hard-dep gate looks at the directive's source-kind, not the
	// fallback.
	if idx := strings.Index(body, "|"); idx >= 0 {
		body = strings.TrimSpace(body[:idx])
	}
	parts := strings.Split(body, ".")
	return len(parts) >= 3 && parts[0] == "nodes" && parts[2] == "attribute"
}

// checkAttributeSource enforces directive syntax + reference validity
// for a `source:` value. Per the 2026-05-21 userdata collapse the
// grammar accepts literal text alongside one or more `{{...}}`
// directives in a single source string; each directive resolves
// independently against its source kind. Per-directive strict-default
// with `?` opt-in to lenient (mutually exclusive with `| <literal>`
// fallback).
//
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

// checkAttributeDirectiveBody validates the body of one `{{...}}`
// directive (caller has already stripped the outer braces). Handles
// `?` lenient marker and `| <literal>` fallback parsing, then routes
// to per-kind validation.
func checkAttributeDirectiveBody(body, path string, declared map[string]int, directAliases, heldAliases map[string]struct{}, res *ValidationResult) {
	body = strings.TrimSpace(body)
	// @deliberate: pipe-fallback parsing first (longest reach) so a
	// trailing `?` is still recognised on the left side of the pipe if
	// present.
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
	// @deliberate: Lenient `?` marker (must appear at the end of the directive body
	// directive body after optional whitespace and before the optional
	// `| <literal>` fallback (already stripped).
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
		// @deliberate: valid forms are claim.<alias>.address,
		// claim.<alias>.claim_scope, and
		// claim.<alias>.payload(.<field-path>?). The bare-form
		// `claim.<alias>.payload` (no trailing field path) resolves to
		// the whole payload object per spec §Item 3 "Empty trailing
		// path".
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
			// @deliberate: bare form `claim.<alias>.payload` is
			// admitted (whole-payload pull). A trailing path is also
			// fine; an explicit empty trailing segment (`...payload.`)
			// is rejected.
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
		// @deliberate: alias must be acquired here (stores:) or
		// co-held (holds:).
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
		// @deliberate: substitution forms are
		// nodes.<node>.attribute(.<field>?…) and
		// nodes.<emitter>.event.<event_name>(.<json-path>?). Bare forms
		// `nodes.<node>.attribute` and `nodes.<node>.event.<name>` (no
		// trailing field path) resolve to the whole attribute object /
		// whole event payload per spec §Item 3 "Empty trailing path".
		if len(parts) < 2 || parts[0] == "" {
			res.Errors = append(res.Errors, ValidationError{
				Path: path,
				Msg:  fmt.Sprintf("nodes directive %q must be nodes.<node>.{attribute|event}[.<...>]", body),
			})
			return
		}
		switch parts[1] {
		case "attribute":
			// @deliberate: nodes.<node>.attribute(.<field>?…) — an
			// explicit empty trailing segment (`...attribute.`) is
			// rejected; a missing trailing path is the bare form.
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
		case "event":
			// @deliberate: nodes.<node>.event.<name>(.<path>?…) — event
			// name is required; a missing field path is the bare form
			// (resolves to the whole event payload).
			if len(parts) < 3 || parts[2] == "" {
				res.Errors = append(res.Errors, ValidationError{
					Path: path,
					Msg:  fmt.Sprintf("nodes directive %q must be nodes.<node>.event.<name>[.<path>]", body),
				})
				return
			}
			if len(parts) > 3 && parts[3] == "" {
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
				Msg:  fmt.Sprintf("nodes directive %q second segment must be 'attribute' or 'event'", body),
			})
		}
	case "trigger":
		// @deliberate: trigger.message.payload(.<field-path>?) — the
		// bare form `trigger.message.payload` (no trailing field path)
		// resolves to the whole trigger message payload per spec §Item
		// 3.
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
		// @deliberate: only child.partition_key is admitted.
		if len(parts) != 1 || parts[0] != "partition_key" {
			res.Errors = append(res.Errors, ValidationError{
				Path: path,
				Msg:  fmt.Sprintf("child directive %q must be child.partition_key", body),
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
				Msg:  fmt.Sprintf("invalid directive %q (expected claim.<a>.{address|claim_scope|payload[.<f>]}, params.<k>, nodes.<n>.attribute[.<f>], nodes.<n>.event.<name>[.<path>], trigger.message.payload[.<f>], or child.partition_key)", body),
			})
		}
	}
}

// validateMaxParkDuration + validateOnEvent removed by the 2026-05-14
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

// AttributesSchemaCheckError reports a unified-attribute-surface
// violation found by CheckEffectiveAttributesSchema. The path is in
// `properties.<name>` form (no leading `attributes.schema`), suitable
// for embedding in registration error messages or dispatch failure
// classes.
type AttributesSchemaCheckError struct {
	Path string
	Msg  string
}

// CheckEffectiveAttributesSchema enforces the unified-attribute-surface
// invariant on a merged effective schema and returns one error per
// violating property. The exported entry point is used by runtime
// dispatch to re-enforce the rule once the executor's expected schema
// is visible (the validator's registration-time pass soft-fails the
// readOnly leg when the discovery cache hasn't populated yet; runtime
// reapplies under the same MergeAttributeDefaults shape).
//
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
		// @deliberate: a property the executor does not enumerate is
		// unconstrained when the executor's schema is open — it
		// declares no `properties` block (fully permissive, e.g.
		// `{"type":"object"}`) or admits extensions via
		// `additionalProperties` (not `false`). The executor has
		// delegated naming authority for such properties, so the
		// readOnly-fallback and readOnly-authorship legs below don't
		// apply — there is no fixed set of executor-written properties
		// to compare against. An enumerated property still goes through
		// both legs.
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

// IsPermissiveExecutorSchema reports whether the executor's advertised
// schema declares "no constraint on attribute shape" — a missing
// `properties` block. The unified-attribute-surface check skips the
// readOnly-fallback leg for permissive schemas: an executor that
// declines to enumerate its properties cannot be checked against
// "which properties am I supposed to produce?" because the set is open.
//
// An executor that declares `"properties": {}` is NOT permissive — it
// is declaring "closed: I have zero properties." That's a meaningful
// contract distinct from "I don't enumerate."
//
// @concept: attribute
func IsPermissiveExecutorSchema(execSchema map[string]any) bool {
	if execSchema == nil {
		return false
	}
	_, hasProps := execSchema["properties"]
	return !hasProps
}

// executorSchemaAllowsExtensions reports whether the executor's advertised
// schema EXPLICITLY opts into accepting properties it does not enumerate —
// i.e. it carries an `additionalProperties` directive that is either the
// boolean `true` or a schema object (e.g. `{"type":"string"}`). Such an
// executor has delegated naming authority for extension properties: a
// template author may declare those properties (source-bound, default,
// write-back, or `readOnly: true`) without the executor enumerating them, so
// the readOnly-fallback and readOnly-authorship legs of the unified-attribute-
// surface check do not apply to an unenumerated property here.
//
// An ABSENT `additionalProperties` returns false even though strict JSON
// Schema would default it to true. The unified-attribute-surface check treats
// the presence of a `properties` block as "the executor declares its
// contract" (see IsPermissiveExecutorSchema): an unenumerated property under
// such a schema must still justify how it is populated (source / default /
// executor readOnly) unless the executor explicitly opens the door. Only the
// explicit directive delegates that authority. An explicit `additionalProperties:
// false` (closed) also returns false. The value-type check for extension
// properties under a schema-object value lives in
// validateCompositionAgainstExecutor.
//
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
	// @deliberate: schema-object value ⇒ extensions admitted (subject
	// to that subschema).
	return true
}

// checkAttributesSchema enforces the unified-attribute-surface
// invariant: each property must satisfy one of (a) has `source:`,
// (b) has `default:`, or (c) is marked `readOnly: true` in the
// executor's expected_attributes_schema (executor-write-back populates
// at commit). Properties with both `source:` and `default:` are also
// rejected.
//
// The template author's L2 declaration cannot set `readOnly: true` on
// a property the executor's schema does not also mark `readOnly: true`
// — the executor is authoritative on which properties it produces.
//
// `effective` is the merged effective schema (executor's
// expected_attributes_schema ∪ L1 defaults ∪ L2 node declaration).
// `nodeSchema` is the per-node L2 schema (used for `readOnly`
// authorship checks since L1 doesn't carry `readOnly`).
//
// `execSchemaVisible` reports whether the executor's expected schema
// was available at validation time. When false (no discovery hook
// wired, executor not yet handshaked, or the executor advertises no
// schema), the readOnly-fallback leg is skipped — without the
// executor's schema we cannot tell whether a sourceless/defaultless
// property is one the executor produces. The "at most one of
// source/default" rule and the L2-readOnly-authorship rule still
// fire unconditionally. Runtime dispatch's
// `runtime/runner_dispatch.go::resolveAttributes` reapplies the full
// check once the executor's schema is visible at dispatch time
// (`code:runtime/runner_dispatch.go` calls
// `node.CheckEffectiveAttributesSchema`).
//
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
		// @deliberate: readOnly-fallback leg. A property the executor
		// does not enumerate is unconstrained when the executor's
		// schema declares no `properties` block (fully permissive) or
		// admits extensions via `additionalProperties` (not `false`) —
		// the executor has delegated naming authority, so there is no
		// per-name comparison to make. An enumerated property still
		// goes through both legs.
		propUnconstrained := schemaHasNoProps || (execOpen && !execEnumerates)
		if !hasSource && !hasDefault && !execRO && execSchemaVisible && !propUnconstrained {
			res.Errors = append(res.Errors, ValidationError{
				Path: fmt.Sprintf("%s.properties.%s", sbase, name),
				Msg:  "property has no `source:`, no `default:`, and is not marked `readOnly: true` in the executor's expected_attributes_schema — declare one of these or the property is unpopulated at dispatch",
			})
			continue
		}
		// @deliberate: L2 cannot grant `readOnly: true` on a property
		// the executor's schema does not also mark `readOnly: true` —
		// the executor is authoritative on which of its attributes it
		// produces.
		//
		// Skipped when the executor's schema isn't visible
		// at validation time — without it the executor's set of
		// produced properties is unknown, so trust the author's
		// declaration. Runtime dispatch reapplies the rule once the
		// executor schema is known. Also skipped for properties the
		// executor leaves unconstrained under an open schema — the
		// author owns extension properties the executor admits by name.
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

// extractReadOnlyProps returns the set of top-level property names with
// `readOnly: true` in the executor's expected_attributes_schema.
// Returns an empty map when the schema is nil or has no `properties`.
//
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

// MergeAttributeDefaults computes the per-node effective attribute
// schema as the union of (1) the executor's expected_attributes_schema,
// (2) the L1 template defaults (`spec.Defaults.Attributes.ByExecutor`),
// and (3) the node's L2 attribute schema. Most specific wins on
// `default:`. Types come from the executor's schema where declared;
// L1 contributes `default:` entries only; L2 deep-merges over both.
//
// Pure function — used both by the template validator at registration
// and by the runtime at dispatch (recompute path; see
// runtime/runner_dispatch.go::substituteAttributesSchema).
//
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
	// @deliberate: L1 — for each (attr, value), set
	// properties[attr].default.
	for attr, val := range l1Defaults {
		prop, _ := props[attr].(map[string]any)
		if prop == nil {
			prop = map[string]any{}
			props[attr] = prop
		}
		prop["default"] = val
	}
	// @deliberate: L2 — deep-merge the node's properties on top.
	if nodeSchema != nil {
		if nodeProps, ok := nodeSchema["properties"].(map[string]any); ok {
			for attr, raw := range nodeProps {
				nodeProp, _ := raw.(map[string]any)
				if nodeProp == nil {
					continue
				}
				existing, _ := props[attr].(map[string]any)
				if existing == nil {
					// @deliberate: Deep copy so L2's prop isn't aliased.
					props[attr] = deepCopyJSON(nodeProp)
					continue
				}
				// @deliberate: `source:` and `default:` are mutually
				// exclusive on a property. If L2 supplies one, it
				// overrides L1's choice of the other — drop the
				// pre-existing key so the effective schema doesn't
				// carry both into checkAttributesSchema (which would
				// reject the template). Most-specific-wins per the
				// userdata-collapse "Resolution waterfall" / "Effective
				// schema computation".
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
		// @deliberate: Carry over `required` from the node schema (union with any
		// (union with any existing list from the executor's schema).
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
		// @deliberate: additionalProperties — the executor is
		// authoritative when it declared the key (closed-schema
		// policy); if the executor didn't declare it, fall back to the
		// node's declaration.
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

// parseDurationStrict wraps time.ParseDuration. Wrapped to keep the
// validator's call-sites uniform with other "parse and report" helpers.
func parseDurationStrict(s string) (time.Duration, error) {
	return time.ParseDuration(s)
}

// validateTagsAtRegistration walks every tag string on a node and
// enforces the materialization-time substitution rules:
//
//   - Only `{{params.<key>}}` directives are admitted (no other source
//     kinds resolve at instance creation).
//   - The `<key>` MUST be declared in TemplateSpec.ParamsSchema.properties.
//
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
			// @deliberate: Take the top-level params key only — params.<key>.<sub>...
			// params.<key>.<sub>... resolves the same root key.
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

// paramsSchemaProperties returns the `properties` map of the template's
// `params_schema` as `map[string]any` (the JSON Schema canonical shape).
// Returns nil when params_schema is absent or has no properties.
func paramsSchemaProperties(spec *TemplateSpec) map[string]any {
	if spec == nil || spec.ParamsSchema == nil {
		return nil
	}
	props, _ := spec.ParamsSchema["properties"].(map[string]any)
	return props
}

// validatePublishers checks the top-level `publishers:` block. Every
// entry must declare `name`, `kind`, and `target_node`; `target_node`
// must reference a declared node type. This catches missing fields at
// template registration so operators see a clear validation error
// instead of a pgx NOT NULL violation when the row is inserted into
// table:rimsky_publisher_subscriptions at instance-create.
func validatePublishers(spec *TemplateSpec, declared map[string]int, res *ValidationResult) {
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
	}
}
