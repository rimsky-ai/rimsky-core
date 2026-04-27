package node

import (
	"bytes"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"github.com/robfig/cron/v3"
	"github.com/santhosh-tekuri/jsonschema/v5"

	"github.com/fallguy/rimsky/core/store"
)

// ValidationError is a blocking problem with a template. Path locates the
// offending element in the template using JSONPath-ish notation (e.g.
// "nodes[2].dependencies[0]").
type ValidationError struct {
	Path string
	Msg  string
}

// ValidationWarning is a non-blocking problem. Callers can surface warnings
// in operator UIs without rejecting the template.
type ValidationWarning struct {
	Path string
	Msg  string
}

// ValidationResult is returned by ValidateTemplate. Ok() is true when no
// errors were found (warnings are allowed).
type ValidationResult struct {
	Errors   []ValidationError
	Warnings []ValidationWarning
}

// Ok reports whether the template passed validation (no errors).
func (r ValidationResult) Ok() bool { return len(r.Errors) == 0 }

// Store kinds the validator recognises. Match the canonical Kind() return
// values from the Store implementations (see core/store/filesystem,
// core/store/claimstorepg). The `stub_*` siblings cover scenario tests
// that wire `core/store/stub/` factories — the stub stores model the
// same capability surface as the production stores and must validate
// under the same rules (claim:true requires a claim-shaped store; write
// regions require a filesystem-shaped store).
const (
	storeKindFilesystem     = "filesystem"
	storeKindClaimStore     = "claim_store"
	storeKindStubFilesystem = "stub_filesystem"
	storeKindStubClaimStore = "stub_claim_store"
)

// isFilesystemKind reports whether kind names a filesystem-shaped store
// (canonical or stub). Used by the validator to gate `write` / `read`
// region declarations.
func isFilesystemKind(kind string) bool {
	return kind == storeKindFilesystem || kind == storeKindStubFilesystem
}

// isClaimStoreKind reports whether kind names a claim-shaped store
// (canonical or stub). Used by the validator to gate `claim:true`.
func isClaimStoreKind(kind string) bool {
	return kind == storeKindClaimStore || kind == storeKindStubClaimStore
}

// instantiationPlaceholderRe matches single-brace, instantiation-time
// placeholders: only `{params.<key>}` per spec §10. (`{instance_id}` and
// `{consumer_key}` were retired alongside the resources/concurrency-tags
// fields they were used in; spec §11.3 / §10.) Used inside region patterns
// and lock names to allow inline instantiation-time substitution.
var instantiationPlaceholderRe = regexp.MustCompile(`\{params\.[a-zA-Z_][a-zA-Z0-9_]*\}`)

// anyBraceRe matches any single-`{...}` segment that isn't part of a
// double-`{{...}}` directive. Used to spot-check stray placeholders.
var anyBraceRe = regexp.MustCompile(`\{[^{}]*\}`)

// dispatchDirectiveRe matches `{{<inside>}}` directives. Mirrors the
// substitution grammar in core/attributes (kept inline here to honour the
// node-package's "import shared/ only-ish" policy: the validator depends
// on syntax recognition, not runtime resolution).
var dispatchDirectiveRe = regexp.MustCompile(`\{\{([^{}]+)\}\}`)

// directiveBodyRe further parses the inside of a `{{...}}` against the
// three known source kinds (deps, claim, params). Anchored so a non-match
// is a syntactic error.
var directiveBodyRe = regexp.MustCompile(`^(deps|claim|params)\.(.+)$`)

// ValidateTemplate walks a parsed template and reports errors/warnings per
// spec §11 (template-deploy validation).
//
// storeKindOf is a lookup for registered stores. Returns the Store.Kind()
// and ok=true for known stores; ok=false for unknown names. Callers wire
// this in at the integration boundary (control-api populates it from its
// store registry); the node package cannot import the store registry
// directly without creating a dependency cycle.
//
// storeKindOf may be nil during tests that don't exercise Stores; in that
// case unknown-store errors are skipped.
func ValidateTemplate(spec *TemplateSpec, storeKindOf func(name string) (string, bool)) ValidationResult {
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
		validateDependencies(n, base, declared, &res)
		validateErrorTypes(n, base, declared, &res)
		validateSchedule(n, base, cronParser, &res)
		validateExecutorCoherence(n, base, &res)
		validateStores(n, base, storeKindOf, &res)
		validateLocks(n, base, &res)
		validateAttributesSchema(n, base, declared, &res)
	}

	detectCycles(spec.Nodes, &res)
	validateClaimResolutions(spec, declared, &res)

	return res
}

// validateFrameResolution enforces the §3-§4 frame-resolution template
// requirements (per docs/specs/2026-04-26-frame-resolution-design.md):
//   - frame_resolution is required and must be one of "coalesce" or
//     "serial_queue".
//   - frame_timeout_ms must be >= FrameTimeoutMinMs (60000) when set; a
//     zero value is accepted (the deploy handler default-fills to
//     FrameTimeoutDefaultMs after validation passes).
//
// Pure: does not mutate spec. Default-fill happens at the deploy boundary
// (see ApplyFrameResolutionDefaults) so re-validating the same struct is
// idempotent.
func validateFrameResolution(spec *TemplateSpec, res *ValidationResult) {
	switch spec.FrameResolution {
	case FrameResolutionCoalesce, FrameResolutionSerialQueue:
		// ok
	case "":
		res.Errors = append(res.Errors, ValidationError{
			Path: "frame_resolution",
			Msg: fmt.Sprintf("frame_resolution is required (one of: %q, %q)",
				FrameResolutionCoalesce, FrameResolutionSerialQueue),
		})
	default:
		res.Errors = append(res.Errors, ValidationError{
			Path: "frame_resolution",
			Msg: fmt.Sprintf("frame_resolution = %q is not a valid value (one of: %q, %q)",
				spec.FrameResolution, FrameResolutionCoalesce, FrameResolutionSerialQueue),
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
// default (FrameTimeoutDefaultMs) when zero. Callers should run
// ValidateTemplate first; this helper applies after validation passes
// so the validator stays pure (no behaviour-modifying mutation).
func ApplyFrameResolutionDefaults(spec *TemplateSpec) {
	if spec == nil {
		return
	}
	if spec.FrameTimeoutMs == 0 {
		spec.FrameTimeoutMs = FrameTimeoutDefaultMs
	}
}

func validateDependencies(n TemplateNodeDef, base string, declared map[string]int, res *ValidationResult) {
	for j, dep := range n.Dependencies {
		if _, ok := declared[dep]; !ok {
			res.Errors = append(res.Errors, ValidationError{
				Path: fmt.Sprintf("%s.dependencies[%d]", base, j),
				Msg:  fmt.Sprintf("dependency %q does not reference a declared node", dep),
			})
		}
	}
}

func validateErrorTypes(n TemplateNodeDef, base string, declared map[string]int, res *ValidationResult) {
	for className, policy := range n.ErrorTypes {
		for ai, action := range policy.Policy {
			if action.Action != "invalidate" {
				continue
			}
			for ti, target := range action.Targets {
				if _, ok := declared[target]; !ok {
					res.Errors = append(res.Errors, ValidationError{
						Path: fmt.Sprintf("%s.error_types[%s].policy[%d].targets[%d]", base, className, ai, ti),
						Msg:  fmt.Sprintf("target %q does not reference a declared node", target),
					})
				}
			}
		}
	}
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

// validateStores enforces the per-node store-usage rules from spec §11.
//
//   - Each named store must resolve to a known store kind via storeKindOf.
//   - A node cannot reference the same store name twice (duplicate detection).
//   - `claim: true` requires the named store to be of kind claim_store.
//   - `write` and `read` patterns are only valid against filesystem-kind
//     stores (claim stores have no region concept).
//   - Region patterns may contain dispatch-time `{{...}}` directives; this
//     pass only checks directive syntax (full resolution happens at dispatch).
func validateStores(n TemplateNodeDef, base string, storeKindOf func(name string) (string, bool), res *ValidationResult) {
	seen := make(map[string]int, len(n.Stores))
	for j, s := range n.Stores {
		sbase := fmt.Sprintf("%s.stores[%d]", base, j)
		name := strings.TrimSpace(s.Name)
		if name == "" {
			res.Errors = append(res.Errors, ValidationError{
				Path: sbase + ".name", Msg: "store name is required",
			})
			continue
		}
		if prev, dup := seen[name]; dup {
			res.Errors = append(res.Errors, ValidationError{
				Path: sbase + ".name",
				Msg:  fmt.Sprintf("duplicate store name %q (already at stores[%d])", name, prev),
			})
			continue
		}
		seen[name] = j

		var kind string
		var known bool
		if storeKindOf != nil {
			kind, known = storeKindOf(name)
			if !known {
				res.Errors = append(res.Errors, ValidationError{
					Path: sbase + ".name",
					Msg:  fmt.Sprintf("unknown store %q", name),
				})
				continue
			}
		}

		if s.Claim && known && !isClaimStoreKind(kind) {
			res.Errors = append(res.Errors, ValidationError{
				Path: sbase + ".claim",
				Msg:  fmt.Sprintf("claim:true requires a %q-kind store; %q is %q", storeKindClaimStore, name, kind),
			})
		}
		if s.Hold && !s.Claim {
			res.Errors = append(res.Errors, ValidationError{
				Path: sbase + ".hold",
				Msg:  "hold:true requires claim:true",
			})
		}

		if known && !isFilesystemKind(kind) {
			if len(s.Write) > 0 {
				res.Errors = append(res.Errors, ValidationError{
					Path: sbase + ".write",
					Msg:  fmt.Sprintf("write regions are only valid on %q-kind stores; %q is %q", storeKindFilesystem, name, kind),
				})
			}
			if len(s.Read) > 0 {
				res.Errors = append(res.Errors, ValidationError{
					Path: sbase + ".read",
					Msg:  fmt.Sprintf("read regions are only valid on %q-kind stores; %q is %q", storeKindFilesystem, name, kind),
				})
			}
		}

		for wi, pat := range s.Write {
			checkRegionDirectives(pat, fmt.Sprintf("%s.write[%d]", sbase, wi), res)
		}
		for ri, pat := range s.Read {
			checkRegionDirectives(pat, fmt.Sprintf("%s.read[%d]", sbase, ri), res)
		}
	}
}

// validateLocks enforces the named-lock spec from §8.3. Mode must be one of
// the two recognised LockMode values; counting locks require a positive
// limit; mutex locks ignore limit (a non-zero limit is accepted but not
// load-bearing — accepted silently to avoid noisy errors when authors
// migrate from counting → mutex without trimming the field).
func validateLocks(n TemplateNodeDef, base string, res *ValidationResult) {
	seen := make(map[string]int, len(n.Locks))
	for j, l := range n.Locks {
		lbase := fmt.Sprintf("%s.locks[%d]", base, j)
		name := strings.TrimSpace(l.Name)
		if name == "" {
			res.Errors = append(res.Errors, ValidationError{
				Path: lbase + ".name", Msg: "lock name is required",
			})
		} else {
			// Lock names may carry instantiation-time `{params.x}` or
			// dispatch-time `{{...}}` directives; spot-check syntax.
			checkLockNameDirectives(name, lbase+".name", res)
			if prev, dup := seen[name]; dup {
				res.Errors = append(res.Errors, ValidationError{
					Path: lbase + ".name",
					Msg:  fmt.Sprintf("duplicate lock name %q (already at locks[%d])", name, prev),
				})
			} else {
				seen[name] = j
			}
		}
		switch l.Mode {
		case store.LockModeMutex:
			// limit is ignored for mutex; accept silently.
		case store.LockModeCounting:
			if l.Limit < 1 {
				res.Errors = append(res.Errors, ValidationError{
					Path: lbase + ".limit",
					Msg:  fmt.Sprintf("counting lock requires limit >= 1; got %d", l.Limit),
				})
			}
		case "":
			res.Errors = append(res.Errors, ValidationError{
				Path: lbase + ".mode",
				Msg:  "lock mode is required (mutex | counting)",
			})
		default:
			res.Errors = append(res.Errors, ValidationError{
				Path: lbase + ".mode",
				Msg:  fmt.Sprintf("unknown lock mode %q (expected mutex | counting)", l.Mode),
			})
		}
	}
}

// validateAttributesSchema parses the JSON Schema (best-effort: a malformed
// schema is reported but does not block other validations) and checks that
// every `source:` directive in `properties[*].source` is syntactically
// valid: a single `{{...}}` body matching `deps.<n>.<f>`,
// `claim.<store>.<f...>`, or `params.<k>`. Referenced upstream node names
// must exist in the template; referenced store names must appear in this
// node's Stores list.
func validateAttributesSchema(n TemplateNodeDef, base string, declared map[string]int, res *ValidationResult) {
	if len(n.Attributes.Schema) == 0 {
		return
	}
	sbase := fmt.Sprintf("%s.attributes.schema", base)

	// Parse: round-trip through JSON to normalise (yaml.v3 may produce
	// map[any]any subtrees) and then compile via santhosh-tekuri to confirm
	// the schema itself parses.
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
		// Continue: source-directive checks below operate on the raw map
		// shape, not the compiled schema.
	}

	// Build the set of store names this node references — used to
	// authorise `{{claim.<store>.payload.<...>}}` references.
	declaredStoreNames := make(map[string]struct{}, len(n.Stores))
	for _, s := range n.Stores {
		if s.Name != "" {
			declaredStoreNames[s.Name] = struct{}{}
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
		checkAttributeSource(src, fmt.Sprintf("%s.properties.%s.source", sbase, fname), declared, declaredStoreNames, res)
	}
}

// checkAttributeSource enforces directive syntax + reference validity for
// a single `source:` value. Per spec §10 the value must be exactly one
// `{{...}}` directive (no surrounding text and no multiple directives).
func checkAttributeSource(src, path string, declared map[string]int, storeNames map[string]struct{}, res *ValidationResult) {
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
	// Confirm the directive consumes the whole value.
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
			Msg:  fmt.Sprintf("source directive %q must start with deps.|claim.|params.", body),
		})
		return
	}
	kind := bodyMatch[1]
	rest := bodyMatch[2]
	parts := strings.Split(rest, ".")
	switch kind {
	case "deps":
		if len(parts) < 2 || parts[0] == "" || parts[1] == "" {
			res.Errors = append(res.Errors, ValidationError{
				Path: path,
				Msg:  fmt.Sprintf("deps directive %q must be deps.<node>.<field>", body),
			})
			return
		}
		if _, ok := declared[parts[0]]; !ok {
			res.Errors = append(res.Errors, ValidationError{
				Path: path,
				Msg:  fmt.Sprintf("deps directive references unknown node %q", parts[0]),
			})
		}
	case "claim":
		// Per attributes/substitution.go, valid form is
		// claim.<store>.payload.<...>. The validator demands the literal
		// `payload` segment to keep dispatch-time resolution well-formed.
		if len(parts) < 3 || parts[0] == "" || parts[1] != "payload" || parts[2] == "" {
			res.Errors = append(res.Errors, ValidationError{
				Path: path,
				Msg:  fmt.Sprintf("claim directive %q must be claim.<store>.payload.<field>", body),
			})
			return
		}
		if _, ok := storeNames[parts[0]]; !ok {
			res.Errors = append(res.Errors, ValidationError{
				Path: path,
				Msg:  fmt.Sprintf("claim directive references store %q not in this node's stores list", parts[0]),
			})
		}
	case "params":
		if len(parts) < 1 || parts[0] == "" {
			res.Errors = append(res.Errors, ValidationError{
				Path: path,
				Msg:  fmt.Sprintf("params directive %q must be params.<key>", body),
			})
		}
	default:
		// Should be unreachable given directiveBodyRe.
		res.Errors = append(res.Errors, ValidationError{
			Path: path,
			Msg:  fmt.Sprintf("unknown directive kind %q", kind),
		})
	}
}

// checkRegionDirectives spot-checks a region pattern. Region patterns may
// contain dispatch-time `{{...}}` directives and instantiation-time
// `{params.x}` placeholders. Stray single-brace tokens that aren't
// `{params.x}` are flagged as malformed.
func checkRegionDirectives(s, path string, res *ValidationResult) {
	checkDispatchDirectives(s, path, res)
	// Strip `{{...}}` directives before scanning for stray `{...}` tokens
	// (otherwise the inner body of a `{{deps.foo.bar}}` would match
	// anyBraceRe).
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

// checkLockNameDirectives spot-checks a lock name. Same grammar as a
// region pattern: `{params.x}` or `{{...}}`.
func checkLockNameDirectives(s, path string, res *ValidationResult) {
	checkRegionDirectives(s, path, res)
}

// checkDispatchDirectives validates every `{{...}}` body in s against the
// substitution grammar. Resolution is dispatch-time; this pass is grammar-
// only.
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
				Msg:  fmt.Sprintf("invalid directive %q (expected deps.<n>.<f>, claim.<store>.payload.<f>, or params.<k>)", body),
			})
		}
	}
}

// validateClaimResolutions implements the §11.4 algorithm: for every node
// that takes a held claim, walk the dependency DAG, find the terminal
// leaves of the holding subgraph, and verify each terminal carries a
// matching `claim_resolutions` entry.
func validateClaimResolutions(spec *TemplateSpec, declared map[string]int, res *ValidationResult) {
	for _, source := range spec.Nodes {
		if _, ok := declared[source.Type]; !ok {
			continue
		}
		for _, s := range source.Stores {
			if !s.Claim || !s.Hold {
				continue
			}
			leaves := findHoldingTerminals(spec, source.Type, s.Name)
			missing := make([]string, 0)
			for _, leaf := range leaves {
				if !leafResolves(spec, leaf, source.Type, s.Name) {
					missing = append(missing, leaf)
				}
			}
			if len(missing) == 0 {
				continue
			}
			path := fmt.Sprintf("nodes[%d].stores", declared[source.Type])
			res.Errors = append(res.Errors, ValidationError{
				Path: path,
				Msg: fmt.Sprintf(
					"held claim %q on store %q is not resolved at terminal node(s) %s",
					source.Type, s.Name, strings.Join(missing, ", "),
				),
			})
		}
	}
}

// findHoldingTerminals returns the terminal leaves of the holding subgraph
// of sourceNode w.r.t. storeName. Per §11.4 the holding subgraph is
// `{ D | D depends transitively on sourceNode } ∪ { sourceNode }`, and a
// leaf is a node in that subgraph with no descendants also in the subgraph.
//
// storeName is included in the signature for forward-compat: today the
// holding subgraph is independent of the store name, but the §11.4 worked
// example treats the leaf set as per-(source, store) — keeping the
// parameter avoids a rewrite if a future refinement makes the subgraph
// store-aware (e.g. resolution-aware path pruning).
func findHoldingTerminals(spec *TemplateSpec, sourceNode string, _ string) []string {
	// Build a reverse adjacency: dep -> [nodes that depend on dep].
	dependents := make(map[string][]string, len(spec.Nodes))
	for _, n := range spec.Nodes {
		for _, dep := range n.Dependencies {
			dependents[dep] = append(dependents[dep], n.Type)
		}
	}
	// BFS from sourceNode through the reverse adjacency to collect the
	// holding subgraph.
	subgraph := map[string]struct{}{sourceNode: {}}
	queue := []string{sourceNode}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		for _, child := range dependents[cur] {
			if _, seen := subgraph[child]; seen {
				continue
			}
			subgraph[child] = struct{}{}
			queue = append(queue, child)
		}
	}
	// A leaf is a node in the subgraph with no descendant also in the
	// subgraph.
	leaves := make([]string, 0)
	for member := range subgraph {
		hasDescendantInSubgraph := false
		for _, child := range dependents[member] {
			if _, ok := subgraph[child]; ok {
				hasDescendantInSubgraph = true
				break
			}
		}
		if !hasDescendantInSubgraph {
			leaves = append(leaves, member)
		}
	}
	// Stable order so error messages and tests aren't map-iteration-flaky.
	sortStrings(leaves)
	return leaves
}

// FindHoldingTerminals returns the §11.4 terminal-leaf node types of the
// holding subgraph rooted at sourceNode. Exported so the supervisor's
// commit path can resolve the leaves at hold-source commit time and
// insert one rimsky_claim_holders row per leaf (per spec §5.6.3).
//
// storeName is reserved for future per-(source, store) leaf-set
// refinement; today the holding subgraph is store-independent. Returns
// an empty slice when sourceNode is unknown.
func FindHoldingTerminals(spec *TemplateSpec, sourceNode, storeName string) []string {
	return findHoldingTerminals(spec, sourceNode, storeName)
}

// leafResolves reports whether the leaf node carries a claim_resolutions
// entry for (source=sourceNode, store=storeName). The lookup is a simple
// linear scan — claim_resolutions lists are tiny in practice.
func leafResolves(spec *TemplateSpec, leafType, sourceNode, storeName string) bool {
	for _, n := range spec.Nodes {
		if n.Type != leafType {
			continue
		}
		for _, r := range n.ClaimResolutions {
			if r.Source == sourceNode && r.Store == storeName {
				return true
			}
		}
		return false
	}
	return false
}

// sortStrings is a tiny insertion sort so we don't pull in sort just for
// short slices. Stable, deterministic.
func sortStrings(s []string) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j-1] > s[j]; j-- {
			s[j-1], s[j] = s[j], s[j-1]
		}
	}
}

// detectCycles runs a depth-first search over Dependencies and records an
// error for each cycle found. Dependencies are node-type identifiers; unknown
// dependencies are already flagged by validateDependencies and are skipped
// here (treated as leaves).
func detectCycles(nodes []TemplateNodeDef, res *ValidationResult) {
	idx := make(map[string]TemplateNodeDef, len(nodes))
	for _, n := range nodes {
		idx[n.Type] = n
	}
	const (
		white = 0 // unvisited
		gray  = 1 // on current DFS stack
		black = 2 // fully explored
	)
	color := make(map[string]int, len(nodes))
	reported := make(map[string]bool)

	var visit func(typ string, stack []string)
	visit = func(typ string, stack []string) {
		color[typ] = gray
		stack = append(stack, typ)
		node, ok := idx[typ]
		if ok {
			for _, dep := range node.Dependencies {
				if _, known := idx[dep]; !known {
					continue
				}
				switch color[dep] {
				case white:
					visit(dep, stack)
				case gray:
					cycle := extractCycle(stack, dep)
					key := canonicalCycle(cycle)
					if !reported[key] {
						reported[key] = true
						res.Errors = append(res.Errors, ValidationError{
							Path: "nodes",
							Msg:  fmt.Sprintf("dependency cycle detected: %s", strings.Join(cycle, " -> ")),
						})
					}
				}
			}
		}
		color[typ] = black
	}

	for _, n := range nodes {
		if color[n.Type] == white {
			visit(n.Type, nil)
		}
	}
}

func extractCycle(stack []string, start string) []string {
	for i, s := range stack {
		if s == start {
			out := append([]string{}, stack[i:]...)
			return append(out, start)
		}
	}
	return append([]string{}, start)
}

func canonicalCycle(cycle []string) string {
	if len(cycle) == 0 {
		return ""
	}
	body := cycle
	if len(body) > 1 && body[0] == body[len(body)-1] {
		body = body[:len(body)-1]
	}
	if len(body) == 0 {
		return ""
	}
	minIdx := 0
	for i := 1; i < len(body); i++ {
		if body[i] < body[minIdx] {
			minIdx = i
		}
	}
	rot := make([]string, 0, len(body))
	for i := 0; i < len(body); i++ {
		rot = append(rot, body[(minIdx+i)%len(body)])
	}
	return strings.Join(rot, "|")
}
