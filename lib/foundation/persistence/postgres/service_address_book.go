// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// @concept: service-address-book

package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
)

func (s *tablesImpl) ServiceAddressBook() persistence.ServiceAddressBookTable {
	return (*serviceAddressBookImpl)(s)
}

type serviceAddressBookImpl tablesImpl

func (b *serviceAddressBookImpl) q(tx persistence.Tx) querier { return (*tablesImpl)(b).q(tx) }

var _ persistence.ServiceAddressBookTable = (*serviceAddressBookImpl)(nil)

func (b *serviceAddressBookImpl) PublishAll(ctx context.Context, rows []persistence.ServiceAddressRow, tx persistence.Tx) error {
	if tx == nil {
		return errors.New("postgres.ServiceAddressBook.PublishAll: tx required")
	}
	ex := b.q(tx)
	if _, err := ex.Exec(ctx, `DELETE FROM rimsky_service_address_book`); err != nil {
		return fmt.Errorf("postgres.ServiceAddressBook.PublishAll: clear: %w", err)
	}
	for _, r := range rows {
		if _, err := ex.Exec(ctx,
			`INSERT INTO rimsky_service_address_book (kind, name, transport, endpoint, tls)
			 VALUES ($1, $2, $3, $4, $5)`,
			r.Kind, r.Name, r.Transport, r.Endpoint, r.TLS,
		); err != nil {
			return fmt.Errorf("postgres.ServiceAddressBook.PublishAll: insert %s %q: %w", r.Kind, r.Name, err)
		}
	}
	return nil
}

func (b *serviceAddressBookImpl) Get(ctx context.Context, kind, name string, tx persistence.Tx) (*persistence.ServiceAddressRow, error) {
	row := b.q(tx).QueryRow(ctx,
		`SELECT kind, name, transport, endpoint, tls
		   FROM rimsky_service_address_book WHERE kind = $1 AND name = $2`,
		kind, name)
	var r persistence.ServiceAddressRow
	if err := row.Scan(&r.Kind, &r.Name, &r.Transport, &r.Endpoint, &r.TLS); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("postgres.ServiceAddressBook.Get: %w", err)
	}
	return &r, nil
}

func (b *serviceAddressBookImpl) List(ctx context.Context, tx persistence.Tx) ([]persistence.ServiceAddressRow, error) {
	rows, err := b.q(tx).Query(ctx,
		`SELECT kind, name, transport, endpoint, tls
		   FROM rimsky_service_address_book ORDER BY kind, name`)
	if err != nil {
		return nil, fmt.Errorf("postgres.ServiceAddressBook.List: %w", err)
	}
	defer rows.Close()
	var out []persistence.ServiceAddressRow
	for rows.Next() {
		var r persistence.ServiceAddressRow
		if err := rows.Scan(&r.Kind, &r.Name, &r.Transport, &r.Endpoint, &r.TLS); err != nil {
			return nil, fmt.Errorf("postgres.ServiceAddressBook.List: %w", err)
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("postgres.ServiceAddressBook.List: %w", err)
	}
	return out, nil
}
