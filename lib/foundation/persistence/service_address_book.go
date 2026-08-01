// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

// @concept: service-address-book

package persistence

import "context"

const (
	ServiceKindExecutor      = "executor"
	ServiceKindClaimProducer = "claim_producer"
)

type ServiceAddressRow struct {
	Kind      string `json:"kind"`
	Name      string `json:"name"`
	Transport string `json:"transport"`
	Endpoint  string `json:"endpoint"`
	TLS       string `json:"tls"`
}

type ServiceAddressBookTable interface {
	PublishAll(ctx context.Context, rows []ServiceAddressRow, tx Tx) error
	Get(ctx context.Context, kind, name string, tx Tx) (*ServiceAddressRow, error)
	List(ctx context.Context, tx Tx) ([]ServiceAddressRow, error)
}
