package node

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/robfig/cron/v3"
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

// placeholderRe matches the v1 template placeholder grammar:
//
//	{instance_id}
//	{consumer_key}
//	{params.<identifier>}
//
// Any occurrence of `{...}` in a path segment, config value, or concurrency
// tag must match this pattern exactly, otherwise the template is rejected.
var placeholderRe = regexp.MustCompile(`\{(instance_id|consumer_key|params\.[a-zA-Z_][a-zA-Z0-9_]*)\}`)

// anyBraceRe matches any `{...}` segment (non-greedy). Used to locate
// candidate placeholders which are then checked against placeholderRe.
var anyBraceRe = regexp.MustCompile(`\{[^{}]*\}`)

// ValidateTemplate walks a parsed template and reports errors/warnings per
// spec §5.6.
//
// resourceImplExists is a lookup for registered resource implementations. The
// resource registry lives in core/resource (Task 8.1), so the node package
// cannot import it directly without creating a dependency cycle. Callers pass
// in a closure at the integration boundary.
func ValidateTemplate(spec *TemplateSpec, resourceImplExists func(name string) bool) ValidationResult {
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
		validateOwnsResources(n, base, resourceImplExists, &res)
		validateConcurrencyTags(n, base, &res)
	}

	detectCycles(spec.Nodes, &res)

	return res
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
	if len(n.OwnsResources) > 0 {
		res.Errors = append(res.Errors, ValidationError{
			Path: fmt.Sprintf("%s.owns_resources", base),
			Msg:  "pure-cascade node (empty executor) must not own resources",
		})
	}
	if len(n.Userdata) > 0 {
		res.Warnings = append(res.Warnings, ValidationWarning{
			Path: fmt.Sprintf("%s.userdata", base),
			Msg:  "pure-cascade node has userdata; userdata is only consumed by executors",
		})
	}
}

func validateOwnsResources(n TemplateNodeDef, base string, implExists func(name string) bool, res *ValidationResult) {
	for j, r := range n.OwnsResources {
		rbase := fmt.Sprintf("%s.owns_resources[%d]", base, j)
		if strings.TrimSpace(r.Implementation) == "" {
			res.Errors = append(res.Errors, ValidationError{
				Path: rbase + ".implementation",
				Msg:  "implementation is required",
			})
		} else if implExists != nil && !implExists(r.Implementation) {
			res.Errors = append(res.Errors, ValidationError{
				Path: rbase + ".implementation",
				Msg:  fmt.Sprintf("unknown resource implementation %q", r.Implementation),
			})
		}
		for pi, seg := range r.Path {
			checkPlaceholders(seg, fmt.Sprintf("%s.path[%d]", rbase, pi), res)
		}
		for key, v := range r.Config {
			checkPlaceholdersInValue(v, fmt.Sprintf("%s.config.%s", rbase, key), res)
		}
	}
}

func validateConcurrencyTags(n TemplateNodeDef, base string, res *ValidationResult) {
	for j, tag := range n.ConcurrencyTags {
		checkPlaceholders(tag, fmt.Sprintf("%s.concurrency_tags[%d]", base, j), res)
	}
}

// checkPlaceholders verifies every `{...}` segment in s matches the v1
// placeholder grammar.
func checkPlaceholders(s, path string, res *ValidationResult) {
	for _, m := range anyBraceRe.FindAllString(s, -1) {
		if !placeholderRe.MatchString(m) {
			res.Errors = append(res.Errors, ValidationError{
				Path: path,
				Msg:  fmt.Sprintf("invalid placeholder %q (expected {instance_id}, {consumer_key}, or {params.<key>})", m),
			})
		}
	}
}

// checkPlaceholdersInValue recursively walks arbitrary JSON-ish values looking
// for string placeholders. Non-string leaves are ignored.
func checkPlaceholdersInValue(v any, path string, res *ValidationResult) {
	switch t := v.(type) {
	case string:
		checkPlaceholders(t, path, res)
	case map[string]any:
		for k, vv := range t {
			checkPlaceholdersInValue(vv, path+"."+k, res)
		}
	case []any:
		for i, vv := range t {
			checkPlaceholdersInValue(vv, fmt.Sprintf("%s[%d]", path, i), res)
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
