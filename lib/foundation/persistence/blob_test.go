// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package persistence

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"testing"
)

func TestInlineBackend(t *testing.T) {
	t.Parallel()
	be := InlineBackend{}
	if be.Name() != "inline" {
		t.Fatalf("Name: got %q, want inline", be.Name())
	}
	if _, err := be.Write(context.Background(), BlobKey{}, []byte("x")); err == nil {
		t.Fatalf("Write: want error, got nil")
	}
	if _, err := be.Read(context.Background(), Handle("anything")); !errors.Is(err, ErrBlobNotFound) {
		t.Fatalf("Read: want ErrBlobNotFound, got %v", err)
	}
	if _, err := be.ReadRange(context.Background(), Handle("anything"), 0, 1); !errors.Is(err, ErrBlobNotFound) {
		t.Fatalf("ReadRange: want ErrBlobNotFound, got %v", err)
	}
	if err := be.Delete(context.Background(), Handle("anything")); err != nil {
		t.Fatalf("Delete: want nil, got %v", err)
	}
}

func TestMemoryBackend(t *testing.T) {
	t.Parallel()
	be := NewMemoryBackend()
	if be.Name() != "memory" {
		t.Fatalf("Name: got %q, want memory", be.Name())
	}
	ctx := context.Background()
	payload := []byte("hello, blob world")
	h, err := be.Write(ctx, BlobKey{NodeID: "n", AttributeName: "a"}, payload)
	if err != nil {
		t.Fatalf("Write: %v", err)
	}
	if !strings.HasPrefix(string(h), "mem:") {
		t.Fatalf("handle prefix: got %q", h)
	}

	got, err := be.Read(ctx, h)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if !bytes.Equal(got, payload) {
		t.Fatalf("Read: bytes mismatch")
	}

	gotRange, err := be.ReadRange(ctx, h, 7, 4)
	if err != nil {
		t.Fatalf("ReadRange: %v", err)
	}
	if string(gotRange) != "blob" {
		t.Fatalf("ReadRange: got %q, want %q", string(gotRange), "blob")
	}

	payload[0] = 0xff
	got2, _ := be.Read(ctx, h)
	if got2[0] != 'h' {
		t.Fatalf("backend should hold its own copy; got %x", got2[0])
	}

	if err := be.Delete(ctx, h); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if err := be.Delete(ctx, h); err != nil {
		t.Fatalf("Delete (idempotent): %v", err)
	}
	if _, err := be.Read(ctx, h); !errors.Is(err, ErrBlobNotFound) {
		t.Fatalf("post-delete Read: want ErrBlobNotFound, got %v", err)
	}
}

func TestMemoryBackendReadRangeOutOfBounds(t *testing.T) {
	t.Parallel()
	be := NewMemoryBackend()
	ctx := context.Background()
	h, _ := be.Write(ctx, BlobKey{}, []byte("short"))
	if _, err := be.ReadRange(ctx, h, 0, 100); !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("ReadRange out-of-bounds: want io.ErrUnexpectedEOF, got %v", err)
	}
}

func TestFilesystemBackend(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	be, err := NewFilesystemBackend(root)
	if err != nil {
		t.Fatalf("NewFilesystemBackend: %v", err)
	}
	if be.Name() != "filesystem" {
		t.Fatalf("Name: got %q, want filesystem", be.Name())
	}
	ctx := context.Background()
	payload := bytes.Repeat([]byte("0123456789abcdef"), 64)
	h, err := be.Write(ctx, BlobKey{NodeID: "n1", AttributeName: "value"}, payload)
	if err != nil {
		t.Fatalf("Write: %v", err)
	}
	if !strings.HasPrefix(string(h), "fs:") {
		t.Fatalf("handle prefix: got %q", h)
	}

	got, err := be.Read(ctx, h)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if !bytes.Equal(got, payload) {
		t.Fatalf("Read: bytes mismatch")
	}

	gotRange, err := be.ReadRange(ctx, h, 16, 16)
	if err != nil {
		t.Fatalf("ReadRange: %v", err)
	}
	if !bytes.Equal(gotRange, []byte("0123456789abcdef")) {
		t.Fatalf("ReadRange: got %q, want %q", gotRange, "0123456789abcdef")
	}

	if err := be.Delete(ctx, h); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if err := be.Delete(ctx, h); err != nil {
		t.Fatalf("Delete (idempotent): %v", err)
	}
	if _, err := be.Read(ctx, h); !errors.Is(err, ErrBlobNotFound) {
		t.Fatalf("post-delete Read: want ErrBlobNotFound, got %v", err)
	}
}

func TestFilesystemBackendReadRangeOutOfBounds(t *testing.T) {
	t.Parallel()
	be, err := NewFilesystemBackend(t.TempDir())
	if err != nil {
		t.Fatalf("NewFilesystemBackend: %v", err)
	}
	ctx := context.Background()
	h, err := be.Write(ctx, BlobKey{}, []byte("short"))
	if err != nil {
		t.Fatalf("Write: %v", err)
	}
	if _, err := be.ReadRange(ctx, h, 0, 100); !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("ReadRange out-of-bounds: want io.ErrUnexpectedEOF, got %v", err)
	}
}

func TestFilesystemBackendReadRangeNotFound(t *testing.T) {
	t.Parallel()
	be, err := NewFilesystemBackend(t.TempDir())
	if err != nil {
		t.Fatalf("NewFilesystemBackend: %v", err)
	}
	if _, err := be.ReadRange(context.Background(), Handle("fs:aa/bb/nonexistent.bin"), 0, 1); !errors.Is(err, ErrBlobNotFound) {
		t.Fatalf("ReadRange on missing handle: want ErrBlobNotFound, got %v", err)
	}
}

func TestMemoryBackendReadRangeNotFound(t *testing.T) {
	t.Parallel()
	be := NewMemoryBackend()
	if _, err := be.ReadRange(context.Background(), Handle("mem:404"), 0, 1); !errors.Is(err, ErrBlobNotFound) {
		t.Fatalf("ReadRange on missing handle: want ErrBlobNotFound, got %v", err)
	}
}

func TestFilesystemBackendRejectsPathEscape(t *testing.T) {
	t.Parallel()
	be, err := NewFilesystemBackend(t.TempDir())
	if err != nil {
		t.Fatalf("NewFilesystemBackend: %v", err)
	}
	if _, err := be.Read(context.Background(), Handle("fs:../../../etc/passwd")); err == nil {
		t.Fatalf("expected error, got success on path-escape handle")
	}
}

func TestFilesystemBackendRejectsNonFsHandle(t *testing.T) {
	t.Parallel()
	be, err := NewFilesystemBackend(t.TempDir())
	if err != nil {
		t.Fatalf("NewFilesystemBackend: %v", err)
	}
	if _, err := be.Read(context.Background(), Handle("mem:1")); err == nil {
		t.Fatalf("expected error on non-fs: handle, got nil")
	}
}

func TestValidateBlobConfig(t *testing.T) {
	cases := []struct {
		name     string
		cfg      BlobConfig
		role     string
		wantErr  bool
		wantWrap error
	}{
		{name: "default-inline", cfg: DefaultBlobConfig(), wantErr: false},
		{name: "explicit-inline", cfg: BlobConfig{Backend: "inline", SpillThresholdBytes: 1024}, wantErr: false},
		{name: "filesystem-with-root", cfg: BlobConfig{Backend: "filesystem", Filesystem: FilesystemBlobConfig{Root: "/tmp/blob-test"}}, wantErr: false},
		{name: "filesystem-missing-root", cfg: BlobConfig{Backend: "filesystem"}, wantErr: true, wantWrap: ErrInvalidBlobConfig},
		{name: "unknown-backend", cfg: BlobConfig{Backend: "s3"}, wantErr: true, wantWrap: ErrInvalidBlobConfig},
		{name: "negative-threshold", cfg: BlobConfig{Backend: "inline", SpillThresholdBytes: -1}, wantErr: true, wantWrap: ErrInvalidBlobConfig},
		{name: "memory-non-unified", cfg: BlobConfig{Backend: "memory"}, role: "scheduler", wantErr: true, wantWrap: ErrInvalidBlobConfig},
		{name: "memory-empty-role", cfg: BlobConfig{Backend: "memory"}, role: "", wantErr: true, wantWrap: ErrInvalidBlobConfig},
		{name: "memory-unified", cfg: BlobConfig{Backend: "memory"}, role: "unified", wantErr: false},
		{name: "pglo-permitted", cfg: BlobConfig{Backend: "pg-largeobject"}, wantErr: false},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv(ProcessRoleEnv, tc.role)
			err := ValidateBlobConfig(tc.cfg)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("want error, got nil")
				}
				if tc.wantWrap != nil && !errors.Is(err, tc.wantWrap) {
					t.Fatalf("want errors.Is(%v), got %v", tc.wantWrap, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("want nil, got %v", err)
			}
		})
	}
}
