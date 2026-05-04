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
