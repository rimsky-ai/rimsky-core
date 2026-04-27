// Lock-spec construction + template lookup helpers extracted from
// runner_acquire.go for the cold-read 500-line file guideline.
//
// Functions here are pure (or near-pure) translations between the
// template's per-node-type lock declarations and the concrete
// store.LockSpec / store.LockHolderRow shapes the acquisition tx
// needs:
//
//   - sortLockSpecs / sortKeyForSpec / storeNameForSpec — the §13.7
//     deterministic ordering used by every step that walks the spec
//     slice (advisory locks 3b, region re-eval 3d, AcquireLock loop
//     3e, the terminal handler's release loop).
//   - buildLockSpecs / substituteSlice — translate
//     TemplateNodeDef.Stores+Locks into LockSpec values, running
//     `{{params.x}}` / `{{deps.x.field}}` substitution per §10.2.
//   - loadDepsAttributes — the dep-attributes lookup used by
//     substitution; mirrored at dispatch time by
//     loadDepsAttributesByID in runner_dispatch.go.
//   - lookupTemplate / lookupNodeDef — convenience wrappers around the
//     storage backend for the per-instance template + node-def.
//   - mustParseUUID — panic-on-corrupt-state helper for UUIDs we
//     previously stringified ourselves.
//
// No new behaviour; these are exact moves to keep runner_acquire.go
// focused on the §13.3 transaction.

package supervisor

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"

	"github.com/google/uuid"

	"github.com/fallguy/rimsky/core/attributes"
	"github.com/fallguy/rimsky/core/node"
	"github.com/fallguy/rimsky/core/shared"
	"github.com/fallguy/rimsky/core/storage"
	"github.com/fallguy/rimsky/core/store"
)

// sortLockSpecs orders specs by (kind, sort_key) per §13.7.
func sortLockSpecs(specs []store.LockSpec) {
	sort.SliceStable(specs, func(i, j int) bool {
		ki, kj := specs[i].Kind(), specs[j].Kind()
		if ki != kj {
			return ki < kj
		}
		return sortKeyForSpec(specs[i]) < sortKeyForSpec(specs[j])
	})
}

// sortKeyForSpec computes the §13.7 sort key for a spec.
func sortKeyForSpec(sp store.LockSpec) string {
	switch v := sp.(type) {
	case store.NamedLockSpec:
		return v.Name
	case store.RegionLockSpec:
		b, _ := json.Marshal(v.Region)
		return v.StoreName + ":" + string(b)
	case store.ClaimLockSpec:
		return v.StoreName
	}
	return ""
}

// storeNameForSpec returns the store name for region/claim specs, or
// "" for named locks.
func storeNameForSpec(sp store.LockSpec) string {
	switch v := sp.(type) {
	case store.RegionLockSpec:
		return v.StoreName
	case store.ClaimLockSpec:
		return v.StoreName
	}
	return ""
}

// buildLockSpecs translates the template's per-node-type Stores+Locks
// declarations into concrete LockSpec values. Substitutes
// `{{params.x}}` etc. into region patterns and named-lock names per
// §10.2.
func buildLockSpecs(
	ctx context.Context, args RunArgs,
	nd *storage.NodeRow, def *node.TemplateNodeDef, inst *storage.InstanceRow,
) ([]store.LockSpec, error) {
	if def == nil {
		return nil, nil
	}
	var params map[string]any
	if inst != nil {
		params = inst.Params
	}
	resolveCtx := attributes.ResolveContext{
		Params: params,
		Deps:   loadDepsAttributes(ctx, args, nd),
	}

	out := make([]store.LockSpec, 0, len(def.Locks)+len(def.Stores))
	for _, l := range def.Locks {
		nameSub, err := attributes.Substitute(l.Name, resolveCtx)
		if err != nil {
			return nil, err
		}
		out = append(out, store.NamedLockSpec{
			Name:  nameSub,
			Mode:  l.Mode,
			Limit: l.Limit,
		})
	}
	for _, sref := range def.Stores {
		write, err := substituteSlice(sref.Write, resolveCtx)
		if err != nil {
			return nil, err
		}
		read, err := substituteSlice(sref.Read, resolveCtx)
		if err != nil {
			return nil, err
		}
		if sref.Claim {
			out = append(out, store.ClaimLockSpec{
				StoreName: sref.Name,
				Hold:      sref.Hold,
				OnCommit:  sref.OnCommit,
				OnGiveUp:  sref.OnGiveUp,
				Resumable: sref.Resumable,
			})
		}
		if len(write) > 0 || len(read) > 0 {
			out = append(out, store.RegionLockSpec{
				StoreName:  sref.Name,
				Region:     write,
				ReadRegion: read,
				Resumable:  sref.Resumable,
			})
		}
	}
	return out, nil
}

func substituteSlice(in []string, ctx attributes.ResolveContext) ([]string, error) {
	if len(in) == 0 {
		return nil, nil
	}
	out := make([]string, 0, len(in))
	for _, s := range in {
		v, err := attributes.Substitute(s, ctx)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, nil
}

// loadDepsAttributes pulls each upstream node's
// rimsky_node_attributes.data into a map keyed by the upstream's
// node_type. Used by region/lock-name substitution; the dispatch path
// uses the same shape.
func loadDepsAttributes(ctx context.Context, args RunArgs, nd *storage.NodeRow) map[string]map[string]any {
	if nd == nil || len(nd.Dependencies) == 0 {
		return nil
	}
	out := make(map[string]map[string]any, len(nd.Dependencies))
	for _, depID := range nd.Dependencies {
		depNode, _ := args.Storage.Nodes().Get(ctx, depID, nil)
		if depNode == nil {
			continue
		}
		row, err := args.Storage.NodeAttributes().Get(ctx, depNode.ID)
		if err != nil || row == nil {
			continue
		}
		out[depNode.NodeType] = row.Data
	}
	return out
}

// lookupTemplate fetches the template for an instance, or nil on miss.
func lookupTemplate(ctx context.Context, args RunArgs, inst *storage.InstanceRow) *node.TemplateSpec {
	if inst == nil {
		return nil
	}
	tmpl, _ := args.Storage.Templates().Get(ctx, inst.TemplateID, nil)
	if tmpl == nil {
		return nil
	}
	return &tmpl.Spec
}

// lookupNodeDef returns the per-node-type def from a template, or nil.
func lookupNodeDef(tmpl *node.TemplateSpec, nodeType string) *node.TemplateNodeDef {
	if tmpl == nil {
		return nil
	}
	for i := range tmpl.Nodes {
		if tmpl.Nodes[i].Type == nodeType {
			return &tmpl.Nodes[i]
		}
	}
	return nil
}

// mustParseUUID is a panic-on-failure helper. Used only when parsing a
// UUID we previously stringified ourselves; if parsing fails the caller
// has corrupted state.
func mustParseUUID(s string) shared.UUID {
	u, err := uuid.Parse(s)
	if err != nil {
		panic(fmt.Sprintf("mustParseUUID: %v", err))
	}
	return u
}
