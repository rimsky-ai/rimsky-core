// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package persistence

// Pagination inputs/outputs shared by every cursor-paginated *Store
// method. ListPagination is request-side; PaginatedListResult is the
// response-side wrapper.

// ListPagination is the cursor + page-size envelope.
type ListPagination struct {
	Limit  int    // 0 → default (implementation-defined)
	Cursor string // opaque; empty for first page
}

// PaginatedListResult wraps a row slice with the next-cursor.
// NextCursor == "" indicates the final page.
type PaginatedListResult[T any] struct {
	Rows       []T
	NextCursor string
}
