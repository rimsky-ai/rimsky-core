// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// @story: sensor-object-store
// @decision: object-store-watching-model
package main

import (
	"context"
	"encoding/hex"
	"fmt"
	"hash/fnv"
	"io"
	"os"
	"path/filepath"
	"strings"
)

type FilesystemLister struct {
	root string
}

func NewFilesystemLister(root string) *FilesystemLister {
	return &FilesystemLister{root: root}
}

func (l *FilesystemLister) List(_ context.Context, bucket, prefix string) ([]ObjectMeta, error) {
	bucketRoot := filepath.Join(l.root, bucket)
	info, err := os.Stat(bucketRoot)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("stat bucket %q: %w", bucketRoot, err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("bucket %q is not a directory", bucketRoot)
	}
	out := make([]ObjectMeta, 0)
	err = filepath.Walk(bucketRoot, func(p string, fi os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if fi.IsDir() {
			return nil
		}
		if !fi.Mode().IsRegular() {
			return nil
		}
		rel, err := filepath.Rel(bucketRoot, p)
		if err != nil {
			return fmt.Errorf("rel %q: %w", p, err)
		}
		name := filepath.ToSlash(rel)
		if prefix != "" && !strings.HasPrefix(name, prefix) {
			return nil
		}
		etag, err := hashFile(p)
		if err != nil {
			return fmt.Errorf("hash %q: %w", p, err)
		}
		out = append(out, ObjectMeta{
			Name:         name,
			LastModified: fi.ModTime(),
			Size:         fi.Size(),
			ETag:         etag,
		})
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

func hashFile(path string) (string, error) {
	f, err := os.Open(path) //nolint:gosec // explicit path from caller, no traversal.
	if err != nil {
		return "", err
	}
	defer func() { _ = f.Close() }()
	h := fnv.New64a()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
