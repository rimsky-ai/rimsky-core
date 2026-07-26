// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// @concept: service-address-book

package config

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/rimsky-ai/rimsky-core/lib/control/controlapi"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/locks"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/runtime/executor"
	"github.com/rimsky-ai/rimsky-core/lib/runtime/peer"
)

const inprocTransport = "inproc"

func publishServiceAddressBook(
	ctx context.Context,
	persist persistence.Tables,
	executors map[string]controlapi.ExecutorEntry,
	stores RemoteClaimProducersConfig,
	bundledStores map[string]locks.ClaimProducer,
) error {
	rows := make([]persistence.ServiceAddressRow, 0, len(executors)+len(stores.ClaimProducers)+len(bundledStores))
	for name, e := range executors {
		rows = append(rows, persistence.ServiceAddressRow{
			Kind:      persistence.ServiceKindExecutor,
			Name:      name,
			Transport: e.Transport,
			Endpoint:  e.Endpoint,
			TLS:       e.TLS,
		})
	}
	for name, e := range stores.ClaimProducers {
		rows = append(rows, persistence.ServiceAddressRow{
			Kind:      persistence.ServiceKindClaimProducer,
			Name:      name,
			Transport: "grpc",
			Endpoint:  e.Endpoint,
			TLS:       e.TLS,
		})
	}
	for name := range bundledStores {
		if _, configured := stores.ClaimProducers[name]; configured {
			continue
		}
		rows = append(rows, persistence.ServiceAddressRow{
			Kind:      persistence.ServiceKindClaimProducer,
			Name:      name,
			Transport: inprocTransport,
			Endpoint:  name,
		})
	}
	if err := persist.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		return persist.ServiceAddressBook().PublishAll(ctx, rows, tx)
	}); err != nil {
		return fmt.Errorf("publishServiceAddressBook: %w", err)
	}
	return nil
}

func AddressBookExecutorLookup(persist persistence.Tables) executor.AddressBookLookup {
	return func(ctx context.Context, name string) (executor.Endpoint, bool, error) {
		var row *persistence.ServiceAddressRow
		if err := persist.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
			r, err := persist.ServiceAddressBook().Get(ctx, persistence.ServiceKindExecutor, name, tx)
			row = r
			return err
		}); err != nil {
			return executor.Endpoint{}, false, err
		}
		if row == nil || row.Transport == inprocTransport {
			return executor.Endpoint{}, false, nil
		}
		return executor.Endpoint{Transport: row.Transport, URL: row.Endpoint, TLS: row.TLS}, true, nil
	}
}

func addressBookStoreLookup(persist persistence.Tables) locks.LookupProducerEndpoint {
	return func(ctx context.Context, name string, tx persistence.Tx) (locks.ProducerEndpoint, bool, error) {
		var row *persistence.ServiceAddressRow
		if tx != nil {
			r, err := persist.ServiceAddressBook().Get(ctx, persistence.ServiceKindClaimProducer, name, tx)
			if err != nil {
				return locks.ProducerEndpoint{}, false, err
			}
			row = r
		} else if err := persist.Transaction(ctx, func(ctx context.Context, itx persistence.Tx) error {
			r, err := persist.ServiceAddressBook().Get(ctx, persistence.ServiceKindClaimProducer, name, itx)
			row = r
			return err
		}); err != nil {
			return locks.ProducerEndpoint{}, false, err
		}
		if row == nil || row.Transport == inprocTransport {
			return locks.ProducerEndpoint{}, false, nil
		}
		return locks.ProducerEndpoint{Transport: row.Transport, Endpoint: row.Endpoint, TLS: row.TLS}, true, nil
	}
}

func addressBookStoreDialer() locks.DialProducer {
	return func(ctx context.Context, name string, ep locks.ProducerEndpoint) (locks.ClaimProducer, error) {
		dialCtx, cancel := context.WithTimeout(ctx, capabilitiesHandshakeTimeout)
		defer cancel()
		client, err := peer.Dial(dialCtx, name, ep.Endpoint, ep.TLS)
		if err != nil {
			return nil, err
		}
		return client, nil
	}
}

func newAddressBookProducerRegistry(persist persistence.Tables, lateBindServiceProxies map[string]string) *locks.Registry {
	lookupBindings := func(ctx context.Context, instanceID string, tx persistence.Tx) (map[string]json.RawMessage, bool, error) {
		return LookupInstanceBindings(ctx, persist, instanceID, tx)
	}
	return locks.NewRegistry(
		locks.WithLookupInstanceBindings(lookupBindings),
		locks.WithLateBindServiceProxies(lateBindServiceProxies),
		locks.WithAddressBookResolution(addressBookStoreLookup(persist), addressBookStoreDialer(), 0, nil),
	)
}
