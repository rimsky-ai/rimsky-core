// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package persistence

import (
	"context"
	"errors"
)

// @decision: blob-backends-pluggable
type BlobBackend interface {
	Write(ctx context.Context, key BlobKey, bytes []byte) (Handle, error)

	Read(ctx context.Context, handle Handle) ([]byte, error)

	ReadRange(ctx context.Context, handle Handle, offset, length int64) ([]byte, error)

	Delete(ctx context.Context, handle Handle) error

	Name() string
}

type TxBlobBackend interface {
	BlobBackend

	WriteInTx(ctx context.Context, key BlobKey, bytes []byte, tx Tx) (Handle, error)

	ReadInTx(ctx context.Context, handle Handle, tx Tx) ([]byte, error)
}

func WriteBlobInTx(ctx context.Context, bb BlobBackend, key BlobKey, bytes []byte, tx Tx) (Handle, error) {
	if txbb, ok := bb.(TxBlobBackend); ok && tx != nil {
		return txbb.WriteInTx(ctx, key, bytes, tx)
	}
	return bb.Write(ctx, key, bytes)
}

func ReadBlobInTx(ctx context.Context, bb BlobBackend, handle Handle, tx Tx) ([]byte, error) {
	if txbb, ok := bb.(TxBlobBackend); ok && tx != nil {
		return txbb.ReadInTx(ctx, handle, tx)
	}
	return bb.Read(ctx, handle)
}

type BlobKey struct {
	NodeID        string
	AttributeName string
	Hint          string
}

type Handle string

var ErrBlobNotFound = errors.New("blob: handle not found")
