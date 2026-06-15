// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package persistence

// ListPagination is the cursor + page-size envelope shared by every
// cursor-paginated *Store method.
type ListPagination struct {
	Limit  int    // @constraint: 0 means use the implementation-defined default page size
	Cursor string // @constraint: opaque token; empty selects the first page
}

// PaginatedListResult wraps a row slice with the next-cursor.
// NextCursor == "" indicates the final page.
type PaginatedListResult[T any] struct {
	Rows       []T
	NextCursor string
}
