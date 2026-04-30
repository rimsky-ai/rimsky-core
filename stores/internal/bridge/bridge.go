// Package bridge mounts the HTTP+JSON bridge handlers per spec §5.2 onto
// an http.ServeMux. Every store-service exposes the same five routes
// (`/v1/capabilities`, `/v1/open`, `/v1/commit`, `/v1/abandon`,
// `/v1/release`); rather than duplicating ~95% identical handlers
// across stores/{filesystem,postgres,stub}/server, each store-service
// calls Mount with its own genv1.StoreServiceServer implementation.
//
// Per spec §15 (out of scope): the bridge currently surfaces every
// substrate error as HTTP 500 / gRPC Internal. Finer-grained
// status.Code mapping (NotFound, AlreadyExists, ResourceExhausted,
// FailedPrecondition, …) is deferred to a follow-up cycle.
package bridge

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"

	genv1 "github.com/fallguy/rimsky/proto/v1/gen"
)

// errBadRequest tags decode errors so the handler can map them to 400
// while keeping all substrate errors at 500 (per the spec §15 deferral
// noted on the package comment).
var errBadRequest = errors.New("bridge: bad request")

// errUnknownVerb tags an unrecognised verb path; mapped to 404.
var errUnknownVerb = errors.New("bridge: unknown verb")

// Mount registers the five bridge routes on mux, dispatching each one
// through the supplied genv1.StoreServiceServer.
func Mount(mux *http.ServeMux, srv genv1.StoreServiceServer) {
	mux.HandleFunc("/v1/capabilities", handler(srv, "capabilities"))
	mux.HandleFunc("/v1/open", handler(srv, "open"))
	mux.HandleFunc("/v1/commit", handler(srv, "commit"))
	mux.HandleFunc("/v1/abandon", handler(srv, "abandon"))
	mux.HandleFunc("/v1/release", handler(srv, "release"))
}

// handler builds an HTTP handler for a single verb. Per the spec §15
// deferral above, all substrate errors surface as HTTP 500.
func handler(srv genv1.StoreServiceServer, verb string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, "read body: "+err.Error(), http.StatusBadRequest)
			return
		}
		respObj, callErr := dispatch(r.Context(), srv, verb, body)
		switch {
		case errors.Is(callErr, errBadRequest):
			http.Error(w, callErr.Error(), http.StatusBadRequest)
			return
		case errors.Is(callErr, errUnknownVerb):
			http.Error(w, callErr.Error(), http.StatusNotFound)
			return
		case callErr != nil:
			http.Error(w, callErr.Error(), http.StatusInternalServerError)
			return
		}
		writeJSON(w, respObj)
	}
}

// dispatch reads the request body, decodes the verb-specific JSON
// payload, and forwards to the StoreServiceServer.
func dispatch(ctx context.Context, srv genv1.StoreServiceServer, verb string, body []byte) (proto.Message, error) {
	switch verb {
	case "capabilities":
		return srv.Capabilities(ctx, &genv1.CapabilitiesRequest{})
	case "open":
		var req openBody
		if err := decodeOptional(body, &req); err != nil {
			return nil, fmt.Errorf("%w: %s", errBadRequest, err.Error())
		}
		return srv.Open(ctx, &genv1.OpenRequest{
			ClaimId:   req.ClaimID,
			StoreName: req.StoreName,
			Selector:  req.Selector,
			Intent:    req.Intent,
			Alias:     req.Alias,
		})
	case "commit":
		var req actionBody
		if err := decodeOptional(body, &req); err != nil {
			return nil, fmt.Errorf("%w: %s", errBadRequest, err.Error())
		}
		return srv.Commit(ctx, &genv1.CommitRequest{
			ClaimId: req.ClaimID, Region: req.Region, Address: req.Address,
		})
	case "abandon":
		var req actionBody
		if err := decodeOptional(body, &req); err != nil {
			return nil, fmt.Errorf("%w: %s", errBadRequest, err.Error())
		}
		return srv.Abandon(ctx, &genv1.AbandonRequest{
			ClaimId: req.ClaimID, Region: req.Region, Address: req.Address,
		})
	case "release":
		var req actionBody
		if err := decodeOptional(body, &req); err != nil {
			return nil, fmt.Errorf("%w: %s", errBadRequest, err.Error())
		}
		return srv.Release(ctx, &genv1.ReleaseRequest{
			ClaimId: req.ClaimID, Region: req.Region, Address: req.Address,
		})
	}
	return nil, errUnknownVerb
}

// openBody is the JSON shape decoded from POST /v1/open.
type openBody struct {
	ClaimID   string `json:"claim_id"`
	StoreName string `json:"store_name"`
	Selector  string `json:"selector"`
	Intent    string `json:"intent"`
	Alias     string `json:"alias"`
}

// actionBody is the JSON shape decoded from POST /v1/{commit,abandon,
// release}. Same fields for all three.
type actionBody struct {
	ClaimID string `json:"claim_id"`
	Region  []byte `json:"region"`
	Address []byte `json:"address"`
}

// decodeOptional accepts either an empty body or a JSON object.
func decodeOptional(body []byte, v any) error {
	if len(strings.TrimSpace(string(body))) == 0 {
		return nil
	}
	if err := json.Unmarshal(body, v); err != nil {
		return fmt.Errorf("decode JSON body: %w", err)
	}
	return nil
}

// writeJSON serializes the response with protojson, the canonical
// proto3-JSON encoder. Required because OpenResponse uses a oneof that
// encoding/json does not produce in the proto3-JSON discriminator
// shape (`{"acquired": {...}}` / `{"unavailable": {}}`).
func writeJSON(w http.ResponseWriter, v proto.Message) {
	w.Header().Set("Content-Type", "application/json")
	data, err := protojson.Marshal(v)
	if err != nil {
		http.Error(w, "marshal: "+err.Error(), http.StatusInternalServerError)
		return
	}
	_, _ = w.Write(data)
}
