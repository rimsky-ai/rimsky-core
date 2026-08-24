// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

// @concept: asset

package controlapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/auth"
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
	r.Delete("/instances/{id}/assets/{alias}", gate(deps, "asset:delete", handleDeleteAsset(deps)))
}

type assetItem struct {
	Alias        string          `json:"alias"`
	ClaimID      string          `json:"claim_id"`
	ProducerName string          `json:"producer_name"`
	Scope        json.RawMessage `json:"scope,omitempty"`
	VersionID    string          `json:"version_id,omitempty"`
	State        string          `json:"state"`
	Lifetime     string          `json:"lifetime"`
	ClaimedAt    time.Time       `json:"claimed_at"`
	HolderNodeID string          `json:"holder_node_id"`
	NodeType     string          `json:"node_type,omitempty"`
}

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
			advertises, err := producerAdvertises(req.Context(), *r.ProducerName)
			if err != nil {
				writeError(w, err)
				return
			}
			if !advertises {
				continue
			}
			node := nodeByID[r.HolderNodeID]
			claimAlias := lookupClaimAliasForProducer(tplSp, node.NodeType, *r.ProducerName)
			if node.NodeType == "" || claimAlias == "" {
				continue
			}
			items = append(items, toAssetItem(r, node, claimAlias))
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"assets": items,
		})
	}
}

func lookupClaimAliasForProducer(s spec.TemplateSpec, nodeType, producerName string) string {
	if nodeType == "" || producerName == "" {
		return ""
	}
	for _, n := range s.Nodes {
		if n.Type != nodeType {
			continue
		}
		for _, st := range n.ClaimProducers {
			if st.Name == producerName {
				return st.AliasOf()
			}
		}
	}
	return ""
}

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

func buildDataProcessingPredicate(deps AppDeps) func(context.Context, string) (bool, error) {
	if deps.ClaimProducers == nil {
		return func(context.Context, string) (bool, error) { return false, nil }
	}
	return func(ctx context.Context, name string) (bool, error) {
		p, ok, err := deps.ClaimProducers.ResolveWithContext(ctx, name, "", nil)
		if err != nil {
			return false, fmt.Errorf("resolving claim producer %q: %w", name, err)
		}
		if !ok {
			return false, nil
		}
		caps, err := p.Capabilities(ctx)
		if err != nil {
			return false, fmt.Errorf("claim producer %q capabilities: %w", name, err)
		}
		return caps.AdvertisesProtocol("data_processing"), nil
	}
}

func parseAssetAlias(s string) (nodeType, claimAlias string, err error) {
	idx := strings.LastIndex(s, ".")
	if idx <= 0 || idx == len(s)-1 {
		return "", "", errors.New("asset alias must be '{node_type}.{claim_alias}'")
	}
	return s[:idx], s[idx+1:], nil
}

func resolveAsset(
	ctx context.Context, deps AppDeps, instance persistence.InstanceRow, nodeType, claimAlias string, tx persistence.Tx,
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
	advertises, err := buildDataProcessingPredicate(deps)(ctx, producerName)
	if err != nil {
		return nil, nil, err
	}
	if !advertises {
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

func lookupProducerForAlias(s spec.TemplateSpec, nodeType, claimAlias string) string {
	if len(s.Nodes) == 0 {
		return ""
	}
	for _, n := range s.Nodes {
		if n.Type != nodeType {
			continue
		}
		for _, st := range n.ClaimProducers {
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
			r, n, err := resolveAsset(ctx, deps, *inst, nodeType, claimAlias, tx)
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
		writeJSON(w, http.StatusOK, toAssetItem(*row, ifNode(node), claimAlias))
	}
}

func ifNode(node *persistence.NodeRow) persistence.NodeRow {
	if node == nil {
		return persistence.NodeRow{}
	}
	return *node
}

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
			r, _, err := resolveAsset(ctx, deps, *inst, nodeType, claimAlias, tx)
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
			writeError(w, err)
			return
		}
		items := make([]map[string]any, 0, len(resp.Versions))
		for _, v := range resp.Versions {
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
			r, _, err := resolveAsset(ctx, deps, *inst, nodeType, claimAlias, tx)
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
		if ModeFromContext(req.Context()) == auth.ModeDryRun {
			handleDeleteAssetDryRun(deps, w, req, instanceID, nodeType, claimAlias)
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
			r, _, err := resolveAsset(ctx, deps, *inst, nodeType, claimAlias, tx)
			if err != nil {
				return err
			}
			if r == nil {
				return errAssetNotFound
			}
			row = r
			if _, err := deps.Persist.ClaimHandles().LockForUpdate(ctx, r.ID, tx); err != nil {
				return err
			}
			holders, err := deps.Persist.ClaimHolders().ListByClaimHandleID(ctx, r.ID, tx)
			if err != nil {
				return err
			}
			if active := activeAssetHolders(holders); len(active) > 0 {
				return &assetHasActiveHoldersError{count: len(active)}
			}
			if row.ProducerName == nil || deps.ClaimProducers == nil {
				return errAssetProducerUnresolvable
			}
			producer, ok, resolveErr := deps.ClaimProducers.ResolveWithContext(ctx, *row.ProducerName, "", tx)
			if resolveErr != nil {
				return fmt.Errorf("asset delete: resolving producer %q: %w", *row.ProducerName, resolveErr)
			}
			if !ok {
				return errAssetProducerUnresolvable
			}
			if err := producer.Release(ctx, claimproducer.ClaimID(row.ID.String()), row.ClaimScopeData, row.Address, row.ProducerLeaseToken); err != nil {
				return fmt.Errorf("asset delete: producer release: %w", err)
			}
			deleted, err := deps.Persist.ClaimHandles().DeleteResolvedIfNoActiveHolders(ctx, row.ID, tx)
			if err != nil {
				deps.Logger.Error("CONTROLAPI.ASSETDELETE.INCONSISTENT",
					"claim_id", row.ID.String(),
					"instance_id", instanceID.String(),
					"producer_name", *row.ProducerName,
					"node_type", nodeType,
					"claim_alias", claimAlias,
					"err", err.Error())
				return fmt.Errorf("asset delete: producer released but delete failed, manual reconciliation required: %w", err)
			}
			if !deleted {
				deps.Logger.Error("CONTROLAPI.ASSETDELETE.INCONSISTENT",
					"claim_id", row.ID.String(),
					"instance_id", instanceID.String(),
					"producer_name", *row.ProducerName,
					"node_type", nodeType,
					"claim_alias", claimAlias,
					"reason", "active holder registered after producer release; claim handle row retained")
				return &assetHasActiveHoldersError{count: -1, producerReleased: true}
			}
			return nil
		})
		if err != nil {
			writeAssetDeleteError(w, err)
			return
		}
		deps.Logger.Info("CONTROLAPI.ASSET.DELETED",
			"claim_id", row.ID.String(),
			"instance_id", instanceID.String(),
			"node_type", nodeType,
			"claim_alias", claimAlias)
		writeJSON(w, http.StatusOK, map[string]any{"deleted": true})
	}
}

func handleDeleteAssetDryRun(
	deps AppDeps, w http.ResponseWriter, req *http.Request,
	instanceID uuid.UUID, nodeType, claimAlias string,
) {
	var active []persistence.ClaimHolderRow
	err := deps.Persist.Transaction(req.Context(), func(ctx context.Context, tx persistence.Tx) error {
		inst, err := deps.Persist.Instances().Get(ctx, shared.UUID(instanceID), tx)
		if err != nil {
			return err
		}
		if inst == nil {
			return shared.ErrInstanceNotFound
		}
		r, _, err := resolveAsset(ctx, deps, *inst, nodeType, claimAlias, tx)
		if err != nil {
			return err
		}
		if r == nil {
			return errAssetNotFound
		}
		holders, err := deps.Persist.ClaimHolders().ListByClaimHandleID(ctx, r.ID, tx)
		if err != nil {
			return err
		}
		active = activeAssetHolders(holders)
		return errDryRunOK
	})
	if errors.Is(err, errDryRunOK) {
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
	writeAssetDeleteError(w, err)
}

func activeAssetHolders(holders []persistence.ClaimHolderRow) []persistence.ClaimHolderRow {
	var active []persistence.ClaimHolderRow
	for _, h := range holders {
		if h.State == persistence.ClaimHolderStateActive {
			active = append(active, h)
		}
	}
	return active
}

func writeAssetDeleteError(w http.ResponseWriter, err error) {
	if errors.Is(err, shared.ErrInstanceNotFound) {
		notFoundResp(w, shared.ErrInstanceNotFound.Error())
		return
	}
	if errors.Is(err, errAssetNotFound) {
		notFoundResp(w, "asset not found")
		return
	}
	if errors.Is(err, errAssetProducerUnresolvable) {
		writeJSON(w, http.StatusConflict, map[string]any{"error": errAssetProducerUnresolvable.Error()})
		return
	}
	var activeErr *assetHasActiveHoldersError
	if errors.As(err, &activeErr) {
		body := map[string]any{"error": "asset has in-flight holder runs; refuse delete"}
		if activeErr.count >= 0 {
			body["active_count"] = activeErr.count
		}
		if activeErr.producerReleased {
			body["producer_released"] = true
			body["error"] = "asset has in-flight holder runs; refuse delete; producer was already released (inconsistent state, needs reconciliation)"
		}
		writeJSON(w, http.StatusConflict, body)
		return
	}
	writeError(w, err)
}

type assetHasActiveHoldersError struct {
	count            int
	producerReleased bool
}

func (e *assetHasActiveHoldersError) Error() string {
	return "asset has in-flight holder runs; refuse delete"
}

var errAssetNotFound = errors.New("asset not found")

var errAssetProducerUnresolvable = errors.New("asset producer not resolvable; refusing delete to avoid leaking producer-side state")
