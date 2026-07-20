// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package persistence_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/protocols/conformance/blobbackend"
)

type blobBackendAdapter struct {
	be persistence.BlobBackend
}

func (a *blobBackendAdapter) Write(ctx context.Context, hint string, bytes []byte) (blobbackend.Handle, error) {
	h, err := a.be.Write(ctx, persistence.BlobKey{Hint: hint}, bytes)
	if err != nil {
		return "", err
	}
	return blobbackend.Handle(h), nil
}

func (a *blobBackendAdapter) Read(ctx context.Context, handle blobbackend.Handle) ([]byte, error) {
	b, err := a.be.Read(ctx, persistence.Handle(handle))
	if errors.Is(err, persistence.ErrBlobNotFound) {
		return nil, blobbackend.ErrBlobNotFound
	}
	return b, err
}

func (a *blobBackendAdapter) ReadRange(ctx context.Context, handle blobbackend.Handle, offset, length int64) ([]byte, error) {
	b, err := a.be.ReadRange(ctx, persistence.Handle(handle), offset, length)
	if errors.Is(err, persistence.ErrBlobNotFound) {
		return nil, blobbackend.ErrBlobNotFound
	}
	return b, err
}

func (a *blobBackendAdapter) Delete(ctx context.Context, handle blobbackend.Handle) error {
	return a.be.Delete(ctx, persistence.Handle(handle))
}

func TestBlobRoundtripBackends(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name         string
		handlePrefix string
		make         func(t *testing.T) persistence.BlobBackend
	}{
		{
			name:         "memory",
			handlePrefix: "mem:",
			make: func(t *testing.T) persistence.BlobBackend {
				return persistence.NewMemoryBackend()
			},
		},
		{
			name:         "filesystem",
			handlePrefix: "fs:",
			make: func(t *testing.T) persistence.BlobBackend {
				root := t.TempDir()
				bb, err := persistence.NewFilesystemBackend(root)
				if err != nil {
					t.Fatalf("NewFilesystemBackend(%s): %v", root, err)
				}
				return bb
			},
		},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			bb := tc.make(t)
			ctx := context.Background()

			results := blobbackend.Run(ctx, &blobBackendAdapter{be: bb})
			for _, r := range results {
				if r.Err != nil {
					t.Errorf("%s: %v", r.Name, r.Err)
				}
			}

			probe, err := bb.Write(ctx, persistence.BlobKey{NodeID: "n1", AttributeName: "handle-prefix-probe"}, []byte("x"))
			if err != nil {
				t.Fatalf("Write probe: %v", err)
			}
			t.Cleanup(func() { _ = bb.Delete(ctx, probe) })
			if !strings.HasPrefix(string(probe), tc.handlePrefix) {
				t.Fatalf("handle missing %q prefix: got %q", tc.handlePrefix, probe)
			}
		})
	}
}

func TestShouldSpillBlobDecision(t *testing.T) {
	t.Parallel()
	mem := persistence.NewMemoryBackend()
	cases := []struct {
		name      string
		bb        persistence.BlobBackend
		threshold int
		size      int
		want      bool
	}{
		{name: "nil-backend", bb: nil, threshold: 1024, size: 2048, want: false},
		{name: "zero-size", bb: mem, threshold: 1024, size: 0, want: false},
		{name: "zero-threshold", bb: mem, threshold: 0, size: 2048, want: false},
		{name: "inline-backend", bb: persistence.InlineBackend{}, threshold: 1024, size: 2048, want: false},
		{name: "below-threshold", bb: mem, threshold: 1024, size: 1024, want: false},
		{name: "above-threshold", bb: mem, threshold: 1024, size: 1025, want: true},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := persistence.ShouldSpillBlob(tc.bb, tc.threshold, tc.size)
			if got != tc.want {
				t.Fatalf("ShouldSpillBlob(%v): got %v, want %v", tc.name, got, tc.want)
			}
		})
	}
}
