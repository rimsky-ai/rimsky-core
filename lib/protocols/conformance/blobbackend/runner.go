// Copyright © 2026 Fall Guy Consulting.
// Licensed under the Apache License, Version 2.0. See LICENSE.apache at the
// repo root, or http://www.apache.org/licenses/LICENSE-2.0.

package blobbackend

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/rimsky-ai/rimsky-core/lib/protocols/conformance/check"
)

type Backend interface {
	Write(ctx context.Context, hint string, bytes []byte) (Handle, error)
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
		{"delete then read returns ErrBlobNotFound", checkDeleteThenRead},
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
