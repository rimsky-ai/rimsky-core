// Incremental attributes writeback callback handler — spec §12.5.
//
// Wire shape:
//
//	POST {callback_url}/v1/attributes/{node_id}
//	Authorization: <cancel_token>          (matches §12.4 async-callback auth)
//	Body: {"delta": { "<field>": <value>, ... }}
//	→ 204 No Content
//
// The handler resolves the supervisor-issued cancel_token to a node_id
// via the AuthLookup callback, then merges the delta into
// rimsky_node_attributes.data and returns 204.

package attributes

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/fallguy/rimsky/core/shared"
)

// Row mirrors a row of `rimsky_node_attributes`. Carried by the local
// NodeAttributesStore below so the HTTP handler can be used by tests
// without depending on the full persistence layer.
type Row struct {
	NodeID     shared.UUID
	RunAttempt int
	Data       map[string]any
	UpdatedAt  time.Time
}

// NodeAttributesStore is the narrow interface the HTTP handler depends
// on. The supervisor adapts the canonical
// `persistence.NodeAttributesStore` to this shape (the persistence
// methods take an additional `tx persistence.Tx`; the callback handler
// always runs outside any caller-owned tx).
type NodeAttributesStore interface {
	Get(ctx context.Context, nodeID shared.UUID) (*Row, error)
	Upsert(ctx context.Context, nodeID shared.UUID, runAttempt int, data map[string]any) error
	MergeDelta(ctx context.Context, nodeID shared.UUID, delta map[string]any) error
}

// AuthLookup resolves a supervisor-issued cancel_token to the node_id it
// authorises. Returns ErrUnauthorizedCallback when the token is unknown
// or doesn't match the URL-supplied node_id. The supervisor wires this
// callback to its in-memory dispatch registry; tests pass a closure.
type AuthLookup func(token string, nodeID shared.UUID) error

// ErrUnauthorizedCallback is the sentinel an AuthLookup returns when the
// cancel_token is missing, unknown, or scoped to a different node. The
// handler maps it to HTTP 401.
var ErrUnauthorizedCallback = errors.New("attributes: unauthorized callback")

// HandlerDeps bundles the callback handler's dependencies. The
// supervisor constructs one of these and registers Handler under
// `/v1/attributes/{node_id}` on its callback router.
type HandlerDeps struct {
	Store  NodeAttributesStore
	Auth   AuthLookup
	Logger shared.Logger
}

// Handler returns the chi-compatible http.Handler for §12.5. It is
// intended to be mounted at `POST /v1/attributes/{node_id}` so chi can
// supply the URL parameter via chi.URLParam.
//
// Auth is required at construction. Passing a HandlerDeps with a nil
// Auth panics: a nil-skip would silently disable callback auth and is
// trivially exploitable. Tests must supply a stub closure.
func Handler(deps HandlerDeps) http.Handler {
	if deps.Auth == nil {
		panic("attributes.Handler: deps.Auth is required (nil would silently disable callback auth)")
	}
	if deps.Store == nil {
		panic("attributes.Handler: deps.Store is required")
	}
	logger := deps.Logger
	if logger == nil {
		logger = shared.SilentLogger{}
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		nodeIDStr := chi.URLParam(r, "node_id")
		nodeID, err := uuid.Parse(nodeIDStr)
		if err != nil {
			http.Error(w, `{"error":"invalid node_id"}`, http.StatusBadRequest)
			return
		}
		token := strings.TrimSpace(r.Header.Get("Authorization"))
		// Strip an optional `Bearer ` prefix; tolerated for executor
		// convenience. Spec §12.5 calls for the bare token in
		// `Authorization`.
		token = strings.TrimPrefix(token, "Bearer ")
		token = strings.TrimSpace(token)
		if token == "" {
			http.Error(w, `{"error":"missing authorization"}`, http.StatusUnauthorized)
			return
		}
		if err := deps.Auth(token, nodeID); err != nil {
			logger.Warn("attributes callback: unauthorized",
				"node_id", nodeID.String(), "error", err.Error())
			http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
			return
		}
		var body struct {
			Delta map[string]any `json:"delta"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, `{"error":"invalid json"}`, http.StatusBadRequest)
			return
		}
		if body.Delta == nil {
			// Per spec §12.5 the body is `{"delta": {...}}`. An empty or
			// missing delta is permitted — it bumps updated_at so the
			// callback's heartbeat-of-progress side-effect still fires.
			body.Delta = map[string]any{}
		}
		if err := deps.Store.MergeDelta(r.Context(), nodeID, body.Delta); err != nil {
			logger.Error("attributes callback: merge failed",
				"node_id", nodeID.String(), "error", err.Error())
			http.Error(w, `{"error":"merge_failed"}`, http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
}
