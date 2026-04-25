package inlinejsonb

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/fallguy/rimsky/core/qualityrule"
	"github.com/fallguy/rimsky/core/resource"
	"github.com/fallguy/rimsky/core/shared"
	"github.com/fallguy/rimsky/core/storage"
)

// ----------------------------------------------------------------------------
// In-memory fake storage.ResourceRegistry — pure-unit-test scaffolding.
// Avoids dependency on the Postgres impl (Task 6.3) which hasn't landed.
// ----------------------------------------------------------------------------

type fakeResourceRegistry struct {
	mu       sync.Mutex
	rows     map[shared.UUID]*storage.ResourceRow
	versions map[shared.UUID]*storage.ResourceVersionRow
	// ordered version IDs per resource (commit order, newest last)
	ordered map[shared.UUID][]shared.UUID
	now     func() time.Time
}

func newFakeRegistry() *fakeResourceRegistry {
	return &fakeResourceRegistry{
		rows:     map[shared.UUID]*storage.ResourceRow{},
		versions: map[shared.UUID]*storage.ResourceVersionRow{},
		ordered:  map[shared.UUID][]shared.UUID{},
		now:      time.Now,
	}
}

func (f *fakeResourceRegistry) Create(ctx context.Context, in storage.ResourceCreateInput, tx storage.Tx) (storage.ResourceRow, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	id := uuid.New()
	row := &storage.ResourceRow{
		ID:           id,
		ResourcePath: append([]string(nil), in.ResourcePath...),
		OwnerNodeID:  in.OwnerNodeID,
		KeepVersions: in.KeepVersions,
		CreatedAt:    f.now(),
	}
	f.rows[id] = row
	return *row, nil
}

func (f *fakeResourceRegistry) Get(ctx context.Context, id shared.UUID, tx storage.Tx) (*storage.ResourceRow, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	row, ok := f.rows[id]
	if !ok {
		return nil, nil
	}
	cp := *row
	return &cp, nil
}

func (f *fakeResourceRegistry) ListByOwner(ctx context.Context, ownerNodeID shared.UUID, tx storage.Tx) ([]storage.ResourceRow, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []storage.ResourceRow
	for _, r := range f.rows {
		if r.OwnerNodeID == ownerNodeID {
			out = append(out, *r)
		}
	}
	return out, nil
}

func (f *fakeResourceRegistry) CommitVersion(ctx context.Context, resourceID shared.UUID, in storage.ResourceCommitInput, tx storage.Tx) (storage.ResourceVersionRow, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	row, ok := f.rows[resourceID]
	if !ok {
		return storage.ResourceVersionRow{}, fmt.Errorf("fake: unknown resource %s", resourceID)
	}
	vid := uuid.New()
	producedBy := in.ProducedBy
	v := &storage.ResourceVersionRow{
		ID:            vid,
		ResourceID:    resourceID,
		ProducedBy:    &producedBy,
		Data:          append([]byte(nil), in.Data...),
		DataRef:       append([]byte(nil), in.DataRef...),
		ChangeSummary: in.ChangeSummary,
		CommittedAt:   f.now(),
	}
	f.versions[vid] = v
	f.ordered[resourceID] = append(f.ordered[resourceID], vid)
	// Roll pointers: old current → previous, new → current.
	if row.CurrentVersionID != nil {
		prev := *row.CurrentVersionID
		row.PreviousVersionID = &prev
	}
	row.CurrentVersionID = &vid
	return *v, nil
}

func (f *fakeResourceRegistry) NoOpCommit(ctx context.Context, resourceID shared.UUID, tx storage.Tx) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.rows[resourceID]; !ok {
		return fmt.Errorf("fake: unknown resource %s", resourceID)
	}
	// No-op: pointers unchanged, no version row written.
	return nil
}

func (f *fakeResourceRegistry) GCOldVersions(ctx context.Context, resourceID shared.UUID, keep int, tx storage.Tx) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	row, ok := f.rows[resourceID]
	if !ok {
		return 0, fmt.Errorf("fake: unknown resource %s", resourceID)
	}
	order := f.ordered[resourceID]
	if len(order) <= keep {
		return 0, nil
	}
	// Drop oldest, but never drop the current/previous pointer targets.
	protected := map[shared.UUID]struct{}{}
	if row.CurrentVersionID != nil {
		protected[*row.CurrentVersionID] = struct{}{}
	}
	if row.PreviousVersionID != nil {
		protected[*row.PreviousVersionID] = struct{}{}
	}
	removed := 0
	newOrder := make([]shared.UUID, 0, len(order))
	toDrop := len(order) - keep
	for _, vid := range order {
		if toDrop > 0 {
			if _, isProtected := protected[vid]; !isProtected {
				delete(f.versions, vid)
				toDrop--
				removed++
				continue
			}
		}
		newOrder = append(newOrder, vid)
	}
	f.ordered[resourceID] = newOrder
	return removed, nil
}

func (f *fakeResourceRegistry) RestoreVersion(ctx context.Context, resourceID shared.UUID, target string, versionID shared.UUID, tx storage.Tx) (storage.ResourceVersionRow, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	row, ok := f.rows[resourceID]
	if !ok {
		return storage.ResourceVersionRow{}, fmt.Errorf("fake: unknown resource %s", resourceID)
	}
	var pickID shared.UUID
	switch target {
	case "previous":
		if row.PreviousVersionID == nil {
			return storage.ResourceVersionRow{}, fmt.Errorf("fake: no previous version")
		}
		pickID = *row.PreviousVersionID
	case "id":
		if _, ok := f.versions[versionID]; !ok {
			return storage.ResourceVersionRow{}, fmt.Errorf("fake: version %s not found (gc'd)", versionID)
		}
		pickID = versionID
	default:
		return storage.ResourceVersionRow{}, fmt.Errorf("fake: unknown target %q", target)
	}
	v, ok := f.versions[pickID]
	if !ok {
		return storage.ResourceVersionRow{}, fmt.Errorf("fake: version %s not found", pickID)
	}
	// Swap: old current → previous, restored target → current.
	if row.CurrentVersionID != nil {
		prev := *row.CurrentVersionID
		row.PreviousVersionID = &prev
	}
	cur := pickID
	row.CurrentVersionID = &cur
	return *v, nil
}

func (f *fakeResourceRegistry) GetVersion(ctx context.Context, versionID shared.UUID, tx storage.Tx) (*storage.ResourceVersionRow, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	v, ok := f.versions[versionID]
	if !ok {
		return nil, nil
	}
	cp := *v
	return &cp, nil
}

func (f *fakeResourceRegistry) ListVersions(ctx context.Context, resourceID shared.UUID, tx storage.Tx) ([]storage.ResourceVersionRow, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	order := f.ordered[resourceID]
	out := make([]storage.ResourceVersionRow, 0, len(order))
	// Return newest-first.
	for i := len(order) - 1; i >= 0; i-- {
		if v, ok := f.versions[order[i]]; ok {
			out = append(out, *v)
		}
	}
	return out, nil
}

func (f *fakeResourceRegistry) ListVersionsPaged(ctx context.Context, resourceID shared.UUID, pag storage.ListPagination, tx storage.Tx) (storage.PaginatedListResult[storage.ResourceVersionRow], error) {
	rows, err := f.ListVersions(ctx, resourceID, tx)
	if err != nil {
		return storage.PaginatedListResult[storage.ResourceVersionRow]{}, err
	}
	return storage.PaginatedListResult[storage.ResourceVersionRow]{Rows: rows}, nil
}

// Compile-time check.
var _ storage.ResourceRegistry = (*fakeResourceRegistry)(nil)

// ----------------------------------------------------------------------------
// Helpers
// ----------------------------------------------------------------------------

// alwaysFailEvaluator is registered for use by tests that want a rule to
// always fail. Config can carry {"details": "..."} to customize output.
type alwaysFailEvaluator struct{}

func (alwaysFailEvaluator) Evaluate(_ context.Context, in qualityrule.EvalInput) (bool, string, error) {
	d, _ := in.Cfg["details"].(string)
	if d == "" {
		d = "always-fail-for-test"
	}
	return false, d, nil
}

func init() {
	qualityrule.Register("inlinejsonb_test_always_fail", alwaysFailEvaluator{})
}

// newTestResource allocates a resource in the fake registry and builds an
// inline-jsonb Resource bound to that ID via Factory.Create.
func newTestResource(t *testing.T, reg *fakeResourceRegistry, keepVersions int, rules []qualityrule.Spec) resource.Resource {
	t.Helper()
	ownerID := uuid.New()
	row, err := reg.Create(context.Background(), storage.ResourceCreateInput{
		ResourcePath: []string{"test", "x"},
		OwnerNodeID:  ownerID,
		KeepVersions: keepVersions,
	}, nil)
	require.NoError(t, err)

	f := Factory{StorageRegistry: reg}
	cfg := resource.Config{
		"keep_versions":   keepVersions,
		"_resource_id":    row.ID.String(),
		"_path":           []string{"test", "x"},
		"_owner_node_id":  ownerID.String(),
	}
	res, err := f.Create(cfg, rules, nil)
	require.NoError(t, err)
	return res
}

// ----------------------------------------------------------------------------
// Tests
// ----------------------------------------------------------------------------

func TestInlineJsonb_CommitHappyPath(t *testing.T) {
	reg := newFakeRegistry()
	res := newTestResource(t, reg, 2, nil)

	ctx := context.Background()
	out, err := res.Commit(ctx, resource.CommitRequest{
		ProducedBy:    uuid.New(),
		Result:        map[string]any{"rows": []any{"a", "b"}},
		Changed:       true,
		ChangeSummary: "initial",
	})
	require.NoError(t, err)
	require.True(t, out.Accepted)
	require.NotNil(t, out.Version)
	require.Equal(t, "initial", out.Version.ChangeSummary)

	cur, err := res.CurrentVersion(ctx)
	require.NoError(t, err)
	require.NotNil(t, cur)
	require.Equal(t, out.Version.ID, cur.ID)

	// Data round-trips.
	var got map[string]any
	require.NoError(t, json.Unmarshal(cur.Data, &got))
	require.Contains(t, got, "rows")
}

func TestInlineJsonb_CommitWithQualityRuleRejection(t *testing.T) {
	reg := newFakeRegistry()
	rules := []qualityrule.Spec{{
		Type:     "inlinejsonb_test_always_fail",
		Config:   map[string]any{"details": "nope"},
		Severity: shared.SeverityError,
	}}
	res := newTestResource(t, reg, 2, rules)

	ctx := context.Background()
	out, err := res.Commit(ctx, resource.CommitRequest{
		ProducedBy: uuid.New(),
		Result:     map[string]any{"x": 1},
		Changed:    true,
	})
	require.NoError(t, err)
	require.False(t, out.Accepted)
	require.Nil(t, out.Version)
	require.Len(t, out.QualityErrors, 1)
	require.Equal(t, "inlinejsonb_test_always_fail", out.QualityErrors[0].RuleType)
	require.Contains(t, out.QualityErrors[0].Details, "nope")

	// No version was written.
	cur, err := res.CurrentVersion(ctx)
	require.NoError(t, err)
	require.Nil(t, cur)
}

func TestInlineJsonb_NoOpCommit_DoesNotWriteVersion(t *testing.T) {
	reg := newFakeRegistry()
	res := newTestResource(t, reg, 2, nil)

	ctx := context.Background()
	// First commit a real version so we have a current pointer to compare.
	out, err := res.Commit(ctx, resource.CommitRequest{
		ProducedBy: uuid.New(),
		Result:     map[string]any{"v": 1},
		Changed:    true,
	})
	require.NoError(t, err)
	require.True(t, out.Accepted)
	firstID := out.Version.ID

	// No-op commit: Changed=false should not write a new version.
	out2, err := res.Commit(ctx, resource.CommitRequest{
		ProducedBy: uuid.New(),
		Result:     map[string]any{"v": 1},
		Changed:    false,
	})
	require.NoError(t, err)
	require.True(t, out2.Accepted)
	require.Nil(t, out2.Version)

	// Current pointer unchanged.
	cur, err := res.CurrentVersion(ctx)
	require.NoError(t, err)
	require.NotNil(t, cur)
	require.Equal(t, firstID, cur.ID)

	// Only one version total.
	list, err := res.ListVersions(ctx, 0)
	require.NoError(t, err)
	require.Len(t, list, 1)

	// Direct NoOpCommit method also works.
	require.NoError(t, res.NoOpCommit(ctx))
	list2, err := res.ListVersions(ctx, 0)
	require.NoError(t, err)
	require.Len(t, list2, 1)
}

func TestInlineJsonb_RestoreVersionPrevious_SwapsPointers(t *testing.T) {
	reg := newFakeRegistry()
	res := newTestResource(t, reg, 5, nil)

	ctx := context.Background()
	c1, err := res.Commit(ctx, resource.CommitRequest{ProducedBy: uuid.New(), Result: map[string]any{"n": 1}, Changed: true})
	require.NoError(t, err)
	c2, err := res.Commit(ctx, resource.CommitRequest{ProducedBy: uuid.New(), Result: map[string]any{"n": 2}, Changed: true})
	require.NoError(t, err)
	require.True(t, c1.Accepted && c2.Accepted)

	// Current=v2, Previous=v1. Restore "previous" should swap.
	restored, err := res.RestoreVersion(ctx, resource.VersionRef{Kind: "previous"})
	require.NoError(t, err)
	require.Equal(t, c1.Version.ID, restored.ID)

	cur, err := res.CurrentVersion(ctx)
	require.NoError(t, err)
	require.Equal(t, c1.Version.ID, cur.ID)

	prev, err := res.PreviousVersion(ctx)
	require.NoError(t, err)
	require.NotNil(t, prev)
	require.Equal(t, c2.Version.ID, prev.ID)
}

func TestInlineJsonb_RestoreVersionGCdIdReturnsRollbackUnsupported(t *testing.T) {
	reg := newFakeRegistry()
	res := newTestResource(t, reg, 2, nil)

	ctx := context.Background()
	// Attempt to restore a completely unknown version ID — the fake returns
	// "not found" and inline-jsonb wraps with ErrRollbackUnsupported.
	randomID := uuid.New()
	_, err := res.RestoreVersion(ctx, resource.VersionRef{Kind: "id", ID: randomID})
	require.Error(t, err)
	require.ErrorIs(t, err, resource.ErrRollbackUnsupported)
}

func TestInlineJsonb_KeepVersionsGCs_OldVersions(t *testing.T) {
	reg := newFakeRegistry()
	// keep_versions=2: after 4 commits, oldest 2 should be GC'd.
	res := newTestResource(t, reg, 2, nil)

	ctx := context.Background()
	var ids []shared.UUID
	for i := 0; i < 4; i++ {
		out, err := res.Commit(ctx, resource.CommitRequest{
			ProducedBy: uuid.New(),
			Result:     map[string]any{"i": i},
			Changed:    true,
		})
		require.NoError(t, err)
		require.True(t, out.Accepted)
		ids = append(ids, out.Version.ID)
	}

	// At most keep_versions rows should remain.
	list, err := res.ListVersions(ctx, 0)
	require.NoError(t, err)
	require.LessOrEqual(t, len(list), 2)

	// Oldest ID should not be reachable any more via GetVersion.
	got, err := reg.GetVersion(ctx, ids[0], nil)
	require.NoError(t, err)
	require.Nil(t, got, "expected oldest version to be GC'd")

	// Most recent two pointers still resolve.
	cur, err := res.CurrentVersion(ctx)
	require.NoError(t, err)
	require.NotNil(t, cur)
	require.Equal(t, ids[3], cur.ID)
	prev, err := res.PreviousVersion(ctx)
	require.NoError(t, err)
	require.NotNil(t, prev)
	require.Equal(t, ids[2], prev.ID)
}

func TestInlineJsonb_UnserializableResultRejected(t *testing.T) {
	reg := newFakeRegistry()
	res := newTestResource(t, reg, 2, nil)

	ctx := context.Background()
	// A channel is not JSON-serializable.
	bad := make(chan int)
	out, err := res.Commit(ctx, resource.CommitRequest{
		ProducedBy: uuid.New(),
		Result:     bad,
		Changed:    true,
	})
	require.NoError(t, err)
	require.False(t, out.Accepted)
	require.Nil(t, out.Version)
	require.Len(t, out.QualityErrors, 1)
	require.Equal(t, "_serializer", out.QualityErrors[0].RuleType)
	require.Equal(t, shared.SeverityError, out.QualityErrors[0].Severity)
	require.Contains(t, out.QualityErrors[0].Details, "unserializable_result")

	// Nothing committed.
	cur, err := res.CurrentVersion(ctx)
	require.NoError(t, err)
	require.Nil(t, cur)

	// Sanity: errors.Is works on ErrRollbackUnsupported wrapping (separate
	// tiny check to keep package-level import used).
	require.True(t, errors.Is(errors.Join(fmt.Errorf("x"), resource.ErrRollbackUnsupported), resource.ErrRollbackUnsupported))
}
