package inlinejsonb

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/fallguy/rimsky/core/qualityrule"
	"github.com/fallguy/rimsky/core/resource"
	"github.com/fallguy/rimsky/core/shared"
	"github.com/fallguy/rimsky/core/storage"
)

// inlineResource is a Resource whose committed payloads live inline in the
// rimsky_resource_versions.data column. All persistence is delegated to the
// injected storage.ResourceRegistry.
type inlineResource struct {
	resourceID   shared.UUID
	path         []string
	ownerNodeID  shared.UUID
	keepVersions int
	rules        []qualityrule.Spec
	storage      storage.ResourceRegistry
}

// Path implements resource.Resource.
func (r *inlineResource) Path() []string { return r.path }

// OwnerNodeID implements resource.Resource.
func (r *inlineResource) OwnerNodeID() shared.UUID { return r.ownerNodeID }

// CurrentVersion implements resource.Resource.
func (r *inlineResource) CurrentVersion(ctx context.Context) (*resource.Version, error) {
	row, err := r.storage.Get(ctx, r.resourceID, nil)
	if err != nil {
		return nil, err
	}
	if row == nil || row.CurrentVersionID == nil {
		return nil, nil
	}
	v, err := r.storage.GetVersion(ctx, *row.CurrentVersionID, nil)
	if err != nil || v == nil {
		return nil, err
	}
	return toResourceVersion(*v), nil
}

// PreviousVersion implements resource.Resource.
func (r *inlineResource) PreviousVersion(ctx context.Context) (*resource.Version, error) {
	row, err := r.storage.Get(ctx, r.resourceID, nil)
	if err != nil {
		return nil, err
	}
	if row == nil || row.PreviousVersionID == nil {
		return nil, nil
	}
	v, err := r.storage.GetVersion(ctx, *row.PreviousVersionID, nil)
	if err != nil || v == nil {
		return nil, err
	}
	return toResourceVersion(*v), nil
}

// ListVersions implements resource.Resource.
func (r *inlineResource) ListVersions(ctx context.Context, limit int) ([]*resource.Version, error) {
	rows, err := r.storage.ListVersions(ctx, r.resourceID, nil)
	if err != nil {
		return nil, err
	}
	if limit > 0 && len(rows) > limit {
		rows = rows[:limit]
	}
	out := make([]*resource.Version, 0, len(rows))
	for _, v := range rows {
		out = append(out, toResourceVersion(v))
	}
	return out, nil
}

// Commit implements resource.Resource.
//
// Commit runs quality rules against the proposed new data and the current
// version's data (if any); on error-severity failure it returns
// CommitResult{Accepted: false}. On pass with Changed=false, NoOpCommit is
// invoked and no new version row is written. On pass with Changed=true, a
// new version is persisted and GC runs to keep at most keep_versions rows.
func (r *inlineResource) Commit(ctx context.Context, req resource.CommitRequest) (*resource.CommitResult, error) {
	// Serializability guard: json.Marshal early so non-serializable values
	// (channels, funcs, cyclic references) surface as a structured quality
	// failure rather than a panic or low-level storage error.
	data, err := json.Marshal(req.Result)
	if err != nil {
		return &resource.CommitResult{
			Accepted: false,
			QualityErrors: []qualityrule.Failure{{
				RuleType: "_serializer",
				Severity: shared.SeverityError,
				Details:  fmt.Sprintf("unserializable_result: %s", err.Error()),
			}},
		}, nil
	}

	// Load previous (if any) for quality-rule context.
	var prev any
	if cur, _ := r.CurrentVersion(ctx); cur != nil && cur.Data != nil {
		_ = json.Unmarshal(cur.Data, &prev)
	}
	var newParsed any
	_ = json.Unmarshal(data, &newParsed)

	errsList, warns, err := qualityrule.EvaluateAll(ctx, r.rules, qualityrule.EvalInput{
		NewData:      newParsed,
		PreviousData: prev,
	})
	_ = warns // TODO: emit warning events via caller-injected event writer
	if err != nil {
		return nil, fmt.Errorf("inlinejsonb: quality rules: %w", err)
	}
	if len(errsList) > 0 {
		return &resource.CommitResult{Accepted: false, QualityErrors: errsList}, nil
	}

	if !req.Changed {
		if err := r.storage.NoOpCommit(ctx, r.resourceID, nil); err != nil {
			return nil, err
		}
		return &resource.CommitResult{Accepted: true, Version: nil}, nil
	}

	vr, err := r.storage.CommitVersion(ctx, r.resourceID, storage.ResourceCommitInput{
		ProducedBy:    req.ProducedBy,
		Data:          data,
		ChangeSummary: req.ChangeSummary,
	}, nil)
	if err != nil {
		return nil, err
	}
	_, _ = r.storage.GCOldVersions(ctx, r.resourceID, r.keepVersions, nil)
	return &resource.CommitResult{Accepted: true, Version: toResourceVersion(vr)}, nil
}

// NoOpCommit implements resource.Resource.
func (r *inlineResource) NoOpCommit(ctx context.Context) error {
	return r.storage.NoOpCommit(ctx, r.resourceID, nil)
}

// RestoreVersion implements resource.Resource. If the storage layer cannot
// find the requested version (e.g. it has been GC'd past keep_versions), the
// returned error wraps resource.ErrRollbackUnsupported so callers can detect
// the class of failure via errors.Is.
func (r *inlineResource) RestoreVersion(ctx context.Context, target resource.VersionRef) (*resource.Version, error) {
	var targetStr string
	var targetID shared.UUID
	switch target.Kind {
	case "previous":
		targetStr = "previous"
	case "id":
		targetStr = "id"
		targetID = target.ID
	default:
		return nil, fmt.Errorf("inlinejsonb: unknown VersionRef.Kind %q", target.Kind)
	}
	vr, err := r.storage.RestoreVersion(ctx, r.resourceID, targetStr, targetID, nil)
	if err != nil {
		return nil, fmt.Errorf("inlinejsonb: restore: %w", errors.Join(err, resource.ErrRollbackUnsupported))
	}
	return toResourceVersion(vr), nil
}

func toResourceVersion(v storage.ResourceVersionRow) *resource.Version {
	return &resource.Version{
		ID:             v.ID,
		ProducedByNode: v.ProducedBy,
		Data:           v.Data,
		DataRef:        v.DataRef,
		ChangeSummary:  v.ChangeSummary,
		CommittedAt:    v.CommittedAt,
	}
}
