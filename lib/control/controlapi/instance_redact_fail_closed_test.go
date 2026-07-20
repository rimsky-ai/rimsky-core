// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package controlapi

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/spec"
	pgtest "github.com/rimsky-ai/rimsky-core/test/support/pgmigrate"
)

type failingTemplateTable struct {
	persistence.TemplateTable
	err error
}

func (f failingTemplateTable) GetByHash(ctx context.Context, hash string, tx persistence.Tx) (*persistence.TemplateRow, error) {
	return nil, f.err
}

type templateLoadFailingTables struct {
	persistence.Tables
	err error
}

func (t templateLoadFailingTables) Templates() persistence.TemplateTable {
	return failingTemplateTable{TemplateTable: t.Tables.Templates(), err: t.err}
}

func TestInstanceRedact_FailsClosedOnTemplateLoadError(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	d := pgtest.OpenDriver(ctx, t)

	deps := AppDeps{
		Persist: templateLoadFailingTables{Tables: d.Tables(), err: errors.New("injected template load failure")},
		Logger:  shared.NewCapturingLogger(),
	}
	redact := instanceRedact(ctx, deps, "sha256-doesnotmatter", shared.UUID(uuid.New()))
	require.Equal(t, []string{RedactAllParamsSentinel}, redact,
		"a template-load error must fail closed (redact everything), not fail open (redact nothing)")
}

func TestInstanceRedact_FailsClosedOnTemplateNotFound(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	d := pgtest.OpenDriver(ctx, t)

	redact := instanceRedact(ctx, AppDeps{Persist: d.Tables(), Logger: shared.NewCapturingLogger()},
		"sha256-"+uuid.NewString(), shared.UUID(uuid.New()))
	require.Equal(t, []string{RedactAllParamsSentinel}, redact,
		"an unresolvable template hash must fail closed (redact everything)")
}

func TestInstanceRedact_ReturnsDeclaredParamsRedactOnSuccess(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	d := pgtest.OpenDriver(ctx, t)

	hash := "sha256-" + uuid.NewString()
	require.NoError(t, d.Tables().Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		return d.Tables().Templates().Insert(ctx, persistence.TemplateInsertInput{
			ID: hash,
			Spec: spec.TemplateSpec{
				Name:         "redact-fixture",
				Version:      "v1",
				ParamsRedact: []string{"credentials.token"},
			},
			State: persistence.TemplateStateRegistered,
		}, tx)
	}))

	redact := instanceRedact(ctx, AppDeps{Persist: d.Tables(), Logger: shared.NewCapturingLogger()},
		hash, shared.UUID(uuid.New()))
	require.Equal(t, []string{"credentials.token"}, redact)
}
