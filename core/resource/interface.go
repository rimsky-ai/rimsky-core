// Package resource defines the Resource and Factory interfaces plus shared
// types for the pluggable resource library. Concrete implementations (e.g.
// inline-jsonb, external-sql) live in sub-packages and register via the
// Factory registry at package init time or via explicit consumer wiring.
package resource

import (
	"context"
	"time"

	"github.com/fallguy/rimsky/core/qualityrule"
	"github.com/fallguy/rimsky/core/shared"
)

// Config is the placeholder-resolved, schema-validated config block from the
// template's owns_resources[].config, passed as a generic decoded JSON map.
// Implementations cast fields to their expected Go types; ConfigSchema()
// guarantees shape.
type Config = map[string]any

// Version is a single committed version of a resource.
type Version struct {
	ID             shared.UUID
	ProducedByNode *shared.UUID // nullable (FK ON DELETE SET NULL)
	Data           []byte       // JSON bytes for inline; nil for external
	DataRef        []byte       // JSON-encoded ref for external; nil for inline
	ChangeSummary  string
	CommittedAt    time.Time
}

// CommitRequest carries a new result for the Resource to consider committing.
type CommitRequest struct {
	ProducedBy    shared.UUID
	Result        any // marshaled to JSON by inline-jsonb impl
	Changed       bool
	ChangeSummary string
}

// CommitResult is the outcome of a Commit call. On accepted, Version is
// populated. On rejected, QualityErrors carries the severity=error failures.
type CommitResult struct {
	Accepted      bool
	Version       *Version
	QualityErrors []qualityrule.Failure
}

// VersionRef identifies a rollback target.
//
//	Kind=="previous" → swap to the previous version pointer.
//	Kind=="id"       → restore the specific version by ID (if it hasn't been
//	                   garbage-collected).
type VersionRef struct {
	Kind string
	ID   shared.UUID
}

// Resource is the pluggable implementation of a node's owned resource. Each
// instance corresponds to one row in rimsky_resources. Implementations are
// constructed via their Factory at instance-provisioning time.
type Resource interface {
	Path() []string
	OwnerNodeID() shared.UUID

	CurrentVersion(ctx context.Context) (*Version, error)
	PreviousVersion(ctx context.Context) (*Version, error)
	ListVersions(ctx context.Context, limit int) ([]*Version, error)

	// Commit runs the configured quality rules internally; on error-severity
	// failure returns CommitResult{Accepted: false, QualityErrors: ...}.
	// Warning-severity failures are logged internally and do not populate
	// QualityErrors.
	Commit(ctx context.Context, req CommitRequest) (*CommitResult, error)

	// NoOpCommit signals a successful run with Changed=false; no version
	// row is written; pointers remain unchanged.
	NoOpCommit(ctx context.Context) error

	// RestoreVersion attempts a rollback. If the implementation cannot
	// support rollback for the given target, returns ErrRollbackUnsupported.
	RestoreVersion(ctx context.Context, target VersionRef) (*Version, error)
}

// Registry is the subset of the Postgres-backed ResourceRegistry surface a
// Resource implementation needs at runtime (to allocate IDs, update version
// pointers, etc.). Implementations are passed a Registry at Create time.
// Concrete wiring from storage.ResourceRegistry to Registry is done by the
// consumer's main or by instance-factory at provisioning time.
type Registry interface {
	AllocResource(ctx context.Context, path []string, ownerNodeID shared.UUID, keepVersions int) (shared.UUID, error)
	SetCurrentVersion(ctx context.Context, resourceID, versionID shared.UUID) error
}

// QualityRuleSpec mirrors qualityrule.Spec — re-exported here for factory
// convenience (so implementations can depend on just core/resource).
type QualityRuleSpec = qualityrule.Spec

// Factory constructs a Resource given its template-level config and quality
// rules. One Factory is registered per implementation name ("inline-jsonb",
// "external-sql", ...).
type Factory interface {
	// ConfigSchema returns a JSON Schema describing the template's config
	// block for this implementation. Used by template validation (§5.6).
	ConfigSchema() []byte

	// Create is called at instance provisioning. cfg is validated +
	// placeholder-resolved. rules is the per-node quality_rules block bound
	// into the Resource once at creation. reg is the shared registry.
	Create(cfg Config, rules []QualityRuleSpec, reg Registry) (Resource, error)
}
