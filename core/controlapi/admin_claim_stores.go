// admin_claim_stores.go — POST /admin/stores/{name}/pick-policies/{selector}/items.
//
// Bulk-inserts items into the operator-owned items table backing a
// configured pick policy on a postgres store. Rimsky itself never
// enqueues — this endpoint exists for operators and external producers
// who want to drive items through HTTP rather than direct SQL.
//
// Selector path-param URL encoding: the recommended selector convention
// is `@policy-name` (e.g. `@review-queue`). Both the raw form and the
// percent-encoded form `%40policy-name` are accepted by chi's path-
// parameter extractor — go's net/url decodes `%40` back to `@` before
// chi inspects the path. External operator tooling that double-encodes
// (e.g. `%2540`) will not match; producers should send either form.
//
// Auth: relies on the global AppDeps.Auth middleware. Operators that
// want the endpoint admin-gated wire an Authenticator that checks
// X-Rimsky-Admin-Token; when no Auth is configured the route is
// anonymous, consistent with the rest of the API in pre-v1.

package controlapi

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"

	"github.com/go-chi/chi/v5"

	pgstore "github.com/fallguy/rimsky/core/store/postgres"
)

type adminPickPolicyItem struct {
	Payload json.RawMessage `json:"payload"`
}

type adminPickPolicyInsertRequest struct {
	Items []adminPickPolicyItem `json:"items"`
}

type adminPickPolicyInsertResponse struct {
	Inserted int `json:"inserted"`
}

// registerAdminClaimStoresRoutes wires the admin item-insert route
// under the new pick-policy URL shape:
// POST /admin/stores/{name}/pick-policies/{selector}/items.
//
// The {selector} path-param accepts both the raw `@policy-name` form
// and the percent-encoded `%40policy-name` form — chi resolves the
// segment after standard URL decoding, so both reach this handler with
// the leading `@` intact. Operators only need to avoid *double*
// encoding (`%2540...`), which leaves a literal `%40` in the decoded
// value and won't match the configured selector. See operator-guide
// §5.5 for the curl examples.
//
// (Legacy POST /admin/claim-stores/{name}/items is dropped per stores-
// redesign-v2 — there is no "claim store" kind anymore.)
func registerAdminClaimStoresRoutes(r chi.Router, deps AppDeps) {
	r.Post("/admin/stores/{name}/pick-policies/{selector}/items", handleAdminPickPolicyInsert(deps))
}

func handleAdminPickPolicyInsert(deps AppDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		if deps.Stores == nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]any{
				"error": "store registry not configured",
			})
			return
		}
		name := chi.URLParam(req, "name")
		// chi returns the path segment without decoding percent-escapes;
		// decode here so percent-encoded `@` (the form many URL-builder
		// libraries auto-produce, e.g. `%40review-queue`) resolves to
		// the same selector as the raw form. Double-encoding still
		// fails the selector lookup below, which is the documented
		// operator footgun in operator-guide §5.5.
		rawSelector := chi.URLParam(req, "selector")
		selector, decodeErr := url.PathUnescape(rawSelector)
		if decodeErr != nil {
			badRequest(w, "pick-policy selector is not valid percent-encoding: "+decodeErr.Error())
			return
		}
		if name == "" {
			badRequest(w, "store name is required")
			return
		}
		if selector == "" {
			badRequest(w, "pick-policy selector is required")
			return
		}
		s, ok := deps.Stores.GetStore(name)
		if !ok {
			notFoundResp(w, "store not found: "+name)
			return
		}
		ps, ok := s.(*pgstore.Store)
		if !ok {
			badRequest(w, "store "+name+" is not a postgres store (kind="+s.Kind()+")")
			return
		}
		if _, ok := ps.PickPolicyConfig(selector); !ok {
			badRequest(w, "store "+name+" has no pick-policy configured for selector "+selector)
			return
		}

		var body adminPickPolicyInsertRequest
		if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
			badRequest(w, "invalid JSON body: "+err.Error())
			return
		}
		if len(body.Items) == 0 {
			badRequest(w, "items array must not be empty")
			return
		}
		payloads := make([]json.RawMessage, 0, len(body.Items))
		for i, item := range body.Items {
			if len(item.Payload) == 0 {
				badRequest(w, "items[*].payload is required")
				return
			}
			if !json.Valid(item.Payload) {
				badRequest(w, "items["+strconv.Itoa(i)+"].payload is not valid JSON")
				return
			}
			payloads = append(payloads, item.Payload)
		}

		if err := ps.InsertItems(req.Context(), selector, payloads); err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusCreated, adminPickPolicyInsertResponse{
			Inserted: len(payloads),
		})
	}
}
