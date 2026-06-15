// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package persistence_test

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
)

// TestBlobRoundtripBackends covers the cross-backend round-trip
// scenarios required by plan §D9:
//   - Write 1 KB and read back via Read.
//   - Write a payload above a configurable threshold and read back.
//   - Range read returns the requested slice.
//   - Delete removes the bytes (post-delete Read returns ErrBlobNotFound).
//   - Idempotent delete (delete twice; second is a no-op).
//
// Inline backend is excluded — its Write returns an error by design
// (the spill-decision sites never reach inline.Write per
// ShouldSpillBlob).
//
// pg-largeobject is exercised in
// foundation/persistence/postgres/blob_largeobject_test.go (testcontainers).
// This test runs against the in-process backends (memory + filesystem)
// so it stays fast and Docker-free.
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

			small := bytes.Repeat([]byte("0123456789abcdef"), 64)
			hSmall, err := bb.Write(ctx, persistence.BlobKey{NodeID: "n1", AttributeName: "small"}, small)
			if err != nil {
				t.Fatalf("Write small: %v", err)
			}
			gotSmall, err := bb.Read(ctx, hSmall)
			if err != nil {
				t.Fatalf("Read small: %v", err)
			}
			if !bytes.Equal(gotSmall, small) {
				t.Fatalf("Read small: bytes mismatch (got %d bytes, want %d)", len(gotSmall), len(small))
			}

			large := bytes.Repeat([]byte("abcdefghij"), 100*1024)
			if len(large) != 1000*1024 {
				t.Fatalf("test fixture wrong: got %d bytes", len(large))
			}
			hLarge, err := bb.Write(ctx, persistence.BlobKey{NodeID: "n1", AttributeName: "large"}, large)
			if err != nil {
				t.Fatalf("Write large: %v", err)
			}
			gotLarge, err := bb.Read(ctx, hLarge)
			if err != nil {
				t.Fatalf("Read large: %v", err)
			}
			if !bytes.Equal(gotLarge, large) {
				t.Fatalf("Read large: bytes mismatch (got %d, want %d)", len(gotLarge), len(large))
			}

			rangeBytes, err := bb.ReadRange(ctx, hLarge, 12345, 100)
			if err != nil {
				t.Fatalf("ReadRange large: %v", err)
			}
			if !bytes.Equal(rangeBytes, large[12345:12345+100]) {
				t.Fatalf("ReadRange large: bytes mismatch")
			}

			if err := bb.Delete(ctx, hSmall); err != nil {
				t.Fatalf("Delete small: %v", err)
			}
			if err := bb.Delete(ctx, hSmall); err != nil {
				t.Fatalf("Delete small (idempotent): %v", err)
			}
			if _, err := bb.Read(ctx, hSmall); !errors.Is(err, persistence.ErrBlobNotFound) {
				t.Fatalf("Read after delete: want ErrBlobNotFound, got %v", err)
			}

			if err := bb.Delete(ctx, hLarge); err != nil {
				t.Fatalf("Delete large: %v", err)
			}

			// @constraint: each backend prefixes handles with a short
			// backend identifier (per blob.go Handle godoc).
			if !strings.HasPrefix(string(hSmall), tc.handlePrefix) || !strings.HasPrefix(string(hLarge), tc.handlePrefix) {
				t.Fatalf("handles missing %q prefix: small=%q large=%q", tc.handlePrefix, hSmall, hLarge)
			}
		})
	}
}

// TestShouldSpillBlobDecision sanity-checks the threshold gate that
// drives D6: ShouldSpillBlob returns false for inline / nil / below-
// threshold payloads and true only when the configured backend can
// actually hold the bytes.
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
