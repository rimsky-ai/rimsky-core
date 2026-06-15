// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// filesystem_lister.go — local-filesystem ObjectLister. Treats a base
// directory as a flat object namespace: every regular file under
// `<root>/<bucket>/` whose name starts with the configured prefix is one
// "object." Subdirectories within the bucket are walked recursively;
// the object's `Name` is the path relative to the bucket root (forward-
// slash separated, matching S3 / GCS convention).
//
// Why this exists. The default bundled sensor image registers ONLY the
// in-memory backend, which has no externally-mutable surface — a test
// process can't drop new objects into the running sensor container's
// in-memory map. The filesystem backend gives the cross-stack scenario
// proof a real, externally-mutable backend: the test mounts a Docker
// volume into the sensor container's bucket root and the host process
// drops files into that volume to trigger emits. This also exhibits
// the sensor's pluggable-backend contract end-to-end (a backend
// registered via SetBackend produces real observations through the
// real poll loop), which is part of STORY-sensor-object-store.
//
// ETag derivation. The lister hashes the file contents with FNV-64a
// (cheap, deterministic, no crypto dependency) and renders the hash
// as a lowercase hex string. The hash is included in the
// publisher-subscription-id-keyed idempotency key the sensor sends to
// rimsky, so a file mutated in place (same name, new contents)
// produces a fresh idempotency key and re-emits. The watermark cursor
// (configured `watermark_field`) decides whether a name-equal /
// last_modified-equal object qualifies as "new" — ETag does not enter
// the watermark decision.
//
// @story: sensor-object-store
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

// FilesystemLister is a thread-safe ObjectLister that exposes the
// contents of a base directory as the object-store. Bucket names map
// to first-level subdirectories under the base root; objects are
// regular files anywhere under the bucket.
//
// The default bundled sensor binary registers this lister under the
// "filesystem" backend name when env RIMSKY_SENSOR_OBJECT_STORE_FS_ROOT
// is non-empty, so a deployment that wants a local-fs source needs
// no extra build step. Tests that need cross-stack proof of the
// pluggable-backend contract drive this same code path.
type FilesystemLister struct {
	root string
}

// NewFilesystemLister returns a lister rooted at the given base
// directory. The directory does not need to exist at construction —
// List returns an empty slice if `<root>/<bucket>` is absent, so the
// sensor does not error on a bucket that has not been created yet.
func NewFilesystemLister(root string) *FilesystemLister {
	return &FilesystemLister{root: root}
}

// List walks `<root>/<bucket>` and returns one ObjectMeta per regular
// file whose path (relative to the bucket root) starts with the
// configured prefix. Symlinks are not followed; subdirectories are
// walked recursively. The returned `Name` is the path relative to the
// bucket root with forward-slash separators (independent of OS path
// separator), matching S3 / GCS naming.
//
// Watermark fields (name, last_modified) consume the values directly
// off the filesystem: Name from the relative path, LastModified from
// the file's mtime, Size from the file's reported size, ETag from a
// FNV-64a hash of the file contents.
//
// Errors from the walk other than "bucket directory not found" are
// surfaced to the caller; the sensor's poll loop treats a List error
// as "skip this poll" (does not advance the watermark), so a transient
// IO error does not cause silent data loss.
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
		// @constraint: object names use forward slashes so the
		// watermark (a name comparison) stays stable across OS and
		// does not flip on path-separator differences.
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

// hashFile streams the file through FNV-64a and returns the hex digest.
// FNV is non-cryptographic but stable, fast, and dependency-free; the
// digest is what populates ObjectMeta.ETag, which the sensor folds
// into the rimsky-side idempotency key so a mutated file produces a
// fresh emit.
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
