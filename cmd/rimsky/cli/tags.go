// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// tags.go — `tag create/list/get/mv/rm`.
//
// `tag get` does a list-and-filter against GET /tags because the
// control-api does not expose a per-tag GET endpoint (spec §1.6).
package cli

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"
)

// RunTagCreate implements `tag create`.
func RunTagCreate(ctx context.Context, args []string) int {
	var template string
	fs, common, endpoint, code := runWithCommon("tag create", args, func(fs *flag.FlagSet) {
		fs.StringVar(&template, "template", "", "tag or hash to point at")
	})
	if code != 0 {
		return code
	}
	rest := fs.Args()
	if len(rest) != 1 {
		fmt.Fprintln(os.Stderr, "usage: rimsky tag create <tag> --template <ref>")
		return 2
	}
	tag := rest[0]
	if strings.HasPrefix(tag, ReservedTagPrefix) {
		fmt.Fprintf(os.Stderr, "tag %q uses reserved prefix %q (managed by `compose`)\n", tag, ReservedTagPrefix)
		return 2
	}
	if template == "" {
		fmt.Fprintln(os.Stderr, "--template is required")
		return 2
	}
	c := NewClient(endpoint)
	c.SetAPIKey(common.ResolveAPIKey(os.Getenv("RIMSKY_API_KEY")))
	if _, err := c.CreateTag(ctx, CreateTagRequest{Tag: tag, Template: template}); err != nil {
		return reportError(err)
	}
	fmt.Fprintf(os.Stdout, "%s → %s\n", tag, template)
	return 0
}

// RunTagList implements `tag list`.
func RunTagList(ctx context.Context, args []string) int {
	var prefix string
	fs, common, endpoint, code := runWithCommon("tag list", args, func(fs *flag.FlagSet) {
		fs.StringVar(&prefix, "prefix", "", "client-side filter on tag prefix")
	})
	if code != 0 {
		return code
	}
	_ = fs
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
	if common.Format == FormatJSON {
		_ = EmitJSON(os.Stdout, all)
		return 0
	}
	rows := make([][]string, 0, len(all))
	for _, t := range all {
		rows = append(rows, []string{t.Tag, TruncHash(t.TemplateID)})
	}
	EmitTable(os.Stdout, []string{"TAG", "HASH"}, rows)
	return 0
}

func pagedListTags(ctx context.Context, c *Client, q ListTagsQuery) ([]Tag, error) {
	var all []Tag
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

// RunTagGet implements `tag get` via list-and-filter (spec §1.6).
func RunTagGet(ctx context.Context, args []string) int {
	fs, common, endpoint, code := runWithCommon("tag get", args, nil)
	if code != 0 {
		return code
	}
	rest := fs.Args()
	if len(rest) != 1 {
		fmt.Fprintln(os.Stderr, "usage: rimsky tag get <tag>")
		return 2
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
			if common.Format == FormatJSON {
				_ = EmitJSON(os.Stdout, t)
			} else {
				EmitKV(os.Stdout, [][2]string{
					{"tag", t.Tag},
					{"template_hash", t.TemplateID},
					{"updated_at", t.UpdatedAt},
				})
			}
			return 0
		}
	}
	fmt.Fprintln(os.Stderr, "tag not found")
	return 1
}

// RunTagMv implements `tag mv`. Refuses to operate on compose-owned
// tags (those with the reserved prefix); compose owns its tags' lifecycle
// and a manual move would silently violate that invariant. See spec §8.3.
func RunTagMv(ctx context.Context, args []string) int {
	var template string
	fs, common, endpoint, code := runWithCommon("tag mv", args, func(fs *flag.FlagSet) {
		fs.StringVar(&template, "template", "", "tag or hash to point at")
	})
	if code != 0 {
		return code
	}
	rest := fs.Args()
	if len(rest) != 1 {
		fmt.Fprintln(os.Stderr, "usage: rimsky tag mv <tag> --template <ref>")
		return 2
	}
	if strings.HasPrefix(rest[0], ReservedTagPrefix) {
		fmt.Fprintf(os.Stderr, "tag %q uses reserved prefix %q (managed by `compose`)\n", rest[0], ReservedTagPrefix)
		return 2
	}
	if template == "" {
		fmt.Fprintln(os.Stderr, "--template is required")
		return 2
	}
	c := NewClient(endpoint)
	c.SetAPIKey(common.ResolveAPIKey(os.Getenv("RIMSKY_API_KEY")))
	if _, err := c.MoveTag(ctx, rest[0], MoveTagRequest{Template: template}); err != nil {
		return reportError(err)
	}
	fmt.Fprintf(os.Stdout, "%s → %s\n", rest[0], template)
	return 0
}

// RunTagRm implements `tag rm`. Refuses to operate on compose-owned
// tags (those with the reserved prefix); compose owns its tags'
// lifecycle. See spec §8.3.
func RunTagRm(ctx context.Context, args []string) int {
	fs, common, endpoint, code := runWithCommon("tag rm", args, nil)
	if code != 0 {
		return code
	}
	rest := fs.Args()
	if len(rest) != 1 {
		fmt.Fprintln(os.Stderr, "usage: rimsky tag rm <tag>")
		return 2
	}
	if strings.HasPrefix(rest[0], ReservedTagPrefix) {
		fmt.Fprintf(os.Stderr, "tag %q uses reserved prefix %q (managed by `compose`)\n", rest[0], ReservedTagPrefix)
		return 2
	}
	c := NewClient(endpoint)
	c.SetAPIKey(common.ResolveAPIKey(os.Getenv("RIMSKY_API_KEY")))
	if err := c.DeleteTag(ctx, rest[0]); err != nil {
		return reportError(err)
	}
	fmt.Fprintf(os.Stdout, "%s removed\n", rest[0])
	return 0
}
