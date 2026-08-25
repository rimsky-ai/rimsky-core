// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package cli

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"
)

func RunTagCreate(ctx context.Context, args []string) int {
	var template string
	fs, common, endpoint, code := runWithCommon("tag create", "<tag> --template <ref>", NoTable, args, func(fs *flag.FlagSet) {
		fs.StringVar(&template, "template", "", "tag or hash to point at")
	})
	if common == nil {
		return code
	}
	rest := fs.Args()
	if len(rest) != 1 {
		return UsageError(fs)
	}
	tag := rest[0]
	if template == "" {
		fmt.Fprintln(os.Stderr, "--template is required")
		return 2
	}
	c := NewClient(endpoint)
	c.SetAPIKey(common.ResolveAPIKey(os.Getenv("RIMSKY_API_KEY")))
	created, err := c.CreateTag(ctx, CreateTagRequest{Tag: tag, Template: template})
	if err != nil {
		return reportError(err)
	}
	return reportTagBinding(common.Format, created, tag, template)
}

func RunTagList(ctx context.Context, args []string) int {
	return runTagListNamed(ctx, "tag list", args)
}

func runTagListNamed(ctx context.Context, name string, args []string) int {
	var prefix string
	_, common, endpoint, code := runWithCommon(name, "[--prefix <prefix>]", HasTable, args, func(fs *flag.FlagSet) {
		fs.StringVar(&prefix, "prefix", "", "client-side filter on tag prefix")
	})
	if common == nil {
		return code
	}
	c := NewClient(endpoint)
	c.SetAPIKey(common.ResolveAPIKey(os.Getenv("RIMSKY_API_KEY")))
	all, err := pagedListTags(ctx, c, ListTagsQuery{})
	if err != nil {
		return reportError(err)
	}
	if prefix != "" {
		filtered := all[:0]
		for _, t := range all {
			if strings.HasPrefix(t.Tag, prefix) {
				filtered = append(filtered, t)
			}
		}
		all = filtered
	}
	return Render(common.Format, all, func() {
		rows := make([][]string, 0, len(all))
		for _, t := range all {
			rows = append(rows, []string{t.Tag, TruncHash(t.TemplateID)})
		}
		EmitTable(os.Stdout, []string{"TAG", "HASH"}, rows)
	})
}

func pagedListTags(ctx context.Context, c *Client, q ListTagsQuery) ([]Tag, error) {
	return PageAll(func(cursor string) ([]Tag, string, error) {
		q.Cursor = cursor
		page, err := c.ListTags(ctx, q)
		if err != nil {
			return nil, "", err
		}
		return page.Tags, page.NextCursor, nil
	})
}

func RunTagGet(ctx context.Context, args []string) int {
	fs, common, endpoint, code := runWithCommon("tag get", "<tag>", NoTable, args, nil)
	if common == nil {
		return code
	}
	rest := fs.Args()
	if len(rest) != 1 {
		return UsageError(fs)
	}
	want := rest[0]
	c := NewClient(endpoint)
	c.SetAPIKey(common.ResolveAPIKey(os.Getenv("RIMSKY_API_KEY")))
	all, err := pagedListTags(ctx, c, ListTagsQuery{})
	if err != nil {
		return reportError(err)
	}
	for _, t := range all {
		if t.Tag == want {
			return Render(common.Format, t, func() {
				EmitKV(os.Stdout, [][2]string{
					{"tag", t.Tag},
					{"template_hash", t.TemplateID},
					{"updated_at", t.UpdatedAt},
				})
			})
		}
	}
	fmt.Fprintln(os.Stderr, "tag not found")
	return 1
}

func RunTagMv(ctx context.Context, args []string) int {
	var template string
	fs, common, endpoint, code := runWithCommon("tag mv", "<tag> --template <ref>", NoTable, args, func(fs *flag.FlagSet) {
		fs.StringVar(&template, "template", "", "tag or hash to point at")
	})
	if common == nil {
		return code
	}
	rest := fs.Args()
	if len(rest) != 1 {
		return UsageError(fs)
	}
	if template == "" {
		fmt.Fprintln(os.Stderr, "--template is required")
		return 2
	}
	c := NewClient(endpoint)
	c.SetAPIKey(common.ResolveAPIKey(os.Getenv("RIMSKY_API_KEY")))
	moved, err := c.MoveTag(ctx, rest[0], MoveTagRequest{Template: template})
	if err != nil {
		return reportError(err)
	}
	return reportTagBinding(common.Format, moved, rest[0], template)
}

func RunTagRm(ctx context.Context, args []string) int {
	fs, common, endpoint, code := runWithCommon("tag rm", "<tag>", NoTable, args, nil)
	if common == nil {
		return code
	}
	rest := fs.Args()
	if len(rest) != 1 {
		return UsageError(fs)
	}
	if !ConfirmDestructiveTargets(common.Yes, "remove tag "+rest[0]) {
		return 2
	}
	c := NewClient(endpoint)
	c.SetAPIKey(common.ResolveAPIKey(os.Getenv("RIMSKY_API_KEY")))
	if err := c.DeleteTag(ctx, rest[0]); err != nil {
		return reportError(err)
	}
	return reportRemoval(common.Format, removalResult{Ref: rest[0], Removed: true},
		fmt.Sprintf("%s removed", rest[0]))
}
