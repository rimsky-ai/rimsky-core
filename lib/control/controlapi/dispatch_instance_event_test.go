// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package controlapi

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/locks/storetest"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	"github.com/rimsky-ai/rimsky-core/lib/protocols/claimproducer"
)

func TestDispatchInstanceEvent_CarriesServiceBindingsAndOwnerAPIKeyID(t *testing.T) {
	t.Parallel()
	fake := storetest.NewFake("peer", claimproducer.Capabilities{})
	ownerID := shared.UUID(uuid.New())
	payload := InstancePayload{
		InstanceKey:     "ck-1",
		Params:          json.RawMessage(`{"a":1}`),
		ServiceBindings: json.RawMessage(`{"svc":"binding-a"}`),
		OwnerAPIKeyID:   &ownerID,
	}

	err := dispatchInstanceEvent(context.Background(), fake, EventInstanceCreated, "sha256-x", "instance-1", payload)
	require.NoError(t, err)

	calls := fake.Calls()
	require.Len(t, calls, 1)
	require.Equal(t, "on_instance_created", calls[0].Verb)
	require.JSONEq(t, `{"svc":"binding-a"}`, string(calls[0].ServiceBindings),
		"OnInstanceCreatedRequest.ServiceBindings must carry the instance payload's service bindings through unmodified")
	require.Equal(t, ownerID.String(), calls[0].OwnerAPIKeyID,
		"OnInstanceCreatedRequest.OwnerAPIKeyID must carry the owning API key id for a non-anonymous instance")
}

func TestDispatchInstanceEvent_AnonymousInstanceHasEmptyOwnerAPIKeyID(t *testing.T) {
	t.Parallel()
	fake := storetest.NewFake("peer", claimproducer.Capabilities{})
	payload := InstancePayload{
		InstanceKey:   "ck-2",
		OwnerAPIKeyID: nil,
	}

	err := dispatchInstanceEvent(context.Background(), fake, EventInstanceCreated, "sha256-x", "instance-2", payload)
	require.NoError(t, err)

	calls := fake.Calls()
	require.Len(t, calls, 1)
	require.Equal(t, "", calls[0].OwnerAPIKeyID,
		"an anonymously-created instance must carry an empty OwnerAPIKeyID, not a zero-UUID string")
}
