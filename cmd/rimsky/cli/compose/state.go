// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// state.go — query the control-api for compose-owned resources.
//
// `GET /tags` and `GET /instances` do not support prefix filtering server-
// side; the CLI lists the full set and filters client-side by the
// project's `compose:<project>:` prefix.
package compose

import (
	"context"
	"strings"

	"github.com/rimsky-ai/rimsky-core/cmd/rimsky/cli"
)

// TagWithTemplate pairs a tag with its currently-bound template hash.
type TagWithTemplate struct {
	Tag          string
	TemplateHash string
}

// ComposeState is the slice of control-api state visible to compose for
// a given project: tags whose names begin with `compose:<project>:`,
// the templates referenced by any such tag, and instances whose
// instance_key begins with `compose:<project>:`.
type ComposeState struct {
	Project      string
	Tags         []TagWithTemplate
	TemplatesByH map[string]cli.Template
	Instances    []cli.Instance
}

// QueryState lists the control-api's tags + instances and filters by
// the project prefix client-side.
func QueryState(ctx context.Context, c *cli.Client, project string) (*ComposeState, error) {
	prefix := cli.ReservedTagPrefix + project + ":"

	tags, err := pagedListTags(ctx, c)
	if err != nil {
		return nil, err
	}
	owned := []TagWithTemplate{}
	hashSet := map[string]bool{}
	for _, t := range tags {
		if !strings.HasPrefix(t.Tag, prefix) {
			continue
		}
		owned = append(owned, TagWithTemplate{Tag: t.Tag, TemplateHash: t.TemplateID})
		hashSet[t.TemplateID] = true
	}

	templates := map[string]cli.Template{}
	for h := range hashSet {
		tpl, err := c.GetTemplate(ctx, h)
		if err != nil {
			if cli.IsNotFound(err) {
				continue
			}
			return nil, err
		}
		templates[h] = *tpl
	}

	insts, err := pagedListInstances(ctx, c)
	if err != nil {
		return nil, err
	}
	ownedInsts := []cli.Instance{}
	for _, inst := range insts {
		if inst.InstanceKey == nil {
			continue
		}
		if strings.HasPrefix(*inst.InstanceKey, prefix) {
			ownedInsts = append(ownedInsts, inst)
		}
	}

	return &ComposeState{
		Project:      project,
		Tags:         owned,
		TemplatesByH: templates,
		Instances:    ownedInsts,
	}, nil
}

func pagedListTags(ctx context.Context, c *cli.Client) ([]cli.Tag, error) {
	var all []cli.Tag
	q := cli.ListTagsQuery{}
	for {
		page, err := c.ListTags(ctx, q)
		if err != nil {
			return nil, err
		}
		all = append(all, page.Tags...)
		if page.NextCursor == "" {
			break
		}
		q.Cursor = page.NextCursor
	}
	return all, nil
}

func pagedListInstances(ctx context.Context, c *cli.Client) ([]cli.Instance, error) {
	var all []cli.Instance
	q := cli.ListInstancesQuery{}
	for {
		page, err := c.ListInstances(ctx, q)
		if err != nil {
			return nil, err
		}
		all = append(all, page.Instances...)
		if page.NextCursor == "" {
			break
		}
		q.Cursor = page.NextCursor
	}
	return all, nil
}
