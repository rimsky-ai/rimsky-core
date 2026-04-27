package store

import (
	"context"

	"github.com/jackc/pgx/v5"
)

// txKey is the unexported context key under which the supervisor stashes the
// open *pgx.Tx for a store call. Using an unexported zero-size struct
// guarantees no collision with other packages' context keys.
type txKey struct{}

// WithTx attaches an open pgx.Tx to the context. Used by the supervisor's
// runner before calling Store methods that may need to write inside the same
// transaction.
//
// A store with no DB writes (like filesystem-direct) is free to call
// TxFromContext and ignore the returned tx. A store with DB writes (like
// claim-store-postgres) MUST use the tx for all its mutations — never the
// underlying pool — so atomicity with the supervisor's lock-holder inserts
// is preserved. (Spec §8.4.1.)
func WithTx(ctx context.Context, tx pgx.Tx) context.Context {
	return context.WithValue(ctx, txKey{}, tx)
}

// TxFromContext returns the pgx.Tx attached via WithTx, or (nil, false) if
// none is present. Stores that have no DB writes (e.g. filesystem-direct)
// may ignore the tx; the supervisor still attaches one so AcquireLock /
// ReleaseLock can be called uniformly.
func TxFromContext(ctx context.Context) (pgx.Tx, bool) {
	tx, ok := ctx.Value(txKey{}).(pgx.Tx)
	return tx, ok
}
