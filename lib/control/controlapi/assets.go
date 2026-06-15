// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// assets.go — F5. Asset endpoints.
//
//   - GET    /instances/{id}/assets                                  — list
//   - GET    /instances/{id}/assets/{alias}                          — single
//   - GET    /instances/{id}/assets/{alias}/versions                 — proxies DataProcessing.ListVersions
//   - GET    /instances/{id}/assets/{alias}/materialization-history  — lineage join
//   - POST   /instances/{id}/assets/{alias}/materialize              — alias for invalidate message
//   - DELETE /instances/{id}/assets/{alias}                          — operator release + row delete
//
// @concept: asset
//
// "Asset" is a documented compound, not a primitive: it's a claim
// against a `DataProcessing`-capable producer with `lifetime: durable`.
// The address-space is `rimsky_claim_handles` filtered to
// state = 'committed' AND lifetime = 'durable' + producer advertising
// data_processing.
//
// The `{alias}` path parameter is the dotted
// `{template_node_alias}.{claim_alias}` form. We resolve to a
// concrete claim handle by walking the instance's template for the
// node's `stores:` entry whose alias matches.

package controlapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/spec"
	"github.com/rimsky-ai/rimsky-core/lib/protocols/claimproducer"
	"github.com/rimsky-ai/rimsky-core/lib/runtime"
)

func registerAssetsRoutes(r chi.Router, deps AppDeps) {
	r.Get("/instances/{id}/assets", gate(deps, "asset:read", handleListAssets(deps)))
	r.Get("/instances/{id}/assets/{alias}", gate(deps, "asset:read", handleGetAsset(deps)))
	r.Get("/instances/{id}/assets/{alias}/versions", gate(deps, "asset:read", handleAssetVersions(deps)))
	r.Get("/instances/{id}/assets/{alias}/materialization-history", gate(deps, "asset:read", handleAssetMaterializationHistory(deps)))
	r.Post("/instances/{id}/assets/{alias}/materialize", gate(deps, "asset:materialize", handleAssetMaterialize(deps)))
	r.Delete("/instances/{id}/assets/{alias}", gate(deps, "asset:delete", handleDeleteAsset(deps)))
}

// assetItem is the per-asset projection. Address bytes are
// intentionally omitted per `@blessed-invariant 20` — operators read
// `scope`, `producer_name`, `version_id`; never the wire address.
type assetItem struct {
	Alias        string          `json:"alias"`
	ClaimID      string          `json:"claim_id"`
	ProducerName string          `json:"producer_name"`
	Scope        json.RawMessage `json:"scope,omitempty"`
	VersionID    string          `json:"version_id,omitempty"`
	// State + Lifetime replace the pre-Stage-4 `held_durable` bool.
	// For asset queries (post-Stage-2 row discovery is
	// `ListByInstanceAndState(committed, durable)`), every surfaced row
	// has State == "committed" and Lifetime == "durable" by
	// construction. The fields are still surfaced for forward
	// compatibility with operator tooling that wants to filter by
	// state explicitly.
	State        string    `json:"state"`
	Lifetime     string    `json:"lifetime"`
	ClaimedAt    time.Time `json:"claimed_at"`
	HolderNodeID string    `json:"holder_node_id"`
	NodeType     string    `json:"node_type,omitempty"`
}

// handleListAssets returns claim_handles rows for the instance filtered
// to state=committed AND lifetime=durable AND producer advertises
// data_processing.
func handleListAssets(deps AppDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		instanceID, err := uuid.Parse(chi.URLParam(req, "id"))
		if err != nil {
			badRequest(w, "invalid instance id")
			return
		}
		instUUID := shared.UUID(instanceID)
		var (
			rows  []persistence.ClaimHandleRow
			nodes []persistence.NodeRow
			tplSp spec.TemplateSpec
		)
		err = deps.Persist.Transaction(req.Context(), func(ctx context.Context, tx persistence.Tx) error {
			inst, err := deps.Persist.Instances().Get(ctx, instUUID, tx)
			if err != nil {
				return err
			}
			if inst == nil {
				return shared.ErrInstanceNotFound
			}
			r, err := deps.Persist.ClaimHandles().ListByInstanceAndState(
				ctx, instUUID, spec.ClaimHandleStateCommitted, spec.ClaimLifetimeDurable, tx,
			)
			if err != nil {
				return err
			}
			rows = r
			n, err := deps.Persist.Nodes().ListByInstance(ctx, instUUID, tx)
			if err != nil {
				return err
			}
			nodes = n
			tpl, err := deps.Persist.Templates().GetByHash(ctx, inst.TemplateHash, tx)
			if err != nil {
				return err
			}
			if tpl != nil {
				tplSp = tpl.Spec
			}
			return nil
		})
		if err != nil {
			if errors.Is(err, shared.ErrInstanceNotFound) {
				notFoundResp(w, shared.ErrInstanceNotFound.Error())
				return
			}
			writeError(w, err)
			return
		}
		nodeByID := map[shared.UUID]persistence.NodeRow{}
		for _, n := range nodes {
			nodeByID[n.ID] = n
		}
		producerAdvertises := buildDataProcessingPredicate(deps)
		items := make([]assetItem, 0, len(rows))
		for _, r := range rows {
			if r.ProducerName == nil || *r.ProducerName == "" {
				continue
			}
			if !producerAdvertises(*r.ProducerName) {
				continue
			}
			node := nodeByID[r.HolderNodeID]
			// @constraint: precise alias resolution: the template node carries one or
			// more `stores:` entries; the producer_name on the claim
			// handle pinpoints which entry. The (alias, producer) pair
			// is unique per node so the first match is canonical.
			claimAlias := lookupClaimAliasForProducer(tplSp, node.NodeType, *r.ProducerName)
			items = append(items, toAssetItem(r, node, claimAlias))
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"assets": items,
		})
	}
}

// lookupClaimAliasForProducer returns the `claim_alias` (the per-node
// alias declared in `stores:`) for a given producer_name on a node.
// Returns empty when no match — callers fall back to the producer-name
// approximation rather than emitting a half-formed alias.
func lookupClaimAliasForProducer(s spec.TemplateSpec, nodeType, producerName string) string {
	if nodeType == "" || producerName == "" {
		return ""
	}
	for _, n := range s.Nodes {
		if n.Type != nodeType {
			continue
		}
		for _, st := range n.Stores {
			if st.Name == producerName {
				return st.AliasOf()
			}
		}
	}
	return ""
}

// toAssetItem projects a claim_handle row + its holder node + the
// resolved claim alias (from the template's `stores:` declaration) into
// the user-facing assetItem shape. Per spec/plan F5 the alias is the
// dotted `{node_type}.{claim_alias}` form; the caller is responsible
// for resolving claim_alias precisely via `lookupClaimAliasForProducer`.
// When the alias resolution fails (template gone, producer mismatch,
// caller passes empty), the Alias field is left empty so consumers
// don't see a half-formed identifier.
func toAssetItem(r persistence.ClaimHandleRow, node persistence.NodeRow, claimAlias string) assetItem {
	out := assetItem{
		ClaimID:      r.ID.String(),
		Scope:        r.ClaimScopeData,
		VersionID:    r.VersionID,
		State:        string(r.State),
		Lifetime:     string(r.Lifetime),
		ClaimedAt:    r.ClaimedAt,
		HolderNodeID: r.HolderNodeID.String(),
		NodeType:     node.NodeType,
	}
	if r.ProducerName != nil {
		out.ProducerName = *r.ProducerName
	}
	if node.NodeType != "" && claimAlias != "" {
		out.Alias = node.NodeType + "." + claimAlias
	}
	return out
}

// buildDataProcessingPredicate returns a function reporting whether a
// producer advertises the `data_processing` mix-in protocol. Sources
// the cached capabilities from the registry; falls back to "yes" when
// the registry has no entry (the row exists, so the producer is
// known; we conservatively include it rather than silently dropping).
func buildDataProcessingPredicate(deps AppDeps) func(string) bool {
	if deps.Stores == nil {
		return func(string) bool { return true }
	}
	return func(name string) bool {
		p, ok := deps.Stores.Get(name)
		if !ok {
			return true
		}
		caps, err := p.Capabilities(context.Background())
		if err != nil {
			return true
		}
		return caps.AdvertisesProtocol("data_processing")
	}
}

// parseAssetAlias splits `{node_type}.{claim_alias}` into its two
// components. Returns an error if the input is missing the dot or has
// empty halves.
func parseAssetAlias(s string) (nodeType, claimAlias string, err error) {
	idx := strings.LastIndex(s, ".")
	if idx <= 0 || idx == len(s)-1 {
		return "", "", errors.New("asset alias must be '{node_type}.{claim_alias}'")
	}
	return s[:idx], s[idx+1:], nil
}

// resolveAsset finds the claim_handle row for (instance_id, node_type,
// claim_alias). Returns (nil, nil) when no row matches. The lookup
// joins ListByInstanceAndState (state='committed', lifetime='durable')
// + a template walk to map alias → producer_name.
func resolveAsset(
	ctx context.Context, deps AppDeps, tx persistence.Tx,
	instance persistence.InstanceRow, nodeType, claimAlias string,
) (*persistence.ClaimHandleRow, *persistence.NodeRow, error) {
	rows, err := deps.Persist.ClaimHandles().ListByInstanceAndState(
		ctx, instance.ID, spec.ClaimHandleStateCommitted, spec.ClaimLifetimeDurable, tx,
	)
	if err != nil {
		return nil, nil, err
	}
	nodes, err := deps.Persist.Nodes().ListByInstance(ctx, instance.ID, tx)
	if err != nil {
		return nil, nil, err
	}
	var node *persistence.NodeRow
	for i := range nodes {
		if nodes[i].NodeType == nodeType {
			node = &nodes[i]
			break
		}
	}
	if node == nil {
		return nil, nil, nil
	}
	tpl, err := deps.Persist.Templates().GetByHash(ctx, instance.TemplateHash, tx)
	if err != nil {
		return nil, nil, err
	}
	if tpl == nil {
		return nil, nil, nil
	}
	producerName := lookupProducerForAlias(tpl.Spec, nodeType, claimAlias)
	if producerName == "" {
		return nil, node, nil
	}
	for i := range rows {
		if rows[i].HolderNodeID != node.ID {
			continue
		}
		if rows[i].ProducerName != nil && *rows[i].ProducerName == producerName {
			return &rows[i], node, nil
		}
	}
	return nil, node, nil
}

// lookupProducerForAlias walks the template's nodes for nodeType, then
// matches a `stores:` entry whose alias (or name) equals claimAlias.
// Returns the producer's `name` (the `producer_name` column on
// rimsky_claim_handles), or empty when not found.
func lookupProducerForAlias(s spec.TemplateSpec, nodeType, claimAlias string) string {
	if len(s.Nodes) == 0 {
		return ""
	}
	for _, n := range s.Nodes {
		if n.Type != nodeType {
			continue
		}
		for _, st := range n.Stores {
			if st.AliasOf() == claimAlias {
				return st.Name
			}
		}
	}
	return ""
}

func handleGetAsset(deps AppDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		instanceID, err := uuid.Parse(chi.URLParam(req, "id"))
		if err != nil {
			badRequest(w, "invalid instance id")
			return
		}
		nodeType, claimAlias, err := parseAssetAlias(chi.URLParam(req, "alias"))
		if err != nil {
			badRequest(w, err.Error())
			return
		}
		var (
			row  *persistence.ClaimHandleRow
			node *persistence.NodeRow
		)
		err = deps.Persist.Transaction(req.Context(), func(ctx context.Context, tx persistence.Tx) error {
			inst, err := deps.Persist.Instances().Get(ctx, shared.UUID(instanceID), tx)
			if err != nil {
				return err
			}
			if inst == nil {
				return shared.ErrInstanceNotFound
			}
			r, n, err := resolveAsset(ctx, deps, tx, *inst, nodeType, claimAlias)
			row = r
			node = n
			return err
		})
		if err != nil {
			if errors.Is(err, shared.ErrInstanceNotFound) {
				notFoundResp(w, shared.ErrInstanceNotFound.Error())
				return
			}
			writeError(w, err)
			return
		}
		if row == nil {
			notFoundResp(w, "asset not found")
			return
		}
		// @constraint: the path parameter is the user-supplied alias; pass it through
		// verbatim. resolveAsset already validated that the (node_type,
		// claim_alias) pair matches a stores entry on the template.
		writeJSON(w, http.StatusOK, toAssetItem(*row, ifNode(node), claimAlias))
	}
}

// ifNode returns *node when non-nil, else a zero-value NodeRow.
func ifNode(node *persistence.NodeRow) persistence.NodeRow {
	if node == nil {
		return persistence.NodeRow{}
	}
	return *node
}

// handleAssetVersions resolves the asset to its claim_handle, looks up
// the DataProcessing client for the producer, and forwards to
// `ListVersions`. Returns 503 when no DataProcessing registry is wired
// (no producer advertised the protocol at startup) or 404 when the
// asset's producer does not advertise data_processing. Per spec
func handleAssetVersions(deps AppDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		instanceID, err := uuid.Parse(chi.URLParam(req, "id"))
		if err != nil {
			badRequest(w, "invalid instance id")
			return
		}
		nodeType, claimAlias, err := parseAssetAlias(chi.URLParam(req, "alias"))
		if err != nil {
			badRequest(w, err.Error())
			return
		}
		if deps.DataProcessors == nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]any{
				"error": "DataProcessing registry not configured on this process",
			})
			return
		}
		var row *persistence.ClaimHandleRow
		err = deps.Persist.Transaction(req.Context(), func(ctx context.Context, tx persistence.Tx) error {
			inst, err := deps.Persist.Instances().Get(ctx, shared.UUID(instanceID), tx)
			if err != nil {
				return err
			}
			if inst == nil {
				return shared.ErrInstanceNotFound
			}
			r, _, err := resolveAsset(ctx, deps, tx, *inst, nodeType, claimAlias)
			row = r
			return err
		})
		if err != nil {
			if errors.Is(err, shared.ErrInstanceNotFound) {
				notFoundResp(w, shared.ErrInstanceNotFound.Error())
				return
			}
			writeError(w, err)
			return
		}
		if row == nil {
			notFoundResp(w, "asset not found")
			return
		}
		if row.ProducerName == nil || *row.ProducerName == "" {
			notFoundResp(w, "asset has no producer recorded")
			return
		}
		client, ok := deps.DataProcessors.Get(*row.ProducerName)
		if !ok {
			notFoundResp(w, "producer does not advertise data_processing")
			return
		}
		resp, err := client.ListVersions(req.Context(), runtime.ListVersionsInput{
			ProducerName:  *row.ProducerName,
			ClaimHandleID: row.ID.String(),
		})
		if err != nil {
			// @constraint: A remote producer returns *peer.ProducerCallError; writeError
			// surfaces the producer's error class + message under 502/422
			// instead of discarding them into a generic body.
			writeError(w, err)
			return
		}
		items := make([]map[string]any, 0, len(resp.Versions))
		for _, v := range resp.Versions {
			// @constraint: `producer_metadata` is opaque bytes per @blessed-invariant
			// 20-class; surface as raw JSON when valid, otherwise base64
			// is implicit via the JSON-encoded byte slice.
			items = append(items, map[string]any{
				"version_id":          v.VersionID,
				"committed_at_unix_s": v.CommittedAtUnixS,
				"producer_metadata":   json.RawMessage(v.ProducerMetadata),
			})
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"versions": items,
		})
	}
}

// handleAssetMaterializationHistory returns claim_terminal lineage rows
// for the asset's claim_handle joined with their parent runs and
// frames.
func handleAssetMaterializationHistory(deps AppDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		instanceID, err := uuid.Parse(chi.URLParam(req, "id"))
		if err != nil {
			badRequest(w, "invalid instance id")
			return
		}
		nodeType, claimAlias, err := parseAssetAlias(chi.URLParam(req, "alias"))
		if err != nil {
			badRequest(w, err.Error())
			return
		}
		var (
			row *persistence.ClaimHandleRow
		)
		err = deps.Persist.Transaction(req.Context(), func(ctx context.Context, tx persistence.Tx) error {
			inst, err := deps.Persist.Instances().Get(ctx, shared.UUID(instanceID), tx)
			if err != nil {
				return err
			}
			if inst == nil {
				return shared.ErrInstanceNotFound
			}
			r, _, err := resolveAsset(ctx, deps, tx, *inst, nodeType, claimAlias)
			row = r
			return err
		})
		if err != nil {
			if errors.Is(err, shared.ErrInstanceNotFound) {
				notFoundResp(w, shared.ErrInstanceNotFound.Error())
				return
			}
			writeError(w, err)
			return
		}
		if row == nil {
			notFoundResp(w, "asset not found")
			return
		}
		records, err := deps.Persist.Lineage().GetByClaimHandleID(req.Context(), row.ID)
		if err != nil {
			writeError(w, err)
			return
		}
		items := make([]lineageRecordItem, 0, len(records))
		for _, lr := range records {
			items = append(items, toLineageItem(lr))
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"materialization_history": items,
		})
	}
}

// materializeRequest carries the operator-supplied invalidate-message
// shape. Mirrors `postMessageRequest` but with explicit field naming
// since the materialize endpoint always synthesises the
// runtime-synthetic `asset/materialize` envelope.
type materializeRequest struct {
	Payload json.RawMessage `json:"payload,omitempty"`
	Reason  string          `json:"reason,omitempty"`
}

// errAssetMaterializeUnknownNode is returned from inside the
// materialize tx when the operator-supplied alias does not resolve to a
// declared node-type on the target instance. Refusing loudly is
// load-bearing: the materialize envelope addresses its receiver by
// concrete node uuid via `payload.wake_node_ids`, and that list is
// populated from the resolved row. An empty list (or one pointing at a
// nonexistent node) would let the frame promote and stale-mark
// nothing — a silent no-op. Mapped to HTTP 400 by the outer handler.
type errAssetMaterializeUnknownNode struct {
	nodeType string
}

func (e errAssetMaterializeUnknownNode) Error() string {
	return fmt.Sprintf("alias %q does not reference a declared node type on this instance", e.nodeType)
}

// findNodeByType is a small helper that finds the rimsky_nodes row for
// a (instance, node_type) pair. The NodeTable does not surface this
// query directly; we walk ListByInstance and match by type. Acceptable
// because the per-instance node count is small (template-defined).
func findNodeByType(
	ctx context.Context, deps AppDeps, tx persistence.Tx,
	instanceID shared.UUID, nodeType string,
) (*persistence.NodeRow, error) {
	nodes, err := deps.Persist.Nodes().ListByInstance(ctx, instanceID, tx)
	if err != nil {
		return nil, err
	}
	for i := range nodes {
		if nodes[i].NodeType == nodeType {
			return &nodes[i], nil
		}
	}
	return nil, nil
}

func handleAssetMaterialize(deps AppDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		instanceID, err := uuid.Parse(chi.URLParam(req, "id"))
		if err != nil {
			badRequest(w, "invalid instance id")
			return
		}
		nodeType, _, err := parseAssetAlias(chi.URLParam(req, "alias"))
		if err != nil {
			badRequest(w, err.Error())
			return
		}
		var body materializeRequest
		if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
			// @constraint: empty body is acceptable; surface only true JSON errors.
			// `json.Decoder.Decode` returns `io.EOF` on empty input.
			if !errors.Is(err, io.EOF) {
				badRequest(w, "invalid JSON body: "+err.Error())
				return
			}
		}
		isDryRun := ModeFromContext(req.Context()) == authModeDryRun
		// @constraint: msgID is assigned by EnqueueSyntheticWakeFrame inside the
		// tx below; it carries the seeded envelope's id back to the operator
		// in the response.
		var msgID shared.UUID
		// @constraint: fold `reason` into the payload as a named field so the
		// receiver's `{{trigger.message.payload.reason}}` substitution
		// resolves. Empty when not provided.
		payload := body.Payload
		if body.Reason != "" {
			merged := map[string]any{"reason": body.Reason}
			if len(payload) > 0 {
				_ = json.Unmarshal(payload, &merged)
				merged["reason"] = body.Reason
			}
			payload, _ = json.Marshal(merged)
		}
		// @constraint: the per-row routing `target` field is retired by the
		// message-schema-layer reshape; receivers are addressed by the
		// cascade walker via subscription edges. The target node alias is
		// preserved in the payload so the legacy operator surface keeps
		// matching.
		//
		// @deliberate: runtime-synthetic-only — `asset/materialize` is a
		// runtime-internal type that never flows through the receipt
		// handler; it is constructed directly via
		// `EnqueueSyntheticWakeFrame` and so bypasses both the receipt-time
		// registry gate and the reserved-field guard. The auto-injected
		// `target_node` field is therefore not subject to author-declared
		// `body_schema` constraints (which apply only to types in the
		// template's `messages:` registry). A future hypothetical
		// publish-through-asset path that routes through the receipt
		// handler would need to model `target_node` as a top-level envelope
		// field or declare it in the body_schema.
		mergedPayload := map[string]any{}
		if len(payload) > 0 {
			_ = json.Unmarshal(payload, &mergedPayload)
		}
		mergedPayload["target_node"] = nodeType
		err = deps.Persist.Transaction(req.Context(), func(ctx context.Context, tx persistence.Tx) error {
			inst, err := deps.Persist.Instances().Get(ctx, shared.UUID(instanceID), tx)
			if err != nil {
				return err
			}
			if inst == nil {
				return shared.ErrInstanceNotFound
			}
			if inst.TerminatedAt != nil {
				return errInstanceTerminated
			}
			// @constraint: resolve the target alias to its concrete node uuid so
			// the synthetic envelope's `wake_node_ids` list can stale-mark
			// the right row at frame promotion. Under the message-schema-
			// layer reshape, `advanceOneFrame` reads `wake_node_ids` (NOT
			// the retired source_node_ids list) and template authors cannot
			// subscribe to runtime-synthetic types like `asset/materialize`;
			// without this list the frame would promote, stale-mark
			// nothing, and silently no-op the materialize trigger.
			node, err := findNodeByType(ctx, deps, tx, shared.UUID(instanceID), nodeType)
			if err != nil {
				return err
			}
			if node == nil {
				// @constraint: target alias is not declared as a node-type in the
				// template — refuse loudly rather than enqueue a no-op.
				return errAssetMaterializeUnknownNode{nodeType: nodeType}
			}
			// @constraint: dry-run gate — validation (instance exists + not
			// terminated + target alias resolved) has succeeded. Signal the
			// outer code to write the synthetic envelope; the tx rolls back
			// without enqueuing the message.
			if isDryRun {
				return errDryRunOK
			}
			// @constraint: runtime-synthetic envelope — the slash-bearing
			// `asset/materialize` type-path follows the same
			// runtime-synthetic pattern as `node/invalidate` /
			// `instance/root` / `node/reset`; receivers are addressed by
			// UUID via the `wake_node_ids` payload field read at frame
			// promotion (`graph/frame/engine.go::advanceOneFrame`), not by
			// template subscription. The type is NOT in the template's
			// `messages:` registry; this endpoint bypasses the receipt-time
			// gate by going through the in-process EnqueueSyntheticWakeFrame
			// helper.
			//
			// @deliberate: `sender_kind: "instance"` matches the runtime-
			// synthetic-envelope convention even though the materialize was
			// operator-INITIATED — the envelope body is runtime-synthesized
			// (the operator did not author the `wake_node_ids` list), so
			// the discriminator marks it as instance-side per
			// `runner_emit_message.go::emitCascadeMessageInTx`.
			//
			// @constraint: the frame is enqueued in the same tx so a crash
			// mid-flow cannot leave a pending message with no frame to
			// carry it, nor a frame with no message.
			seededID, _, err := runtime.EnqueueSyntheticWakeFrame(ctx, tx, deps.Persist,
				shared.UUID(instanceID), "asset/materialize", "",
				[]shared.UUID{node.ID}, mergedPayload)
			if err != nil {
				return err
			}
			msgID = seededID
			return nil
		})
		if isDryRun && errors.Is(err, errDryRunOK) {
			WriteDryRunResponseForced(w, "would_have_materialized", map[string]any{
				"instance_id": instanceID.String(),
				"alias":       chi.URLParam(req, "alias"),
				"reason":      body.Reason,
			})
			return
		}
		if err != nil {
			if errors.Is(err, shared.ErrInstanceNotFound) {
				notFoundResp(w, shared.ErrInstanceNotFound.Error())
				return
			}
			if errors.Is(err, errInstanceTerminated) {
				writeJSON(w, http.StatusConflict, map[string]any{"error": err.Error()})
				return
			}
			var unknownNode errAssetMaterializeUnknownNode
			if errors.As(err, &unknownNode) {
				badRequest(w, unknownNode.Error())
				return
			}
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusCreated, postMessageResponse{MessageID: msgID.String()})
	}
}

// handleDeleteAsset implements the operator-driven asset delete per
// F5 step 6: refuse if any in-flight run holds the claim; otherwise
// call ClaimProducer.Release; DELETE the claim_handle row; audit.
func handleDeleteAsset(deps AppDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		instanceID, err := uuid.Parse(chi.URLParam(req, "id"))
		if err != nil {
			badRequest(w, "invalid instance id")
			return
		}
		nodeType, claimAlias, err := parseAssetAlias(chi.URLParam(req, "alias"))
		if err != nil {
			badRequest(w, err.Error())
			return
		}
		isDryRun := ModeFromContext(req.Context()) == authModeDryRun
		var (
			row    *persistence.ClaimHandleRow
			active []persistence.ClaimHolderRow
		)
		err = deps.Persist.Transaction(req.Context(), func(ctx context.Context, tx persistence.Tx) error {
			inst, err := deps.Persist.Instances().Get(ctx, shared.UUID(instanceID), tx)
			if err != nil {
				return err
			}
			if inst == nil {
				return shared.ErrInstanceNotFound
			}
			r, _, err := resolveAsset(ctx, deps, tx, *inst, nodeType, claimAlias)
			if err != nil {
				return err
			}
			if r == nil {
				return errAssetNotFound
			}
			row = r
			holders, err := deps.Persist.ClaimHolders().ListByClaimHandleID(ctx, r.ID, tx)
			if err != nil {
				return err
			}
			for _, h := range holders {
				if h.State == persistence.ClaimHolderStateActive {
					active = append(active, h)
				}
			}
			// @constraint: dry-run gate: instance + asset resolved successfully.
			// Defer the in-flight-holder check to outside the tx (a real
			// call surfaces it as 409 after the tx commits) — both real
			// and dry-run paths share the same downstream check.
			if isDryRun {
				return errDryRunOK
			}
			return nil
		})
		if isDryRun && errors.Is(err, errDryRunOK) {
			// @constraint: in-flight-holder check matches the real-call's post-tx
			// behaviour: a dry-run against an asset with active holders
			// surfaces the same 409 a real call would.
			if len(active) > 0 {
				writeJSON(w, http.StatusConflict, map[string]any{
					"error":        "asset has in-flight holder runs; refuse delete",
					"active_count": len(active),
				})
				return
			}
			WriteDryRunResponseForced(w, "would_have_deleted_asset", map[string]any{
				"instance_id": instanceID.String(),
				"alias":       chi.URLParam(req, "alias"),
				"node_type":   nodeType,
			})
			return
		}
		if err != nil {
			if errors.Is(err, shared.ErrInstanceNotFound) {
				notFoundResp(w, shared.ErrInstanceNotFound.Error())
				return
			}
			if errors.Is(err, errAssetNotFound) {
				notFoundResp(w, "asset not found")
				return
			}
			writeError(w, err)
			return
		}
		if len(active) > 0 {
			writeJSON(w, http.StatusConflict, map[string]any{
				"error":        "asset has in-flight holder runs; refuse delete",
				"active_count": len(active),
			})
			return
		}
		// @constraint: Producer.Release — the producer registry holds the concrete
		// ClaimProducer. Skip when no producer name is recorded (defensive).
		if deps.Stores != nil && row.ProducerName != nil {
			if producer, ok := deps.Stores.Get(*row.ProducerName); ok {
				if err := producer.Release(req.Context(), claimproducer.ClaimID(row.ID.String()), row.ClaimScopeData, row.Address); err != nil {
					// @constraint: A remote producer returns *peer.ProducerCallError;
					// writeError surfaces the producer's error class +
					// message under 502/422 ("your producer rejected this")
					// instead of the bare 500 that used to mask them as a
					// rimsky-internal failure. The claim_handle row is
					// intentionally left in place — the delete did not
					// happen; the operator can fix the producer and retry.
					writeError(w, err)
					return
				}
			}
		}
		// @constraint: delete the row via the absence-guarded DeleteResolved path —
		// the row is state='committed' / lifetime='durable' with
		// holder_supervisor_id IS NULL by construction post-Promote
		// (Stage 4 of the claim-handle state-column refactor: the CHECK
		// constraint nulls holder_supervisor_id whenever state exits
		// 'active').
		// @blessed-invariant 4 (post-refactor): non-active-row deletions
		// are guarded by absence + the row-discovery query filter.
		err = deps.Persist.Transaction(req.Context(), func(ctx context.Context, tx persistence.Tx) error {
			return deps.Persist.ClaimHandles().DeleteResolved(ctx, row.ID, tx)
		})
		if err != nil {
			writeError(w, err)
			return
		}
		deps.Logger.Info("asset.deleted",
			"claim_id", row.ID.String(),
			"instance_id", instanceID.String(),
			"node_type", nodeType,
			"claim_alias", claimAlias)
		writeJSON(w, http.StatusOK, map[string]any{"deleted": true})
	}
}

// errAssetNotFound is the sentinel surfaced inside a tx for the
// asset-not-found path. The handler maps it to 404 outside the tx.
var errAssetNotFound = errors.New("asset not found")
