// Copyright © 2026 Fall Guy Consulting.
// Licensed under the Apache License, Version 2.0. See LICENSE.apache at the
// repo root, or http://www.apache.org/licenses/LICENSE-2.0.

package blobbackend

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"sync"

	"github.com/rimsky-ai/rimsky-core/lib/protocols/conformance/check"
)

type Backend interface {
	Write(ctx context.Context, hint string, payload []byte) (Handle, error)
	Read(ctx context.Context, handle Handle) ([]byte, error)
	ReadRange(ctx context.Context, handle Handle, offset, length int64) ([]byte, error)
	Delete(ctx context.Context, handle Handle) error
}

type Handle string

var ErrBlobNotFound = errors.New("blobbackend: handle not found")

type CheckResult = check.Result

func Run(ctx context.Context, be Backend) []CheckResult {
	checks := []struct {
		name string
		fn   func(context.Context, Backend) error
	}{
		{"round-trip 1KB", checkRoundtripSmall},
		{"round-trip 10MB", checkRoundtripLarge},
		{"range read", checkReadRange},
		{"range read out of bounds returns io.ErrUnexpectedEOF", checkReadRangeOutOfBounds},
		{"delete then read returns ErrBlobNotFound", checkDeleteThenRead},
		{"delete then range-read returns ErrBlobNotFound", checkDeleteThenReadRange},
		{"empty-payload write round-trips", checkEmptyPayloadWrite},
		{"handle is a non-empty self-describing string", checkHandleNonEmpty},
		{"idempotent delete", checkIdempotentDelete},
		{"concurrent writes", checkConcurrentWrites},
	}
	out := make([]CheckResult, 0, len(checks))
	for _, c := range checks {
		err := c.fn(ctx, be)
		out = append(out, CheckResult{Name: c.name, Err: err})
	}
	return out
}

func checkRoundtripSmall(ctx context.Context, be Backend) error {
	payload := bytes.Repeat([]byte("x"), 1024)
	h, err := be.Write(ctx, "rt-small", payload)
	if err != nil {
		return fmt.Errorf("write: %w", err)
	}
	got, err := be.Read(ctx, h)
	if err != nil {
		return fmt.Errorf("read: %w", err)
	}
	if !bytes.Equal(got, payload) {
		return errors.New("byte mismatch")
	}
	return be.Delete(ctx, h)
}

func checkRoundtripLarge(ctx context.Context, be Backend) error {
	payload := bytes.Repeat([]byte("0123456789"), 1024*1024)
	h, err := be.Write(ctx, "rt-large", payload)
	if err != nil {
		return fmt.Errorf("write: %w", err)
	}
	got, err := be.Read(ctx, h)
	if err != nil {
		return fmt.Errorf("read: %w", err)
	}
	if !bytes.Equal(got, payload) {
		return errors.New("byte mismatch")
	}
	return be.Delete(ctx, h)
}

func checkReadRange(ctx context.Context, be Backend) error {
	payload := []byte("0123456789abcdef")
	h, err := be.Write(ctx, "range", payload)
	if err != nil {
		return err
	}
	got, err := be.ReadRange(ctx, h, 5, 5)
	if err != nil {
		return err
	}
	if string(got) != "56789" {
		return fmt.Errorf("range mismatch: got %q", got)
	}
	return be.Delete(ctx, h)
}

func checkDeleteThenRead(ctx context.Context, be Backend) error {
	h, err := be.Write(ctx, "del-read", []byte("x"))
	if err != nil {
		return err
	}
	if err := be.Delete(ctx, h); err != nil {
		return fmt.Errorf("delete: %w", err)
	}
	if _, err := be.Read(ctx, h); !errors.Is(err, ErrBlobNotFound) {
		return fmt.Errorf("post-delete Read: want ErrBlobNotFound, got %v", err)
	}
	return nil
}

func checkDeleteThenReadRange(ctx context.Context, be Backend) error {
	h, err := be.Write(ctx, "del-range", []byte("0123456789"))
	if err != nil {
		return err
	}
	if err := be.Delete(ctx, h); err != nil {
		return fmt.Errorf("delete: %w", err)
	}
	if _, err := be.ReadRange(ctx, h, 0, 1); !errors.Is(err, ErrBlobNotFound) {
		return fmt.Errorf("post-delete ReadRange: want ErrBlobNotFound, got %v", err)
	}
	return nil
}

func checkReadRangeOutOfBounds(ctx context.Context, be Backend) error {
	payload := []byte("0123456789abcdef")
	h, err := be.Write(ctx, "range-oob", payload)
	if err != nil {
		return err
	}
	defer func() { _ = be.Delete(ctx, h) }()
	if _, err := be.ReadRange(ctx, h, int64(len(payload))+1, 1); err == nil {
		return errors.New("ReadRange with offset beyond EOF must error, got nil")
	}
	if _, err := be.ReadRange(ctx, h, 0, int64(len(payload))+100); !errors.Is(err, io.ErrUnexpectedEOF) {
		return fmt.Errorf("range read past end of blob: want io.ErrUnexpectedEOF, got %v", err)
	}
	return nil
}

func checkEmptyPayloadWrite(ctx context.Context, be Backend) error {
	h, err := be.Write(ctx, "empty-payload", []byte{})
	if err != nil {
		return fmt.Errorf("write empty payload: %w", err)
	}
	defer func() { _ = be.Delete(ctx, h) }()
	got, err := be.Read(ctx, h)
	if err != nil {
		return fmt.Errorf("read empty payload: %w", err)
	}
	if len(got) != 0 {
		return fmt.Errorf("empty-payload write round-trip: got %d bytes, want 0", len(got))
	}
	return nil
}

func checkHandleNonEmpty(ctx context.Context, be Backend) error {
	h, err := be.Write(ctx, "handle-shape", []byte("x"))
	if err != nil {
		return err
	}
	defer func() { _ = be.Delete(ctx, h) }()
	if string(h) == "" {
		return errors.New("Write returned an empty handle; handles must be non-empty self-describing strings")
	}
	return nil
}

func checkIdempotentDelete(ctx context.Context, be Backend) error {
	h, err := be.Write(ctx, "idem", []byte("x"))
	if err != nil {
		return err
	}
	if err := be.Delete(ctx, h); err != nil {
		return err
	}
	if err := be.Delete(ctx, h); err != nil {
		return fmt.Errorf("second delete: %w", err)
	}
	return nil
}

func checkConcurrentWrites(ctx context.Context, be Backend) error {
	const N = 16
	var wg sync.WaitGroup
	errs := make([]error, N)
	for i := 0; i < N; i++ {
		i := i
		wg.Add(1)
		go func() {
			defer wg.Done()
			h, err := be.Write(ctx, fmt.Sprintf("c-%d", i), []byte(fmt.Sprintf("payload-%d", i)))
			if err != nil {
				errs[i] = err
				return
			}
			defer func() { _ = be.Delete(ctx, h) }()
			got, err := be.Read(ctx, h)
			if err != nil {
				errs[i] = err
				return
			}
			if !bytes.Equal(got, []byte(fmt.Sprintf("payload-%d", i))) {
				errs[i] = fmt.Errorf("c-%d byte mismatch", i)
			}
		}()
	}
	wg.Wait()
	for _, e := range errs {
		if e != nil {
			return e
		}
	}
	return nil
}
