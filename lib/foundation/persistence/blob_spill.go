// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package persistence

import (
	"context"
	"errors"
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

func CarryForwardBag(
	ctx context.Context, bb BlobBackend, key BlobKey, priorData []byte, priorHandle, priorBackend string, tx Tx,
) (CarriedBag, error) {
	if priorHandle == "" || bb == nil || priorBackend != bb.Name() {
		return CarriedBag{
			Data:        priorData,
			DispatchBag: priorData,
			Handle:      priorHandle,
			Backend:     priorBackend,
		}, nil
	}
	bytes, err := ReadBlobInTx(ctx, bb, Handle(priorHandle), tx)
	if err != nil {
		if errors.Is(err, ErrBlobNotFound) {
			empty := []byte("{}")
			return CarriedBag{Data: empty, DispatchBag: empty}, nil
		}
		return CarriedBag{}, err
	}
	fresh, err := WriteBlobInTx(ctx, bb, key, bytes, tx)
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
