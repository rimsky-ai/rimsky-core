// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package compose

import (
	"context"
	"strings"

	"github.com/rimsky-ai/rimsky-core/cmd/rimsky/cli"
)

type TagWithTemplate struct {
	Tag          string
	TemplateHash string
}

type ComposeState struct {
	Project      string
	Tags         []TagWithTemplate
	TemplatesByH map[string]cli.Template
	Instances    []cli.Instance
}

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

	insts, err := cli.PagedListInstances(ctx, c, cli.ListInstancesQuery{})
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
