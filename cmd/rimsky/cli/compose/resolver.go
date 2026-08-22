// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

// @decision: template-identity-deployment-canonical
// @concept: template

package compose

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/rimsky-ai/rimsky-core/cmd/rimsky/cli"
	"github.com/rimsky-ai/rimsky-core/lib/graph/node"
	"github.com/rimsky-ai/rimsky-core/lib/graph/template/canonical"
	configload "github.com/rimsky-ai/rimsky-core/lib/protocols/config"
)

func LoadTemplateSpec(path string) (node.TemplateSpec, error) {
	var spec node.TemplateSpec
	if _, err := os.Stat(path); err != nil {
		return node.TemplateSpec{}, err
	}
	if err := configload.LoadFile(path, &spec); err != nil {
		return node.TemplateSpec{}, err
	}
	node.ApplyFrameResolutionDefaults(&spec)
	node.CanonicalizeAggregationPolicyDefault(&spec)
	return spec, nil
}

func ResolveTemplate(path string) (hash string, spec node.TemplateSpec, err error) {
	spec, err = LoadTemplateSpec(path)
	if err != nil {
		return "", node.TemplateSpec{}, err
	}
	hash, err = canonical.CanonicalSpecHash(spec)
	if err != nil {
		return "", node.TemplateSpec{}, err
	}
	return hash, spec, nil
}

// @decision: template-identity-deployment-canonical
type templateHashResolver interface {
	ValidateTemplate(ctx context.Context, body cli.RegisterTemplateRequest, warningsAsErrors bool) (*cli.ValidateResult, error)
}

// @decision: template-identity-deployment-canonical
// @concept: template
func ResolveTemplateThroughDeployment(
	ctx context.Context, c templateHashResolver, path string,
) (hash string, spec node.TemplateSpec, err error) {
	spec, err = LoadTemplateSpec(path)
	if err != nil {
		return "", node.TemplateSpec{}, err
	}
	res, err := c.ValidateTemplate(ctx, cli.RegisterTemplateRequest{Spec: spec}, false)
	if err != nil {
		return "", node.TemplateSpec{}, err
	}
	if res.TemplateHash == "" {
		return "", node.TemplateSpec{}, fmt.Errorf(
			"the deployment returned no canonical hash for %s; it owns the canonicalization, "+
				"so its answer is the template's identity: %s", path, validationSummary(res))
	}
	return res.TemplateHash, spec, nil
}

func validationSummary(res *cli.ValidateResult) string {
	if len(res.ValidationErrors) == 0 {
		return "no validation errors reported"
	}
	parts := make([]string, 0, len(res.ValidationErrors))
	for _, e := range res.ValidationErrors {
		parts = append(parts, e.Path+": "+e.Msg)
	}
	return strings.Join(parts, "; ")
}
