// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package persistence

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"
)

type FilesystemBackend struct {
	root string
	seq  atomic.Uint64
}

var _ BlobBackend = (*FilesystemBackend)(nil)

func NewFilesystemBackend(root string) (*FilesystemBackend, error) {
	if root == "" {
		return nil, errors.New("blob filesystem: root must not be empty")
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		return nil, fmt.Errorf("blob filesystem: mkdir root: %w", err)
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("blob filesystem: resolve abs root: %w", err)
	}
	return &FilesystemBackend{root: abs}, nil
}

func (b *FilesystemBackend) Write(_ context.Context, key BlobKey, bytes []byte) (Handle, error) {
	rel := b.derivePath(key)
	abs := filepath.Join(b.root, rel)
	if !strings.HasPrefix(abs, b.root+string(filepath.Separator)) && abs != b.root {
		return "", fmt.Errorf("blob filesystem: derived path escapes root: %q", abs)
	}
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		return "", fmt.Errorf("blob filesystem: mkdir parent: %w", err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(abs), ".tmp-blob-")
	if err != nil {
		return "", fmt.Errorf("blob filesystem: create temp: %w", err)
	}
	tmpName := tmp.Name()
	if _, err := tmp.Write(bytes); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
		return "", fmt.Errorf("blob filesystem: write: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
		return "", fmt.Errorf("blob filesystem: fsync: %w", err)
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpName)
		return "", fmt.Errorf("blob filesystem: close temp: %w", err)
	}
	if err := os.Rename(tmpName, abs); err != nil {
		_ = os.Remove(tmpName)
		return "", fmt.Errorf("blob filesystem: rename: %w", err)
	}
	return Handle("fs:" + rel), nil
}

func (b *FilesystemBackend) Read(_ context.Context, handle Handle) ([]byte, error) {
	abs, err := b.absFromHandle(handle)
	if err != nil {
		return nil, err
	}
	bytes, err := os.ReadFile(abs)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, ErrBlobNotFound
		}
		return nil, fmt.Errorf("blob filesystem: read %q: %w", handle, err)
	}
	return bytes, nil
}

func (b *FilesystemBackend) ReadRange(_ context.Context, handle Handle, offset, length int64) ([]byte, error) {
	abs, err := b.absFromHandle(handle)
	if err != nil {
		return nil, err
	}
	if offset < 0 || length < 0 {
		return nil, fmt.Errorf("blob filesystem: ReadRange: negative offset=%d length=%d", offset, length)
	}
	f, err := os.Open(abs)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, ErrBlobNotFound
		}
		return nil, fmt.Errorf("blob filesystem: open %q: %w", handle, err)
	}
	defer func() { _ = f.Close() }()
	fi, err := f.Stat()
	if err != nil {
		return nil, fmt.Errorf("blob filesystem: stat %q: %w", handle, err)
	}
	if size := fi.Size(); offset > size || length > size-offset {
		return nil, io.ErrUnexpectedEOF
	}
	if _, err := f.Seek(offset, io.SeekStart); err != nil {
		return nil, fmt.Errorf("blob filesystem: seek: %w", err)
	}
	out := make([]byte, length)
	n, err := io.ReadFull(f, out)
	if err != nil {
		if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
			return nil, io.ErrUnexpectedEOF
		}
		return nil, fmt.Errorf("blob filesystem: read range: %w", err)
	}
	return out[:n], nil
}

func (b *FilesystemBackend) Delete(_ context.Context, handle Handle) error {
	abs, err := b.absFromHandle(handle)
	if err != nil {
		return err
	}
	if err := os.Remove(abs); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("blob filesystem: remove %q: %w", handle, err)
	}
	return nil
}

func (b *FilesystemBackend) Name() string { return "filesystem" }

func (b *FilesystemBackend) derivePath(key BlobKey) string {
	h := sha256.New()
	h.Write([]byte(key.NodeID))
	h.Write([]byte{0})
	h.Write([]byte(key.AttributeName))
	h.Write([]byte{0})
	h.Write([]byte(key.Hint))
	full := hex.EncodeToString(h.Sum(nil))
	a, c, rest := full[:2], full[2:4], full[4:]
	id := b.seq.Add(1)
	name := fmt.Sprintf("%s-%d-%d.bin", rest, time.Now().UnixNano(), id)
	return filepath.Join(a, c, name)
}

func (b *FilesystemBackend) absFromHandle(handle Handle) (string, error) {
	s := string(handle)
	if !strings.HasPrefix(s, "fs:") {
		return "", fmt.Errorf("blob filesystem: handle %q is not a filesystem handle", handle)
	}
	rel := strings.TrimPrefix(s, "fs:")
	abs := filepath.Join(b.root, filepath.Clean("/"+rel))
	if !strings.HasPrefix(abs, b.root+string(filepath.Separator)) && abs != b.root {
		return "", fmt.Errorf("blob filesystem: handle %q escapes root", handle)
	}
	return abs, nil
}
