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

// TestInlineBackend confirms the documented degenerate behavior:
// Write returns an error (caller should never reach it under correct
// spill-check usage); Read/ReadRange return ErrBlobNotFound; Delete is
// a no-op.
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

// TestMemoryBackend covers the round-trip path: write returns a unique
// handle, read returns the bytes, range read returns the slice, delete
// is idempotent, and post-delete reads return ErrBlobNotFound.
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

	// Mutate the original slice; cached bytes must remain intact.
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

// TestMemoryBackendReadRangeOutOfBounds confirms io.ErrUnexpectedEOF is
// returned when the requested range exceeds the stored size.
func TestMemoryBackendReadRangeOutOfBounds(t *testing.T) {
	t.Parallel()
	be := NewMemoryBackend()
	ctx := context.Background()
	h, _ := be.Write(ctx, BlobKey{}, []byte("short"))
	if _, err := be.ReadRange(ctx, h, 0, 100); !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("ReadRange out-of-bounds: want io.ErrUnexpectedEOF, got %v", err)
	}
}

// TestFilesystemBackend covers the round-trip path against a t.TempDir.
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
	payload := bytes.Repeat([]byte("0123456789abcdef"), 64) // 1 KiB
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

// TestFilesystemBackendRejectsPathEscape confirms a hand-crafted handle
// with directory escape sequences is rejected.
func TestFilesystemBackendRejectsPathEscape(t *testing.T) {
	t.Parallel()
	be, err := NewFilesystemBackend(t.TempDir())
	if err != nil {
		t.Fatalf("NewFilesystemBackend: %v", err)
	}
	if _, err := be.Read(context.Background(), Handle("fs:../../../etc/passwd")); err == nil {
		// Path is normalized into the root by filepath.Clean("/"+rel)
		// — should resolve to <root>/etc/passwd which doesn't exist.
		// Either ErrBlobNotFound or an explicit "escapes root" error is acceptable;
		// what is NOT acceptable is a successful read of /etc/passwd.
		t.Fatalf("expected error, got success on path-escape handle")
	}
}

// TestFilesystemBackendRejectsNonFsHandle confirms a Read on a non-fs:
// prefixed handle is rejected, not silently treated as a relative path.
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

// TestValidateBlobConfig covers the four backend names + the multi-process
// rejection on memory backend.
func TestValidateBlobConfig(t *testing.T) {
	cases := []struct {
		name     string
		cfg      BlobConfig
		role     string // value of RIMSKY_PROCESS_ROLE
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
