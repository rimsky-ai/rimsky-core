// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package lifecycle_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/lifecycle"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/locks/storetest"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	"github.com/rimsky-ai/rimsky-core/lib/protocols/claimproducer"
)

func TestDispatchEvent_InstanceCreatedCarriesServiceBindingsAndOwnerAPIKeyID(t *testing.T) {
	t.Parallel()
	fake := storetest.NewFake("service", claimproducer.Capabilities{})
	ownerID := shared.UUID(uuid.New())
	payload := lifecycle.StagedPayload{
		TemplateHash: "sha256-x",
		InstanceID:   "instance-1",
		Instance: &lifecycle.InstancePayload{
			InstanceKey:     "ck-1",
			Params:          json.RawMessage(`{"a":1}`),
			ServiceBindings: json.RawMessage(`{"svc":"binding-a"}`),
			OwnerAPIKeyID:   &ownerID,
		},
	}

	err := lifecycle.DispatchEvent(context.Background(), fake, lifecycle.EventInstanceCreated, payload)
	require.NoError(t, err)

	calls := fake.Calls()
	require.Len(t, calls, 1)
	require.Equal(t, "on_instance_created", calls[0].Verb)
	require.JSONEq(t, `{"svc":"binding-a"}`, string(calls[0].ServiceBindings),
		"OnInstanceCreatedRequest.ServiceBindings must carry the instance payload's service bindings through unmodified")
	require.Equal(t, ownerID.String(), calls[0].OwnerAPIKeyID,
		"OnInstanceCreatedRequest.OwnerAPIKeyID must carry the owning API key id for a non-anonymous instance")
}

func TestDispatchEvent_AnonymousInstanceHasEmptyOwnerAPIKeyID(t *testing.T) {
	t.Parallel()
	fake := storetest.NewFake("service", claimproducer.Capabilities{})
	payload := lifecycle.StagedPayload{
		TemplateHash: "sha256-x",
		InstanceID:   "instance-2",
		Instance:     &lifecycle.InstancePayload{InstanceKey: "ck-2"},
	}

	err := lifecycle.DispatchEvent(context.Background(), fake, lifecycle.EventInstanceCreated, payload)
	require.NoError(t, err)

	calls := fake.Calls()
	require.Len(t, calls, 1)
	require.Equal(t, "", calls[0].OwnerAPIKeyID,
		"an anonymously-created instance must carry an empty OwnerAPIKeyID, not a zero-UUID string")
}

// @concept: run-scope
func TestDispatchEvent_RunScopeTerminalCarriesScopeInstanceAndReason(t *testing.T) {
	t.Parallel()
	fake := storetest.NewFake("service", claimproducer.Capabilities{})
	payload := lifecycle.StagedPayload{
		RunScopeID:     "scope-1",
		InstanceID:     "instance-3",
		TerminalReason: "frame_end",
	}

	err := lifecycle.DispatchEvent(context.Background(), fake, lifecycle.EventRunScopeTerminal, payload)
	require.NoError(t, err)

	calls := fake.Calls()
	require.Len(t, calls, 1)
	require.Equal(t, "on_run_scope_terminal", calls[0].Verb)
}
