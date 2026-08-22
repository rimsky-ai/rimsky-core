// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package persistence

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// @concept: blob-backend
// @decision: blob-spill-threshold-config
func ShouldSpillBlob(bb BlobBackend, threshold int, size int) bool {
	if size <= 0 {
		return false
	}
	if bb == nil {
		return false
	}
	if threshold <= 0 {
		return false
	}
	if bb.Name() == "inline" {
		return false
	}
	return size > threshold
}

const DefaultOrphanRetention = 24 * time.Hour

func QueueBlobOrphan(
	ctx context.Context, orphans BlobOrphanTable, handle, backend string, now time.Time, retention time.Duration, tx Tx,
) error {
	if handle == "" || orphans == nil {
		return nil
	}
	if retention <= 0 {
		retention = DefaultOrphanRetention
	}
	return orphans.Insert(ctx, BlobOrphanRow{
		Handle:     handle,
		Backend:    backend,
		OrphanedAt: now,
		ReapAfter:  now.Add(retention),
	}, tx)
}

type CarriedBag struct {
	Data        []byte
	DispatchBag []byte
	Handle      string
	Backend     string
}

// @concept: blob-backend
// @decision: blob-backend-mismatch-read-refused
func CheckBlobBackendMatches(op string, bb BlobBackend, handle, rowBackend string) error {
	if handle == "" {
		return nil
	}
	if bb != nil && rowBackend == bb.Name() {
		return nil
	}
	if rowBackend == "" {
		rowBackend = "<none>"
	}
	active := "<none>"
	if bb != nil {
		active = bb.Name()
	}
	return fmt.Errorf("%s: row has value_handle %q on backend %q, but active blob backend is %q",
		op, handle, rowBackend, active)
}

// @decision: blob-backend-mismatch-read-refused
func CarryForwardBag(
	ctx context.Context, bb BlobBackend, key BlobKey, priorData []byte, priorHandle, priorBackend string, tx Tx,
) (CarriedBag, error) {
	if err := CheckBlobBackendMatches("CarryForwardBag", bb, priorHandle, priorBackend); err != nil {
		return CarriedBag{}, err
	}
	if priorHandle == "" {
		return CarriedBag{
			Data:        priorData,
			DispatchBag: priorData,
		}, nil
	}
	bytes, err := ReadBlob(ctx, bb, Handle(priorHandle), tx)
	if err != nil {
		if errors.Is(err, ErrBlobNotFound) {
			empty := []byte("{}")
			return CarriedBag{Data: empty, DispatchBag: empty}, nil
		}
		return CarriedBag{}, err
	}
	fresh, err := WriteBlob(ctx, bb, key, bytes, tx)
	if err != nil {
		return CarriedBag{}, err
	}
	return CarriedBag{
		Data:        []byte("{}"),
		DispatchBag: bytes,
		Handle:      string(fresh),
		Backend:     bb.Name(),
	}, nil
}
