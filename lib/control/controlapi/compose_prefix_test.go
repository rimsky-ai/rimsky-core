// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package controlapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/auth"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/lifecycle"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/locks"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/locks/storetest"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	"github.com/rimsky-ai/rimsky-core/lib/protocols/claimproducer"
	"github.com/rimsky-ai/rimsky-core/test/support/pgdbtest"
)

const composeOriginHeaderName = "X-Rimsky-Compose-Origin"

func TestIsComposeOrigin_NoIdentityInContextFailsClosed(t *testing.T) {
	t.Parallel()
	req := httptest.NewRequest(http.MethodPost, "/v1/tags", nil)
	req.Header.Set(composeOriginHeaderName, "1")

	if isComposeOrigin(req) {
		t.Fatalf("isComposeOrigin: got true want false; a request with no identity in context " +
			"must not be granted the compose-origin capability")
	}
}

func newAuthedComposeHarness(t *testing.T, permissions string) (*harness, string) {
	t.Helper()
	ctx := context.Background()
	d := pgdbtest.OpenDriver(ctx, t)

	reg := locks.NewRegistry()
	contentFake := storetest.NewFake("content", claimproducer.Capabilities{WriteSemanticsAllowed: []claimproducer.WriteSemantics{claimproducer.WriteSemanticsSync}})
	topicsFake := storetest.NewFake("topics-ring", claimproducer.Capabilities{WriteSemanticsAllowed: []claimproducer.WriteSemantics{claimproducer.WriteSemanticsSync}})
	reg.Add("content", contentFake)
	reg.Add("topics-ring", topicsFake)

	lcReg := lifecycle.NewRegistry()
	lcReg.Add("content", contentFake)
	lcReg.Add("topics-ring", topicsFake)

	capLog := shared.NewCapturingLogger()
	clock := shared.SystemClock{}
	state := &AuthState{
		Tables:   d.Tables(),
		Registry: BuildV1Registry(),
		Clock:    clock,
		Logger:   capLog,
	}
	plaintext, hash, err := auth.Mint()
	require.NoError(t, err)
	require.NoError(t, d.Tables().Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		return d.Tables().APIKeys().Insert(ctx, persistence.APIKey{
			ID:          shared.UUID(uuid.New()),
			Name:        "compose-prefix-test-key",
			KeyHash:     hash[:],
			Permissions: []byte(permissions),
			CreatedAt:   clock.Now(),
		}, tx)
	}))

	app := NewApp(AppDeps{
		Persist:        d.Tables(),
		AdvisoryLocker: d.AdvisoryLocker(),
		Queue:          d.Queue(),
		Clock:          clock,
		Logger:         capLog,
		ClaimProducers: reg,
		LifecycleSubs:  lcReg,
		NamedLocks: locks.NamedLocksConfig{
			Locks: map[string]locks.NamedLockConfig{
				"topics-ring:concurrent": {Limit: 5},
			},
		},
		Executors: map[string]ExecutorEntry{
			"worker":      {Transport: "grpc", Endpoint: "localhost:0"},
			"unused-exec": {Transport: "grpc", Endpoint: "localhost:0"},
		},
		AuthState: state,
	})
	srv := httptest.NewServer(app)
	t.Cleanup(srv.Close)

	h := &harness{srv: srv, driver: d, persist: d.Tables(), producers: reg, logger: capLog}
	return h, plaintext
}

func registerAndDeploy(t *testing.T, h *harness, namePrefix string) string {
	t.Helper()
	_, out := h.httpJSON(t, "POST", "/v1/templates", validTemplateBody(namePrefix+"-"+uuid.NewString()))
	tplID, _ := out["template_id"].(string)
	require.NotEmpty(t, tplID, "register did not return a template_id: %v", out)
	deployStatus, deployOut := h.httpJSON(t, "POST", "/v1/templates/"+tplID+"/deploy", map[string]any{})
	require.Equal(t, http.StatusOK, deployStatus, deployOut)
	return tplID
}

func tagPresent(t *testing.T, h *harness, name string) bool {
	t.Helper()
	status, listed := h.httpJSON(t, "GET", "/v1/tags", nil)
	require.Equal(t, http.StatusOK, status, listed)
	tags, _ := listed["tags"].([]any)
	for _, raw := range tags {
		item, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		if item["tag"] == name {
			return true
		}
	}
	return false
}

func TestCreateTag_ComposePrefixRejected(t *testing.T) {
	t.Parallel()
	h, teardown := newHarness(t)
	t.Cleanup(teardown)

	tplID := registerAndDeploy(t, h, "compose-tag-reject")

	reservedTag := "compose:my-app:v1"
	status, body := h.httpJSON(t, "POST", "/v1/tags", map[string]any{
		"tag":      reservedTag,
		"template": tplID,
	})

	require.Equal(t, http.StatusBadRequest, status, body)
	errMsg, _ := body["error"].(string)
	require.Contains(t, strings.ToLower(errMsg), "reserved",
		"error should name the reserved prefix: %v", body)
	require.Contains(t, errMsg, "compose:",
		"error should cite the compose: prefix: %v", body)

	require.False(t, tagPresent(t, h, reservedTag),
		"reserved-prefix tag was persisted despite the rejection")
}

func TestCreateInstance_ComposePrefixRejected(t *testing.T) {
	t.Parallel()
	h, teardown := newHarness(t)
	t.Cleanup(teardown)

	tplID := registerAndDeploy(t, h, "compose-inst-reject")

	reservedKey := "compose:my-app:i1"
	status, body := h.httpJSON(t, "POST", "/v1/instances", map[string]any{
		"template":     tplID,
		"instance_key": reservedKey,
	})

	require.Equal(t, http.StatusBadRequest, status, body)
	errMsg, _ := body["error"].(string)
	require.Contains(t, strings.ToLower(errMsg), "reserved",
		"error should name the reserved prefix: %v", body)
	require.Contains(t, errMsg, "compose:",
		"error should cite the compose: prefix: %v", body)

	getStatus, getBody := h.httpJSON(t, "GET", "/v1/instances/"+reservedKey, nil)
	require.Equal(t, http.StatusNotFound, getStatus, getBody)
}

func TestCreateTag_ComposeOriginHeaderWithoutGrantRejected(t *testing.T) {
	t.Parallel()
	h, token := newAuthedComposeHarness(t, `[{"action":"template:register"},{"action":"template:deploy"},{"action":"tag:create"},{"action":"tag:read"}]`)
	authHeaders := map[string]string{"Authorization": "Bearer " + token}

	regResp := h.httpJSONWithHeaders(t, "POST", "/v1/templates", validTemplateBody("compose-origin-no-grant-"+uuid.NewString()), authHeaders)
	require.Equal(t, http.StatusCreated, regResp.status, regResp.body)
	tplID, _ := regResp.body["template_id"].(string)
	require.NotEmpty(t, tplID)
	deployResp := h.httpJSONWithHeaders(t, "POST", "/v1/templates/"+tplID+"/deploy", map[string]any{}, authHeaders)
	require.Equal(t, http.StatusOK, deployResp.status, deployResp.body)

	reservedTag := "compose:my-app:v1"
	withHeader := map[string]string{"Authorization": "Bearer " + token, composeOriginHeaderName: "1"}
	resp := h.httpJSONWithHeaders(t, "POST", "/v1/tags", map[string]any{
		"tag":      reservedTag,
		"template": tplID,
	}, withHeader)

	require.Equal(t, http.StatusBadRequest, resp.status, resp.body)
	errMsg, _ := resp.body["error"].(string)
	require.Contains(t, strings.ToLower(errMsg), "reserved",
		"an identity without compose:origin must still be rejected as if the header were absent: %v", resp.body)

	listResp := h.httpJSONWithHeaders(t, "GET", "/v1/tags", nil, authHeaders)
	require.Equal(t, http.StatusOK, listResp.status, listResp.body)
	tags, _ := listResp.body["tags"].([]any)
	for _, raw := range tags {
		item, _ := raw.(map[string]any)
		require.NotEqual(t, reservedTag, item["tag"],
			"reserved-prefix tag was persisted despite the missing compose:origin grant")
	}
}

func TestCreateTag_ComposeOriginHeaderWithGrantAllowed(t *testing.T) {
	t.Parallel()
	h, token := newAuthedComposeHarness(t, `[{"action":"template:register"},{"action":"template:deploy"},{"action":"tag:create"},{"action":"tag:read"},{"action":"compose:origin"}]`)
	authHeaders := map[string]string{"Authorization": "Bearer " + token}

	regResp := h.httpJSONWithHeaders(t, "POST", "/v1/templates", validTemplateBody("compose-origin-grant-"+uuid.NewString()), authHeaders)
	require.Equal(t, http.StatusCreated, regResp.status, regResp.body)
	tplID, _ := regResp.body["template_id"].(string)
	require.NotEmpty(t, tplID)
	deployResp := h.httpJSONWithHeaders(t, "POST", "/v1/templates/"+tplID+"/deploy", map[string]any{}, authHeaders)
	require.Equal(t, http.StatusOK, deployResp.status, deployResp.body)

	reservedTag := "compose:my-app:v1"
	withHeader := map[string]string{"Authorization": "Bearer " + token, composeOriginHeaderName: "1"}
	resp := h.httpJSONWithHeaders(t, "POST", "/v1/tags", map[string]any{
		"tag":      reservedTag,
		"template": tplID,
	}, withHeader)

	require.Equal(t, http.StatusCreated, resp.status, resp.body)
	require.Equal(t, reservedTag, resp.body["tag"], resp.body)

	listResp := h.httpJSONWithHeaders(t, "GET", "/v1/tags", nil, authHeaders)
	require.Equal(t, http.StatusOK, listResp.status, listResp.body)
	tags, _ := listResp.body["tags"].([]any)
	found := false
	for _, raw := range tags {
		item, _ := raw.(map[string]any)
		if item["tag"] == reservedTag {
			found = true
		}
	}
	require.True(t, found, "compose-origin tag create with the compose:origin grant should have persisted the reserved tag")
}

func TestCreateTag_ComposeOriginAllowed(t *testing.T) {
	t.Parallel()
	h, teardown := newHarness(t)
	t.Cleanup(teardown)

	tplID := registerAndDeploy(t, h, "compose-tag-allow")

	reservedTag := "compose:my-app:v1"
	resp := h.httpJSONWithHeaders(t, "POST", "/v1/tags", map[string]any{
		"tag":      reservedTag,
		"template": tplID,
	}, map[string]string{composeOriginHeaderName: "1"})

	require.Equal(t, http.StatusCreated, resp.status, resp.body)
	require.Equal(t, reservedTag, resp.body["tag"], resp.body)

	require.True(t, tagPresent(t, h, reservedTag),
		"compose-origin tag create should have persisted the reserved tag")
}
